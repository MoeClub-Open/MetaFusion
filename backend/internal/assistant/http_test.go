package assistant

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

func TestAssistantIdentityRejectsAnonymousAndThirdPartyBeforeStorage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name   string
		user   *catalog.User
		manage bool
		status int
	}{{"anonymous", nil, false, 401}, {"third_party_private_read", &catalog.User{ID: "u", IsThirdParty: true, Permissions: []string{"*"}}, false, 403}, {"ordinary_admin", &catalog.User{ID: "u"}, true, 403}, {"manager", &catalog.User{ID: "u", Permissions: []string{PermissionManage}}, true, 200}} {
		t.Run(test.name, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) {
				if test.user != nil {
					c.Set("catalog_user", test.user)
				}
			})
			r.GET("/", assistantIdentity(test.manage), func(c *gin.Context) { c.Status(200) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
			if w.Code != test.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAssistantBodyRejectsIdentityInjectionAndTrailingPayload(t *testing.T) {
	for _, body := range []string{`{"message":"hello","user_id":"someone-else"}`, `{"message":"hello"} {}`} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		var req ChatRequest
		if decodeBody(c, &req) || w.Code != 400 {
			t.Fatalf("body=%s status=%d", body, w.Code)
		}
	}
}

func TestPostgresChatRequestIsOwnerBoundAndNeverReplayed(t *testing.T) {
	s, model, owner := accountingFixture(t)
	ctx := context.Background()
	req := ChatRequest{ClientRequestID: uuid.NewString(), Message: "A requested edit", ModelID: model.ID, credentialSpecified: true}
	conv, replay, err := s.claimChat(ctx, owner, req)
	if err != nil || replay != nil {
		t.Fatalf("claim err=%v replay=%+v", err, replay)
	}
	if _, _, err = s.claimChat(ctx, owner, req); !errors.Is(err, ErrConflict) {
		t.Fatalf("pending replay: %v", err)
	}
	if _, _, err = s.claimChat(ctx, uuid.NewString(), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("other owner: %v", err)
	}
	second := ChatRequest{ClientRequestID: uuid.NewString(), ConversationID: conv.ID, Message: "Concurrent message", ModelID: model.ID}
	if _, _, err = s.claimChat(ctx, owner, second); !errors.Is(err, ErrConflict) {
		t.Fatalf("concurrent turn: %v", err)
	}
	response := ChatResponse{ConversationID: conv.ID, CompletionRun: CompletionRun{Answer: "Done", Messages: []Message{{Role: "user", Content: req.Message}, {Role: "assistant", Content: "Done"}}, CreditsSpent: 5}}
	if err = s.finishChat(ctx, owner, req.ClientRequestID, conv, response); err != nil {
		t.Fatal(err)
	}
	_, replay, err = s.claimChat(ctx, owner, req)
	if err != nil || replay == nil || replay.Answer != "Done" || replay.CreditsSpent != 5 {
		t.Fatalf("completed replay=%+v err=%v", replay, err)
	}
	changed := req
	changed.Message = "Different edit"
	if _, _, err = s.claimChat(ctx, owner, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed payload: %v", err)
	}
	// Deleting private conversation text retains the idempotency seal.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("catalog_user", &catalog.User{ID: owner})
	c.Params = gin.Params{{Key: "id", Value: conv.ID}}
	c.Request = httptest.NewRequest(http.MethodDelete, "/", nil)
	HTTP{Store: s}.deleteConversation(false)(c)
	// A direct handler call bypasses Gin Engine's final header flush. Production
	// routes perform it automatically even for an empty 204 response.
	c.Writer.WriteHeaderNow()
	if w.Code != 204 {
		t.Fatalf("delete status=%d body=%s", w.Code, w.Body.String())
	}
	_, replay, err = s.claimChat(ctx, owner, req)
	if err != nil || replay == nil || replay.Error != "assistant_conversation_deleted" {
		t.Fatalf("deleted replay=%+v err=%v", replay, err)
	}
}

func TestPostgresOnlyExpiredRequestsCanBeCancelled(t *testing.T) {
	s, model, owner := accountingFixture(t)
	ctx := context.Background()
	req := ChatRequest{ClientRequestID: uuid.NewString(), Message: "request", ModelID: model.ID}
	_, _, err := s.claimChat(ctx, owner, req)
	if err != nil {
		t.Fatal(err)
	}
	cancel := func() int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("catalog_user", &catalog.User{ID: "manager"})
		c.Params = gin.Params{{Key: "id", Value: req.ClientRequestID}}
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"note":"worker interrupted"}`))
		HTTP{Store: s}.cancelRequest(c)
		return w.Code
	}
	if code := cancel(); code != 409 {
		t.Fatalf("live cancel=%d", code)
	}
	if _, err = s.DB.Exec(`UPDATE assistant.chat_requests SET created_at=now()-interval '11 minutes' WHERE id=$1`, req.ClientRequestID); err != nil {
		t.Fatal(err)
	}
	if code := cancel(); code != 200 {
		t.Fatalf("expired cancel=%d", code)
	}
	var status, note string
	if err = s.DB.QueryRow(`SELECT status,response->>'admin_cancel_note' FROM assistant.chat_requests WHERE id=$1`, req.ClientRequestID).Scan(&status, &note); err != nil || status != "failed" || note != "worker interrupted" {
		t.Fatalf("status=%s note=%s err=%v", status, note, err)
	}
}
