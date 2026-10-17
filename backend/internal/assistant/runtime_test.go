package assistant

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

type runtimeFixture struct {
	events      []string
	reserveErr  error
	completeErr error
	settleErr   error
	response    CompletionResponse
	status      string
	errorCode   string
	reservation ReserveRequest
}

func (f *runtimeFixture) GetSettings(context.Context) (Settings, error) {
	return Settings{Enabled: true, DefaultModelID: "model", MaxToolRounds: 2}, nil
}
func (f *runtimeFixture) Models(context.Context, bool) ([]Model, error) {
	return []Model{{ID: "model", MaxOutputTokens: 100, ContextWindow: 1000000}}, nil
}
func (f *runtimeFixture) Reserve(_ context.Context, r ReserveRequest) (Reservation, error) {
	f.events = append(f.events, "reserve")
	f.reservation = r
	return Reservation{ReserveRequest: r, Status: "reserved"}, f.reserveErr
}
func (f *runtimeFixture) Complete(context.Context, CompletionRequest) (CompletionResponse, error) {
	f.events = append(f.events, "complete")
	return f.response, f.completeErr
}
func (f *runtimeFixture) Settle(_ context.Context, _ string, u TokenUsage, status, code string) (UsageRecord, error) {
	f.events = append(f.events, "settle")
	f.status = status
	f.errorCode = code
	return UsageRecord{Usage: u, Credits: 9}, f.settleErr
}
func (f *runtimeFixture) SyncCredits(context.Context, string) (Balance, error) {
	return Balance{Credits: 91, AvailableCredits: 91}, nil
}

func TestCompletionReservesBeforeNetworkAndSettlesActualTokens(t *testing.T) {
	f := &runtimeFixture{response: CompletionResponse{Message: Message{Role: "assistant", Content: "answer"}, Usage: TokenUsage{InputTokens: 123, OutputTokens: 45, CachedInputTokens: 12}}}
	r, err := runCompletion(context.Background(), f, nil, catalog.User{ID: "owner"}, ChatRequest{ClientRequestID: uuid.NewString()}, []Message{{Role: "user", Content: "question"}}, false, "generated", "entity")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.events, []string{"reserve", "complete", "settle"}) {
		t.Fatalf("events=%v", f.events)
	}
	if f.reservation.InputTokens <= 0 || f.reservation.OutputTokens != 100 || f.reservation.UserID != "owner" {
		t.Fatalf("reservation=%+v", f.reservation)
	}
	if r.Answer != "answer" || r.Usage.InputTokens != 123 || r.CreditsSpent != 9 || r.Balance.AvailableCredits != 91 {
		t.Fatalf("run=%+v", r)
	}
}

func TestCompletionInsufficientCreditNeverCallsProvider(t *testing.T) {
	f := &runtimeFixture{reserveErr: ErrInsufficientCredits}
	_, err := runCompletion(context.Background(), f, nil, catalog.User{ID: "owner"}, ChatRequest{}, nil, false, "generated", "")
	if !errors.Is(err, ErrInsufficientCredits) || !reflect.DeepEqual(f.events, []string{"reserve"}) {
		t.Fatalf("err=%v events=%v", err, f.events)
	}
}

func TestCompletionFailedProviderKeepsDefiniteRejectionForAccounting(t *testing.T) {
	f := &runtimeFixture{completeErr: errors.New("assistant_provider_http_429")}
	_, err := runCompletion(context.Background(), f, nil, catalog.User{ID: "owner"}, ChatRequest{}, nil, false, "generated", "")
	if err == nil || f.status != "failed" || f.errorCode != "assistant_provider_http_429" {
		t.Fatalf("err=%v status=%q code=%q", err, f.status, f.errorCode)
	}
	if !reflect.DeepEqual(f.events, []string{"reserve", "complete", "settle"}) {
		t.Fatalf("events=%v", f.events)
	}
}

func TestCompletionDoesNotExecuteToolsUntilAccountingSettles(t *testing.T) {
	call := ToolCall{ID: "call", Type: "function"}
	call.Function.Name = "catalog_definitions"
	call.Function.Arguments = "{}"
	f := &runtimeFixture{settleErr: errors.New("database failed"), response: CompletionResponse{Message: Message{Role: "assistant", ToolCalls: []ToolCall{call}}}}
	r, err := runCompletion(context.Background(), f, nil, catalog.User{ID: "owner"}, ChatRequest{}, nil, true, "chat", "")
	if err == nil || len(r.ToolResults) != 0 {
		t.Fatalf("err=%v results=%v", err, r.ToolResults)
	}
}

func TestThirdPartyCannotStartPrivateAssistantCall(t *testing.T) {
	f := &runtimeFixture{}
	_, err := runCompletion(context.Background(), f, nil, catalog.User{ID: "owner", IsThirdParty: true}, ChatRequest{}, nil, false, "generated", "")
	if err == nil || len(f.events) != 0 {
		t.Fatalf("err=%v events=%v", err, f.events)
	}
}

func TestUnknownModelFailureStillReturnsCurrentBalance(t *testing.T) {
	f := &runtimeFixture{}
	r, err := runCompletion(context.Background(), f, nil, catalog.User{ID: "owner"}, ChatRequest{ModelID: "disabled-model"}, nil, false, "chat", "")
	if !errors.Is(err, ErrNotFound) || r.Balance.AvailableCredits != 91 || len(f.events) != 0 {
		t.Fatalf("run=%+v err=%v events=%v", r, err, f.events)
	}
}

func TestValidateChatRequiresStableRequestID(t *testing.T) {
	if validateChatRequest(ChatRequest{Message: "hello"}) == nil {
		t.Fatal("missing id accepted")
	}
	if err := validateChatRequest(ChatRequest{Message: "hello", ClientRequestID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
}
