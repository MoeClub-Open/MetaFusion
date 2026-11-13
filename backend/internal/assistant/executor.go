package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

const (
	AgentWakeChannel           = "metafusion_agent_wake"
	backgroundChatConcurrency  = 8
	backgroundChatLeaseTTL     = 45 * time.Second
	backgroundChatRenewEvery   = 12 * time.Second
	backgroundChatPollInterval = 2 * time.Second
)

var (
	errActorReauthRequired = errors.New("assistant_actor_reauth_required")
	errAuthUnavailable     = errors.New("auth_unavailable")
	errChatLeaseLost       = errors.New("assistant_lease_lost")
)

type chatLeaseOwnerKey struct{}

func withChatLeaseOwner(ctx context.Context, leaseOwner string) context.Context {
	if leaseOwner == "" {
		return ctx
	}
	return context.WithValue(ctx, chatLeaseOwnerKey{}, leaseOwner)
}

func chatLeaseOwnerFromContext(ctx context.Context) string {
	v, _ := ctx.Value(chatLeaseOwnerKey{}).(string)
	return v
}

type activeChatRun struct {
	conversationID string
	cancel         context.CancelFunc
}

type wakeListenerState struct {
	mu      sync.Mutex
	nextID  uint64
	subs    map[uint64]func(string)
	running bool
	cancel  context.CancelFunc
}

func (s *Store) registerActiveChat(requestID, conversationID string, cancel context.CancelFunc) {
	if s == nil || requestID == "" || cancel == nil {
		return
	}
	s.activeChatsMu.Lock()
	if s.activeChats == nil {
		s.activeChats = map[string]activeChatRun{}
	}
	s.activeChats[requestID] = activeChatRun{conversationID: conversationID, cancel: cancel}
	s.activeChatsMu.Unlock()
}

func (s *Store) unregisterActiveChat(requestID string) {
	if s == nil || requestID == "" {
		return
	}
	s.activeChatsMu.Lock()
	delete(s.activeChats, requestID)
	s.activeChatsMu.Unlock()
}

func (s *Store) cancelActiveChat(requestID string) {
	if s == nil || requestID == "" {
		return
	}
	s.activeChatsMu.Lock()
	run, ok := s.activeChats[requestID]
	s.activeChatsMu.Unlock()
	if ok && run.cancel != nil {
		run.cancel()
	}
}

func (s *Store) cancelActiveConversation(conversationID string) {
	if s == nil || conversationID == "" {
		return
	}
	s.activeChatsMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.activeChats))
	for _, run := range s.activeChats {
		if run.conversationID == conversationID && run.cancel != nil {
			cancels = append(cancels, run.cancel)
		}
	}
	s.activeChatsMu.Unlock()
	for _, fn := range cancels {
		fn()
	}
}

func (s *Store) dispatchWake(extra string) {
	if s == nil {
		return
	}
	switch {
	case extra == "" || strings.HasPrefix(extra, "chat:"):
		s.signalChatWake()
	case strings.HasPrefix(extra, "chat_cancel:"):
		s.cancelActiveChat(strings.TrimPrefix(extra, "chat_cancel:"))
	case strings.HasPrefix(extra, "chat_cancel_conv:"):
		s.cancelActiveConversation(strings.TrimPrefix(extra, "chat_cancel_conv:"))
	}
	s.wakeSubs.mu.Lock()
	fns := make([]func(string), 0, len(s.wakeSubs.subs))
	for _, fn := range s.wakeSubs.subs {
		fns = append(fns, fn)
	}
	s.wakeSubs.mu.Unlock()
	for _, fn := range fns {
		fn(extra)
	}
}

// SubscribeWake multiplexes a single PostgreSQL LISTEN connection across all
// worker loops sharing this Store and unsubscribes when ctx is cancelled.
func (s *Store) SubscribeWake(ctx context.Context, onNotify func(string)) {
	if s == nil || onNotify == nil || ctx.Err() != nil {
		return
	}
	s.wakeSubs.mu.Lock()
	if s.wakeSubs.subs == nil {
		s.wakeSubs.subs = map[uint64]func(string){}
	}
	s.wakeSubs.nextID++
	id := s.wakeSubs.nextID
	s.wakeSubs.subs[id] = onNotify
	if !s.wakeSubs.running && strings.TrimSpace(s.DSN) != "" {
		lctx, lcancel := context.WithCancel(context.Background())
		s.wakeSubs.running = true
		s.wakeSubs.cancel = lcancel
		ListenAgentWake(lctx, s.DSN, s.dispatchWake)
	}
	s.wakeSubs.mu.Unlock()
	go func() {
		<-ctx.Done()
		var stopListener context.CancelFunc
		s.wakeSubs.mu.Lock()
		delete(s.wakeSubs.subs, id)
		if len(s.wakeSubs.subs) == 0 && s.wakeSubs.running {
			stopListener = s.wakeSubs.cancel
			s.wakeSubs.running = false
			s.wakeSubs.cancel = nil
		}
		s.wakeSubs.mu.Unlock()
		if stopListener != nil {
			stopListener()
		}
	}()
}

type chatExecutionPayload struct {
	Actor        MissionActor `json:"actor"`
	Request      ChatRequest  `json:"request"`
	Conversation Conversation `json:"conversation"`
	Messages     []Message    `json:"messages"`
}

func (s *Store) NotifyWake(ctx context.Context, payload string) {
	if s == nil {
		return
	}
	s.dispatchWake(payload)
	if s.DB == nil {
		return
	}
	notifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_, _ = s.DB.ExecContext(notifyCtx, `SELECT pg_notify($1, $2)`, AgentWakeChannel, payload)
}

type wakeTrackedConn struct {
	net.Conn
	dialer      *wakeDialer
	deadlineMu  sync.Mutex
	handshaking bool
}

func (c *wakeTrackedConn) Read(b []byte) (int, error) {
	c.deadlineMu.Lock()
	if c.handshaking {
		_ = c.Conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	}
	c.deadlineMu.Unlock()
	return c.Conn.Read(b)
}

func (c *wakeTrackedConn) Write(b []byte) (int, error) {
	c.deadlineMu.Lock()
	if c.handshaking {
		_ = c.Conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	}
	c.deadlineMu.Unlock()
	return c.Conn.Write(b)
}

func (c *wakeTrackedConn) finishHandshake() {
	c.deadlineMu.Lock()
	if c.handshaking {
		c.handshaking = false
		_ = c.Conn.SetDeadline(time.Time{})
	}
	c.deadlineMu.Unlock()
}

func (c *wakeTrackedConn) Close() error {
	c.dialer.mu.Lock()
	delete(c.dialer.conns, c)
	c.dialer.mu.Unlock()
	return c.Conn.Close()
}

type wakeDialer struct {
	ctx   context.Context
	mu    sync.Mutex
	conns map[*wakeTrackedConn]struct{}
}

func (d *wakeDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialTimeout(network, address, 5*time.Second)
}

func (d *wakeDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	if timeout <= 0 || timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	dialCtx, cancel := context.WithTimeout(d.ctx, timeout)
	defer cancel()
	var nd net.Dialer
	raw, err := nd.DialContext(dialCtx, network, address)
	if err != nil {
		return nil, err
	}
	wrapped := &wakeTrackedConn{Conn: raw, dialer: d, handshaking: true}
	d.mu.Lock()
	if d.ctx.Err() != nil {
		d.mu.Unlock()
		_ = raw.Close()
		return nil, d.ctx.Err()
	}
	d.conns[wrapped] = struct{}{}
	d.mu.Unlock()
	return wrapped, nil
}

func (d *wakeDialer) markConnected() {
	d.mu.Lock()
	conns := make([]*wakeTrackedConn, 0, len(d.conns))
	for c := range d.conns {
		conns = append(conns, c)
	}
	d.mu.Unlock()
	for _, c := range conns {
		c.finishHandshake()
	}
}

func (d *wakeDialer) abortAll() {
	d.mu.Lock()
	conns := make([]*wakeTrackedConn, 0, len(d.conns))
	for c := range d.conns {
		conns = append(conns, c)
	}
	d.conns = map[*wakeTrackedConn]struct{}{}
	d.mu.Unlock()
	for _, c := range conns {
		_ = c.Conn.Close()
	}
}

func ListenAgentWake(ctx context.Context, dsn string, onNotify func(string)) <-chan struct{} {
	exited := make(chan struct{})
	if strings.TrimSpace(dsn) == "" || onNotify == nil || ctx.Err() != nil {
		close(exited)
		return exited
	}
	go func() {
		defer close(exited)
		dialer := &wakeDialer{ctx: ctx, conns: map[*wakeTrackedConn]struct{}{}}
		listener := pq.NewDialListener(dialer, dsn, time.Second, 30*time.Second, func(ev pq.ListenerEventType, _ error) {
			if ev == pq.ListenerEventConnected || ev == pq.ListenerEventReconnected {
				dialer.markConnected()
			}
			if ev == pq.ListenerEventReconnected {
				onNotify("")
			}
		})
		var closeOnce sync.Once
		closeListener := func() {
			closeOnce.Do(func() {
				dialer.abortAll()
				_ = listener.Close()
			})
		}
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-ctx.Done():
				closeListener()
			case <-done:
			}
		}()
		if err := listener.Listen(AgentWakeChannel); err != nil {
			closeListener()
			return
		}
		dialer.markConnected()
		defer closeListener()
		ping := time.NewTicker(time.Minute)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ping.C:
				_ = listener.Ping()
			case n, ok := <-listener.Notify:
				if !ok {
					return
				}
				if n == nil {
					onNotify("")
				} else {
					onNotify(n.Extra)
				}
			}
		}
	}()
	return exited
}

func checkActorCredentials(ctx context.Context, cat *catalog.Store, actor MissionActor) error {
	if cat == nil || cat.Creds == nil || !cat.Creds.Enabled() || actor.ID == "" {
		return nil
	}
	st, err := cat.Creds.Lookup(ctx, actor.ID)
	if err != nil {
		return errAuthUnavailable
	}
	if st.Banned || (!actor.FromPAT && actor.TokenVersion > 0 && !st.Match(actor.TokenVersion)) {
		return errActorReauthRequired
	}
	return nil
}

func isCatalogReadOnlyTool(name string) bool {
	switch name {
	case "catalog_search", "catalog_get", "catalog_relationships", "catalog_candidates", "catalog_definitions",
		"catalog_workspace_list", "catalog_workspace_get", "catalog_workspace_commits",
		"catalog_pull_request_list", "catalog_pull_request_get", "catalog_commit_get", "catalog_version_read",
		"external_sources", "external_source_query", "public_fetch", "public_wiki", "web_search", "web_fetch":
		return true
	default:
		return false
	}
}

func (s *Store) enqueueBackgroundChat(ctx context.Context, u catalog.User, req ChatRequest, conv Conversation, initial ChatResponse, messages []Message) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stored := make([]Message, 0, len(initial.Messages))
	for _, m := range initial.Messages {
		if m.Role != "system" {
			stored = append(stored, m)
		}
	}
	convBytes, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE assistant.conversations SET messages=$1,model_id=NULLIF($2,'')::uuid,reasoning_effort=$3,updated_at=now() WHERE id=$4 AND owner_id=$5`, convBytes, conv.ModelID, conv.ReasoningEffort, conv.ID, u.ID); err != nil {
		return err
	}
	initial.RequestID = req.ClientRequestID
	initial.Status = "pending"
	respBytes, err := json.Marshal(initial)
	if err != nil {
		return err
	}
	payloadBytes, err := json.Marshal(chatExecutionPayload{
		Actor:        ActorFromCatalogUser(u),
		Request:      req,
		Conversation: conv,
		Messages:     messages,
	})
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE assistant.chat_requests SET mode='background',response=$1,execution_payload=$2,updated_at=now() WHERE id=$3 AND owner_id=$4 AND status='pending'`, respBytes, payloadBytes, req.ClientRequestID, u.ID)
	if err != nil {
		return err
	}
	if n, e := res.RowsAffected(); e != nil {
		return e
	} else if n == 0 {
		return ErrNotFound
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.NotifyWake(ctx, "chat:"+req.ClientRequestID)
	return nil
}

func (s *Store) RunBackgroundChats(ctx context.Context, cat *catalog.Store) {
	if s == nil || s.DB == nil || cat == nil {
		return
	}
	workerID := uuid.NewString()
	sem := make(chan struct{}, backgroundChatConcurrency)
	wake := make(chan struct{}, 1)
	trigger := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	s.SubscribeWake(ctx, func(extra string) {
		if extra == "" || strings.HasPrefix(extra, "chat:") {
			trigger()
		}
	})
	s.recoverStaleBackgroundChats(ctx)
	s.dispatchBackgroundChats(ctx, cat, workerID, sem)
	ticker := time.NewTicker(backgroundChatPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-s.chatWake:
		case <-ticker.C:
		}
		s.recoverStaleBackgroundChats(ctx)
		s.dispatchBackgroundChats(ctx, cat, workerID, sem)
	}
}

type claimedBackgroundChat struct {
	id      string
	ownerID string
	payload []byte
}

func (s *Store) claimNextBackgroundChat(ctx context.Context, workerID string) (claimedBackgroundChat, bool, error) {
	var out claimedBackgroundChat
	err := s.DB.QueryRowContext(ctx, `WITH next AS (
		SELECT id FROM assistant.chat_requests
		WHERE status='pending' AND mode='background' AND started_at IS NULL AND jsonb_typeof(execution_payload->'request')='object'
		ORDER BY created_at ASC, id ASC
		FOR UPDATE SKIP LOCKED LIMIT 1
	)
	UPDATE assistant.chat_requests r
	SET started_at=now(), lease_owner=$1, lease_until=now()+interval '45 seconds', updated_at=now()
	FROM next WHERE r.id=next.id
	RETURNING r.id::text, r.owner_id, r.execution_payload`, workerID).Scan(&out.id, &out.ownerID, &out.payload)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	return out, true, nil
}

func (s *Store) dispatchBackgroundChats(ctx context.Context, cat *catalog.Store, workerID string, sem chan struct{}) {
	for ctx.Err() == nil {
		select {
		case sem <- struct{}{}:
		default:
			return
		}
		claimed, ok, err := s.claimNextBackgroundChat(ctx, workerID)
		if err != nil || !ok {
			<-sem
			return
		}
		s.bg.Add(1)
		go func(job claimedBackgroundChat) {
			defer func() {
				<-sem
				s.bg.Done()
			}()
			s.executeBackgroundChat(ctx, cat, workerID, job)
		}(claimed)
	}
}

func (s *Store) executeBackgroundChat(ctx context.Context, cat *catalog.Store, workerID string, job claimedBackgroundChat) {
	var payload chatExecutionPayload
	if err := json.Unmarshal(job.payload, &payload); err != nil {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = s.DB.QueryRowContext(finishCtx, `UPDATE assistant.chat_requests SET status='failed',response=jsonb_build_object('request_id',$1::text,'status','failed','error','assistant_request_failed'),execution_payload='{}'::jsonb,lease_owner='',lease_until=NULL,updated_at=now() WHERE id=$1 AND status='pending' RETURNING id::text`, job.id).Scan(new(string))
		return
	}

	runCtx, cancelRun := context.WithCancel(withChatLeaseOwner(ctx, workerID))
	s.registerActiveChat(job.id, payload.Conversation.ID, cancelRun)
	defer s.unregisterActiveChat(job.id)
	var leaseLost atomic.Bool
	leaseDone := make(chan struct{})
	go func() {
		defer close(leaseDone)
		ticker := time.NewTicker(backgroundChatRenewEvery)
		defer ticker.Stop()
		lastRenewed := time.Now()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				renewCtx, cancel := context.WithTimeout(context.WithoutCancel(runCtx), 5*time.Second)
				res, err := s.DB.ExecContext(renewCtx, `UPDATE assistant.chat_requests SET lease_until=now()+interval '45 seconds' WHERE id=$1 AND status='pending' AND lease_owner=$2`, job.id, workerID)
				cancel()
				if err == nil {
					if n, _ := res.RowsAffected(); n == 0 {
						leaseLost.Store(true)
						cancelRun()
						return
					}
					lastRenewed = time.Now()
				} else if time.Since(lastRenewed) > backgroundChatLeaseTTL-5*time.Second {
					leaseLost.Store(true)
					cancelRun()
					return
				}
			}
		}
	}()

	req := payload.Request
	conv := payload.Conversation
	messages := payload.Messages
	verifyActiveAndActor := func() error {
		if leaseLost.Load() || runCtx.Err() != nil {
			return errChatLeaseLost
		}
		vctx, cancel := context.WithTimeout(context.WithoutCancel(runCtx), 5*time.Second)
		var curStatus, curLeaseOwner string
		err := s.DB.QueryRowContext(vctx, `SELECT status, COALESCE(lease_owner, '') FROM assistant.chat_requests WHERE id=$1 AND owner_id=$2`, job.id, job.ownerID).Scan(&curStatus, &curLeaseOwner)
		cancel()
		if errors.Is(err, sql.ErrNoRows) || (err == nil && (curStatus != "pending" || curLeaseOwner != workerID)) {
			leaseLost.Store(true)
			cancelRun()
			return errChatLeaseLost
		}
		if err != nil {
			return err
		}
		return checkActorCredentials(runCtx, cat, payload.Actor)
	}
	req.beforeCompletion = verifyActiveAndActor
	req.beforeTool = func(call ToolCall) (ToolCall, error) {
		if !isCatalogReadOnlyTool(call.Function.Name) {
			if err := verifyActiveAndActor(); err != nil {
				return call, err
			}
		}
		return call, nil
	}
	req.checkpoint = func(progress CompletionRun) error {
		if leaseLost.Load() {
			return errChatLeaseLost
		}
		checkpointCtx, cancel := context.WithTimeout(withChatLeaseOwner(context.WithoutCancel(runCtx), workerID), 10*time.Second)
		defer cancel()
		err := s.persistChat(checkpointCtx, job.ownerID, job.id, conv, ChatResponse{
			ConversationID: conv.ID,
			RequestID:      job.id,
			Status:         "pending",
			CompletionRun:  progress,
		}, "pending")
		if errors.Is(err, errChatLeaseLost) {
			leaseLost.Store(true)
			cancelRun()
		}
		return err
	}

	var run CompletionRun
	runErr := checkActorCredentials(runCtx, cat, payload.Actor)
	if runErr == nil {
		execCtx, cancelExec := context.WithTimeout(runCtx, 5*time.Minute)
		run, runErr = runCompletion(execCtx, s, cat, payload.Actor.catalogUser(false), req, messages, true, "chat", "")
		cancelExec()
	}
	cancelRun()
	<-leaseDone
	if leaseLost.Load() || errors.Is(runErr, errChatLeaseLost) {
		return
	}
	if run.Messages == nil {
		run.Messages = messages
	}
	response := ChatResponse{ConversationID: conv.ID, RequestID: job.id, CompletionRun: run}
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) && ctx.Err() != nil {
			response.Error = "assistant_request_interrupted"
		} else {
			response.Error = safeRuntimeCode(runErr)
		}
	}
	finishCtx, cancelFinish := context.WithTimeout(withChatLeaseOwner(context.WithoutCancel(ctx), workerID), 10*time.Second)
	defer cancelFinish()
	_ = s.finishChat(finishCtx, job.ownerID, job.id, conv, response)
}

func (s *Store) recoverStaleBackgroundChats(ctx context.Context) {
	if s == nil || s.DB == nil || ctx.Err() != nil {
		return
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE assistant.chat_requests SET status='failed',response=jsonb_build_object('conversation_id',COALESCE(conversation_id::text,''),'request_id',id::text,'status','failed','error','assistant_request_interrupted'),execution_payload='{}'::jsonb,lease_owner='',lease_until=NULL,updated_at=now() WHERE status='pending' AND mode='background' AND started_at IS NULL AND jsonb_typeof(execution_payload->'request') IS DISTINCT FROM 'object' AND created_at < now()-interval '30 seconds'`)

	rows, err := s.DB.QueryContext(ctx, `SELECT id::text FROM assistant.chat_requests WHERE status='pending' AND mode='background' AND started_at IS NOT NULL AND lease_until < now() ORDER BY updated_at ASC LIMIT 16`)
	if err != nil {
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	for _, id := range ids {
		s.recoverOneStaleChat(ctx, id)
	}
}

func (s *Store) recoverOneStaleChat(ctx context.Context, requestID string) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	var ownerID, convID string
	var rawResp []byte
	err = tx.QueryRowContext(ctx, `SELECT owner_id, COALESCE(conversation_id::text,''), response FROM assistant.chat_requests WHERE id=$1 AND status='pending' AND mode='background' AND started_at IS NOT NULL AND lease_until < now() FOR UPDATE SKIP LOCKED`, requestID).Scan(&ownerID, &convID, &rawResp)
	if err != nil {
		return
	}
	if convID != "" {
		var rawMsgs []byte
		if tx.QueryRowContext(ctx, `SELECT messages FROM assistant.conversations WHERE id=$1 AND owner_id=$2 FOR UPDATE`, convID, ownerID).Scan(&rawMsgs) == nil {
			var msgs []Message
			if json.Unmarshal(rawMsgs, &msgs) == nil {
				repaired := withoutSystemMessages(msgs)
				if b, e := json.Marshal(repaired); e == nil {
					_, _ = tx.ExecContext(ctx, `UPDATE assistant.conversations SET messages=$1,updated_at=now() WHERE id=$2 AND owner_id=$3`, b, convID, ownerID)
				}
			}
		}
	}
	var resp ChatResponse
	_ = json.Unmarshal(rawResp, &resp)
	if resp.ConversationID == "" {
		resp.ConversationID = convID
	}
	resp.RequestID = requestID
	resp.Status = "failed"
	resp.Error = "assistant_request_interrupted"
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	if _, err = tx.ExecContext(ctx, `UPDATE assistant.chat_requests SET status='failed',response=$1,execution_payload='{}'::jsonb,lease_owner='',lease_until=NULL,updated_at=now() WHERE id=$2`, b, requestID); err != nil {
		return
	}
	_ = tx.Commit()
}
