package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

type providerRoundTripper func(*http.Request) (*http.Response, error)

func (f providerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPostgresProviderRequestSingleInvocationAndBYOModelSnapshot(t *testing.T) {
	s, m, ownerID := accountingFixture(t)
	ctx := context.Background()
	byoID := uuid.NewString()
	secret, err := s.encryptSecret("custom-byo-secret", "byo:"+byoID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`INSERT INTO assistant.byo_credentials(id,owner_id,name,base_url,upstream_model,encrypted_key) VALUES($1,$2,'Test','https://93.184.216.34/v1','custom-model',$3)`, byoID, ownerID, secret); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE assistant.models SET max_tokens_parameter='max_completion_tokens' WHERE id=$1`, m.ID); err != nil {
		t.Fatal(err)
	}
	request := ReserveRequest{ID: uuid.NewString(), UserID: ownerID, ModelID: m.ID, BYOCredentialID: byoID, InputTokens: 10, OutputTokens: 10, Purpose: "test"}
	r, err := s.Reserve(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if r.Model.UpstreamModel != "custom-model" {
		t.Fatal("BYO model override absent from reserved snapshot")
	}
	// Editing credentials after reservation cannot silently change the reserved model.
	if _, err = s.DB.Exec(`UPDATE assistant.byo_credentials SET upstream_model='changed-model' WHERE id=$1`, byoID); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s.CompletionClient = &http.Client{Transport: providerRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if req.URL.String() != "https://93.184.216.34/v1/chat/completions" {
			t.Errorf("bad URL: %v", req.URL)
		}
		if req.Header.Get("Authorization") != "Bearer custom-byo-secret" {
			t.Error("BYO key not used")
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "custom-model" || body["max_completion_tokens"] != float64(10) || body["max_tokens"] != nil {
			t.Errorf("model snapshot/parameter drift: %v", body)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"upstream-call","choices":[{"message":{"role":"assistant","content":"Test"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`)), Header: make(http.Header)}, nil
	})}
	in := CompletionRequest{ReservationID: request.ID, Messages: []Message{{Role: "user", Content: "test"}}}
	out, err := s.Complete(ctx, in)
	if err != nil || out.Usage.InputTokens != 4 {
		t.Fatalf("completion %+v %v", out, err)
	}
	if _, err = s.Complete(ctx, in); !errors.Is(err, ErrConflict) {
		t.Fatalf("provider request replayed: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider billed %d times", calls.Load())
	}
	u, err := s.Settle(ctx, in.ReservationID, out.Usage, "completed", "")
	if err != nil || u.Credits != 0 || u.Usage.OutputTokens != 2 {
		t.Fatalf("BYO usage %+v %v", u, err)
	}
}

func TestPostgresProviderMissingUsageDoesNotBecomeFreeRetry(t *testing.T) {
	s, m, ownerID := accountingFixture(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(`UPDATE assistant.providers SET base_url='https://93.184.216.34/v1' WHERE id=$1`, m.ProviderID); err != nil {
		t.Fatal(err)
	}
	request := ReserveRequest{ID: uuid.NewString(), UserID: ownerID, ModelID: m.ID, InputTokens: 10, OutputTokens: 10, Purpose: "test"}
	if _, err := s.Reserve(ctx, request); err != nil {
		t.Fatal(err)
	}
	s.CompletionClient = &http.Client{Transport: providerRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"No usage"}}]}`)), Header: make(http.Header)}, nil
	})}
	_, err := s.Complete(ctx, CompletionRequest{ReservationID: request.ID, Messages: []Message{{Role: "user", Content: "test"}}})
	if err == nil || err.Error() != "assistant_provider_usage_missing" {
		t.Fatal(err)
	}
	u, settleErr := s.Settle(ctx, request.ID, TokenUsage{}, "failed", err.Error())
	if settleErr != nil || u.Status != "unknown" || !u.UsageEstimated || u.Credits != 20 {
		t.Fatalf("missing usage silently free: %+v %v", u, settleErr)
	}
}

func TestPostgresProviderExplicitRateLimitReleasesReservation(t *testing.T) {
	s, m, ownerID := accountingFixture(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(`UPDATE assistant.providers SET base_url='https://93.184.216.34/v1' WHERE id=$1`, m.ProviderID); err != nil {
		t.Fatal(err)
	}
	in := ReserveRequest{ID: uuid.NewString(), UserID: ownerID, ModelID: m.ID, InputTokens: 10, OutputTokens: 10, Purpose: "test"}
	if _, err := s.Reserve(ctx, in); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s.CompletionClient = &http.Client{Transport: providerRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)), Header: make(http.Header)}, nil
	})}
	_, err := s.Complete(ctx, CompletionRequest{ReservationID: in.ID, Messages: []Message{{Role: "user", Content: "test"}}})
	if err == nil || err.Error() != "assistant_provider_http_429" {
		t.Fatal(err)
	}
	u, settleErr := s.Settle(ctx, in.ID, TokenUsage{}, "failed", err.Error())
	if settleErr != nil || u.Status != "failed" || u.UsageEstimated || u.Credits != 0 {
		t.Fatalf("rejected request charged: %+v %v", u, settleErr)
	}
	b, err := s.SyncCredits(ctx, ownerID)
	if err != nil || b.AvailableCredits != 100 {
		t.Fatalf("rejection reservation retained: %+v %v", b, err)
	}
	if calls.Load() != 1 {
		t.Fatal("rate limited call retried")
	}
}
