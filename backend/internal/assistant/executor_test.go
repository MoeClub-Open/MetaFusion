package assistant

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

func TestMissionActorPreservesFullCatalogIdentity(t *testing.T) {
	src := catalog.User{
		ID:                  uuid.NewString(),
		Username:            "pat-curator",
		Email:               "curator@example.org",
		Groups:              []string{"editor", "reviewer"},
		Permissions:         []string{catalog.PermissionEntityEdit, catalog.PermissionReviewBypass},
		FromPAT:             true,
		CatalogReviewExempt: true,
		IsThirdParty:        false,
		TokenName:           "ci-agent",
		TokenVersion:        7,
	}
	actor := ActorFromCatalogUser(src)
	raw, err := json.Marshal(actor)
	if err != nil {
		t.Fatal(err)
	}
	var decoded MissionActor
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	direct := decoded.catalogUser(false)
	if !direct.FromPAT || !direct.CatalogReviewExempt || direct.TokenName != "ci-agent" || direct.TokenVersion != 7 || direct.Email != src.Email || len(direct.Groups) != 2 {
		t.Fatalf("actor lost identity fields across JSON round-trip: %+v", direct)
	}
	prOnly := decoded.catalogUser(true)
	if prOnly.Can(catalog.PermissionReviewBypass) || prOnly.CatalogReviewExempt || prOnly.CanBypassReview() {
		t.Fatalf("preferPR must clear both PermissionReviewBypass and CatalogReviewExempt: %+v", prOnly)
	}

	adminActor := ActorFromCatalogUser(catalog.User{ID: uuid.NewString(), Username: "root", Permissions: []string{"*"}})
	if !adminActor.catalogUser(false).CanBypassReview() {
		t.Fatal("admin actor with preferPR=false should retain CanBypassReview")
	}
	if adminActor.catalogUser(true).CanBypassReview() {
		t.Fatal("admin actor with preferPR=true must have ForcePullRequest enforced")
	}
}

type fakeExecState struct {
	mu            sync.Mutex
	notifications []string
	conversations map[string][]byte
	requests      map[string]*fakeChatReqRow
}

type fakeChatReqRow struct {
	id               string
	ownerID          string
	conversationID   string
	status           string
	mode             string
	response         []byte
	executionPayload []byte
	leaseOwner       string
	started          bool
	stale            bool
}

type fakeExecDriver struct{}

var (
	fakeExecDriverOnce sync.Once
	fakeStatesMu       sync.Mutex
	fakeStates         = map[string]*fakeExecState{}
)

func openFakeExecDB(t *testing.T, state *fakeExecState) *sql.DB {
	t.Helper()
	fakeExecDriverOnce.Do(func() {
		sql.Register("assistant_fake_exec", fakeExecDriver{})
	})
	key := uuid.NewString()
	fakeStatesMu.Lock()
	fakeStates[key] = state
	fakeStatesMu.Unlock()
	t.Cleanup(func() {
		fakeStatesMu.Lock()
		delete(fakeStates, key)
		fakeStatesMu.Unlock()
	})
	db, err := sql.Open("assistant_fake_exec", key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func (fakeExecDriver) Open(name string) (driver.Conn, error) {
	fakeStatesMu.Lock()
	st := fakeStates[name]
	fakeStatesMu.Unlock()
	if st == nil {
		return nil, fmt.Errorf("missing state %s", name)
	}
	return &fakeExecConn{st: st}, nil
}

type fakeExecConn struct{ st *fakeExecState }
type fakeExecTx struct{}
type fakeExecRows struct {
	cols []string
	vals [][]driver.Value
	idx  int
}

func (c *fakeExecConn) Prepare(string) (driver.Stmt, error) { return nil, fmt.Errorf("unsupported") }
func (c *fakeExecConn) Close() error                        { return nil }
func (c *fakeExecConn) Begin() (driver.Tx, error)           { return fakeExecTx{}, nil }
func (fakeExecTx) Commit() error                            { return nil }
func (fakeExecTx) Rollback() error                          { return nil }
func (r *fakeExecRows) Columns() []string                   { return r.cols }
func (r *fakeExecRows) Close() error                        { return nil }
func (r *fakeExecRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.vals) {
		return io.EOF
	}
	copy(dest, r.vals[r.idx])
	r.idx++
	return nil
}

func toBytes(v driver.Value) []byte {
	switch x := v.(type) {
	case []byte:
		return append([]byte(nil), x...)
	case string:
		return []byte(x)
	default:
		return nil
	}
}

func (c *fakeExecConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	q := strings.TrimSpace(query)
	switch {
	case strings.HasPrefix(q, "SELECT pg_notify"):
		c.st.notifications = append(c.st.notifications, fmt.Sprint(args[1].Value))
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(q, "UPDATE assistant.conversations SET messages=$1,model_id="):
		convID := fmt.Sprint(args[3].Value)
		c.st.conversations[convID] = toBytes(args[0].Value)
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(q, "UPDATE assistant.conversations SET messages=$1,updated_at=now()"):
		convID := fmt.Sprint(args[1].Value)
		c.st.conversations[convID] = toBytes(args[0].Value)
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(q, "UPDATE assistant.chat_requests SET mode='background'"):
		id := fmt.Sprint(args[2].Value)
		row := c.st.requests[id]
		if row == nil || row.status != "pending" {
			return driver.RowsAffected(0), nil
		}
		row.mode = "background"
		row.response = toBytes(args[0].Value)
		row.executionPayload = toBytes(args[1].Value)
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(q, "UPDATE assistant.chat_requests SET status='failed',response=$1,execution_payload='{}'::jsonb"):
		id := fmt.Sprint(args[1].Value)
		row := c.st.requests[id]
		if row == nil {
			return driver.RowsAffected(0), nil
		}
		row.status = "failed"
		row.response = toBytes(args[0].Value)
		row.executionPayload = []byte("{}")
		row.leaseOwner = ""
		row.stale = false
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(q, "UPDATE assistant.chat_requests SET status=$1,response=$2"):
		id := fmt.Sprint(args[2].Value)
		owner := fmt.Sprint(args[3].Value)
		row := c.st.requests[id]
		if row == nil || row.ownerID != owner || row.status != "pending" {
			return driver.RowsAffected(0), nil
		}
		row.status = fmt.Sprint(args[0].Value)
		row.response = toBytes(args[1].Value)
		if row.status != "pending" {
			row.executionPayload = []byte("{}")
			row.leaseOwner = ""
		}
		return driver.RowsAffected(1), nil
	case strings.Contains(q, "started_at IS NULL AND jsonb_typeof"):
		return driver.RowsAffected(0), nil
	default:
		return nil, fmt.Errorf("unexpected exec: %s", q)
	}
}

func (c *fakeExecConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	q := strings.TrimSpace(query)
	switch {
	case strings.HasPrefix(q, "WITH next AS"):
		workerID := fmt.Sprint(args[0].Value)
		for _, row := range c.st.requests {
			if row.status == "pending" && row.mode == "background" && !row.started && len(row.executionPayload) > 2 {
				row.started = true
				row.leaseOwner = workerID
				return &fakeExecRows{
					cols: []string{"id", "owner_id", "execution_payload"},
					vals: [][]driver.Value{{row.id, row.ownerID, append([]byte(nil), row.executionPayload...)}},
				}, nil
			}
		}
		return &fakeExecRows{cols: []string{"id", "owner_id", "execution_payload"}}, nil
	case strings.HasPrefix(q, "SELECT id::text FROM assistant.chat_requests WHERE status='pending' AND mode='background' AND started_at IS NOT NULL"):
		var vals [][]driver.Value
		for _, row := range c.st.requests {
			if row.status == "pending" && row.mode == "background" && row.started && row.stale {
				vals = append(vals, []driver.Value{row.id})
			}
		}
		return &fakeExecRows{cols: []string{"id"}, vals: vals}, nil
	case strings.HasPrefix(q, "SELECT owner_id, COALESCE(conversation_id::text,''), response FROM assistant.chat_requests"):
		id := fmt.Sprint(args[0].Value)
		row := c.st.requests[id]
		if row == nil || row.status != "pending" || !row.stale {
			return &fakeExecRows{cols: []string{"owner_id", "conversation_id", "response"}}, nil
		}
		return &fakeExecRows{
			cols: []string{"owner_id", "conversation_id", "response"},
			vals: [][]driver.Value{{row.ownerID, row.conversationID, append([]byte(nil), row.response...)}},
		}, nil
	case strings.HasPrefix(q, "SELECT messages FROM assistant.conversations"):
		convID := fmt.Sprint(args[0].Value)
		raw := c.st.conversations[convID]
		return &fakeExecRows{cols: []string{"messages"}, vals: [][]driver.Value{{append([]byte(nil), raw...)}}}, nil
	case strings.HasPrefix(q, "SELECT status, COALESCE(lease_owner, '') FROM assistant.chat_requests"):
		id := fmt.Sprint(args[0].Value)
		owner := fmt.Sprint(args[1].Value)
		row := c.st.requests[id]
		if row == nil || row.ownerID != owner {
			return &fakeExecRows{cols: []string{"status", "lease_owner"}}, nil
		}
		return &fakeExecRows{cols: []string{"status", "lease_owner"}, vals: [][]driver.Value{{row.status, row.leaseOwner}}}, nil
	default:
		return nil, fmt.Errorf("unexpected query: %s", q)
	}
}

func TestBackgroundChatEnqueueClaimAndStaleRecovery(t *testing.T) {
	convID := uuid.NewString()
	reqID := uuid.NewString()
	owner := catalog.User{ID: uuid.NewString(), Username: "alice", FromPAT: true, CatalogReviewExempt: true, Permissions: []string{catalog.PermissionEntityEdit}}
	st := &fakeExecState{
		conversations: map[string][]byte{convID: []byte("[]")},
		requests: map[string]*fakeChatReqRow{
			reqID: {id: reqID, ownerID: owner.ID, conversationID: convID, status: "pending", mode: "background"},
		},
	}
	db := openFakeExecDB(t, st)
	store := NewStore(db, nil)
	store.SetLocalExecution(false)
	if store.LocalExecution() {
		t.Fatal("expected LocalExecution to be false when disabled")
	}

	conv := Conversation{ID: convID, OwnerID: owner.ID, Title: "Test"}
	req := ChatRequest{ConversationID: convID, ClientRequestID: reqID, Message: "hello", Background: true}
	msgs := chatMessages(nil, "hello")
	initial := ChatResponse{ConversationID: convID, RequestID: reqID, Status: "pending", CompletionRun: CompletionRun{Messages: msgs}}
	if err := store.enqueueBackgroundChat(context.Background(), owner, req, conv, initial, msgs); err != nil {
		t.Fatalf("enqueueBackgroundChat failed: %v", err)
	}
	claimed, ok, err := store.claimNextBackgroundChat(context.Background(), "worker-1")
	if err != nil || !ok || claimed.id != reqID {
		t.Fatalf("claimNextBackgroundChat unexpected result: ok=%v err=%v claimed=%+v", ok, err, claimed)
	}
	var payload chatExecutionPayload
	if err := json.Unmarshal(claimed.payload, &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Actor.FromPAT || !payload.Actor.CatalogReviewExempt {
		t.Fatalf("claimed payload dropped actor PAT flags: %+v", payload.Actor)
	}

	// Simulate worker crash mid-tool-call: conversation persisted planned tool call without tool output.
	danglingMsgs, _ := json.Marshal([]Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Type: "function"}}},
	})
	st.mu.Lock()
	st.conversations[convID] = danglingMsgs
	st.requests[reqID].stale = true
	st.mu.Unlock()

	store.recoverStaleBackgroundChats(context.Background())

	st.mu.Lock()
	row := st.requests[reqID]
	repairedRaw := st.conversations[convID]
	st.mu.Unlock()
	if row.status != "failed" || string(row.executionPayload) != "{}" || row.leaseOwner != "" {
		t.Fatalf("stale request not failed and scrubbed: %+v", row)
	}
	var finalResp ChatResponse
	if err := json.Unmarshal(row.response, &finalResp); err != nil || finalResp.Error != "assistant_request_interrupted" {
		t.Fatalf("unexpected final response after stale recovery: %+v err=%v", finalResp, err)
	}
	var repaired []Message
	if err := json.Unmarshal(repairedRaw, &repaired); err != nil || len(repaired) != 3 || repaired[2].Role != "tool" || !strings.Contains(repaired[2].Content, "assistant_interrupted_tool_result_unknown") {
		t.Fatalf("dangling tool call was not repaired with unknown marker: %+v err=%v", repaired, err)
	}

	// Verify remote MissionEngine Wake / Interrupt emit pg_notify without spawning local goroutines.
	cat := &catalog.Store{DB: db}
	eng := store.EnsureMissionEngine(cat)
	missionID := uuid.NewString()
	eng.Wake(missionID)
	eng.Interrupt(missionID)
	st.mu.Lock()
	notes := append([]string(nil), st.notifications...)
	st.mu.Unlock()
	wantWake, wantStop := "mission:"+missionID, "mission_stop:"+missionID
	if len(notes) < 3 || notes[len(notes)-2] != wantWake || notes[len(notes)-1] != wantStop {
		t.Fatalf("expected remote mission notifications %q and %q, got %v", wantWake, wantStop, notes)
	}
}

func TestBackgroundChatLeaseFenceAndCancelWake(t *testing.T) {
	convID := uuid.NewString()
	reqID := uuid.NewString()
	ownerID := uuid.NewString()
	origConv := "[{\"role\":\"user\",\"content\":\"keep me\"}]"
	st := &fakeExecState{
		conversations: map[string][]byte{convID: []byte(origConv)},
		requests: map[string]*fakeChatReqRow{
			reqID: {id: reqID, ownerID: ownerID, conversationID: convID, status: "pending", mode: "background", leaseOwner: "worker-current"},
		},
	}
	db := openFakeExecDB(t, st)
	store := NewStore(db, nil)

	// Stale worker whose lease was superseded must not overwrite conversation messages.
	staleCtx := withChatLeaseOwner(context.Background(), "worker-stale")
	err := store.finishChat(staleCtx, ownerID, reqID, Conversation{ID: convID, OwnerID: ownerID}, ChatResponse{
		ConversationID: convID,
		RequestID:      reqID,
		CompletionRun:  CompletionRun{Answer: "stale overwrite", Messages: []Message{{Role: "assistant", Content: "stale overwrite"}}},
	})
	if !errors.Is(err, errChatLeaseLost) {
		t.Fatalf("expected errChatLeaseLost for stale worker, got %v", err)
	}
	if got := string(st.conversations[convID]); got != origConv {
		t.Fatalf("stale worker overwrote conversation: got %s", got)
	}

	// Cancelled request must reject persistence even for matching worker.
	st.mu.Lock()
	st.requests[reqID].status = "failed"
	st.requests[reqID].leaseOwner = ""
	st.mu.Unlock()
	err = store.finishChat(withChatLeaseOwner(context.Background(), "worker-current"), ownerID, reqID, Conversation{ID: convID, OwnerID: ownerID}, ChatResponse{
		ConversationID: convID,
		RequestID:      reqID,
		CompletionRun:  CompletionRun{Answer: "after cancel", Messages: []Message{{Role: "assistant", Content: "after cancel"}}},
	})
	if !errors.Is(err, errChatLeaseLost) || string(st.conversations[convID]) != origConv {
		t.Fatalf("cancelled request should reject finishChat without mutating conversation: err=%v conv=%s", err, string(st.conversations[convID]))
	}

	// NotifyWake(chat_cancel:<id>) and NotifyWake(chat_cancel_conv:<conv>) cancel registered contexts immediately.
	runCtx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	store.registerActiveChat(reqID, convID, cancel1)
	store.NotifyWake(context.Background(), "chat_cancel:"+reqID)
	if runCtx1.Err() == nil {
		t.Fatal("expected chat_cancel notification to cancel active request context")
	}
	store.unregisterActiveChat(reqID)

	runCtx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	reqID2 := uuid.NewString()
	store.registerActiveChat(reqID2, convID, cancel2)
	store.NotifyWake(context.Background(), "chat_cancel_conv:"+convID)
	if runCtx2.Err() == nil {
		t.Fatal("expected chat_cancel_conv notification to cancel conversation context")
	}
	store.unregisterActiveChat(reqID2)
}

func TestCheckActorCredentialsFailsClosedOnAuthFailure(t *testing.T) {
	var mode int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch mode {
		case 0:
			w.WriteHeader(http.StatusInternalServerError)
		case 1:
			_ = json.NewEncoder(w).Encode(catalog.CredentialsStatus{UserID: "u1", TokenVersion: 5, Banned: false})
		case 2:
			_ = json.NewEncoder(w).Encode(catalog.CredentialsStatus{UserID: "u2", TokenVersion: 1, Banned: true})
		}
	}))
	defer srv.Close()

	cat := &catalog.Store{Creds: catalog.NewCredentialsChecker(srv.URL, "svc-token")}
	mode = 0
	if err := checkActorCredentials(context.Background(), cat, MissionActor{ID: "u0", TokenVersion: 1}); !errors.Is(err, errAuthUnavailable) {
		t.Fatalf("expected errAuthUnavailable on upstream 500, got %v", err)
	}
	mode = 1
	if err := checkActorCredentials(context.Background(), cat, MissionActor{ID: "u1", TokenVersion: 4}); !errors.Is(err, errActorReauthRequired) {
		t.Fatalf("expected errActorReauthRequired on stale token_version, got %v", err)
	}
	if err := checkActorCredentials(context.Background(), cat, MissionActor{ID: "u1", TokenVersion: 5}); err != nil {
		t.Fatalf("expected matching token_version to succeed, got %v", err)
	}
	mode = 2
	if err := checkActorCredentials(context.Background(), cat, MissionActor{ID: "u2", FromPAT: true}); !errors.Is(err, errActorReauthRequired) {
		t.Fatalf("expected errActorReauthRequired on banned PAT actor, got %v", err)
	}
}

func TestListenAgentWakeCancelsHungHandshakeImmediately(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			select {
			case accepted <- struct{}{}:
			default:
			}
			defer c.Close()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	dsn := "postgres://user:pass@" + ln.Addr().String() + "/db?sslmode=disable"
	exited := ListenAgentWake(ctx, dsn, func(string) {})
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("timed out waiting for listener TCP dial")
	}
	cancel()
	select {
	case <-exited:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ListenAgentWake remained blocked on hung handshake after ctx cancel")
	}
}
