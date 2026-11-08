package assistant

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestCredentialEncryptionAuthenticatesOwnerAndCiphertext(t *testing.T) {
	s := NewStore(nil, bytes.Repeat([]byte{0x41}, 32))
	plain := "test-secret-never-returned"
	ciphertext, err := s.encryptSecret(plain, "byo:alice")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte(plain)) {
		t.Fatal("plaintext leaked")
	}
	if value, err := s.decryptSecret(ciphertext, "byo:alice"); err != nil || value != plain {
		t.Fatalf("roundtrip: %q %v", value, err)
	}
	if _, err := s.decryptSecret(ciphertext, "byo:bob"); err == nil {
		t.Fatal("credential reusable under another owner")
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := s.decryptSecret(ciphertext, "byo:alice"); err == nil {
		t.Fatal("tampering accepted")
	}
	if _, err := NewStore(nil, nil).encryptSecret("x", "id"); !errors.Is(err, ErrEncryptionUnavailable) {
		t.Fatal(err)
	}
	if _, err := ParseEncryptionKey(base64.StdEncoding.EncodeToString(make([]byte, 16))); err == nil {
		t.Fatal("non AES256 key accepted")
	}
}

func TestPriceIntegerPrecisionCachedInputAndOverflow(t *testing.T) {
	m := Model{InputPriceMicroUSDPerMillion: 1000000, CachedInputPriceMicroUSDPerMillion: 100000, OutputPriceMicroUSDPerMillion: 3000000, InputCreditsPerMillion: 2000000, OutputCreditsPerMillion: 4000000}
	credits, cost, err := quote(m, TokenUsage{InputTokens: 100, OutputTokens: 10, CachedInputTokens: 50})
	if err != nil || credits != 240 || cost != 85 {
		t.Fatalf("credits=%d cost=%d err=%v", credits, cost, err)
	}
	if v, err := priced(1, 1); err != nil || v != 1 {
		t.Fatal("fractional micro amounts must round up", v, err)
	}
	if _, err := priced(math.MaxInt64, math.MaxInt64); err == nil {
		t.Fatal("overflow accepted")
	}
	if _, _, err := quote(m, TokenUsage{InputTokens: 1, CachedInputTokens: 2}); err == nil {
		t.Fatal("cached usage exceeds input")
	}
}

func TestCompletionRequiresUsageAndKeepsToolCalls(t *testing.T) {
	body := `{"id":"response-1","choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"tool1","type":"function","function":{"name":"catalog_search","arguments":"{\"q\":\"test\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":14,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":8}}}`
	out, err := decodeCompletion(strings.NewReader(body))
	if err != nil || len(out.Message.ToolCalls) != 1 || out.Usage.CachedInputTokens != 8 {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := decodeCompletion(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"free?"}}]}`)); err == nil || err.Error() != "assistant_provider_usage_missing" {
		t.Fatal("missing usage must be explicit", err)
	}
	if _, err := decodeCompletion(strings.NewReader(strings.Replace(body, `"cached_tokens":8`, `"cached_tokens":80`, 1))); err == nil {
		t.Fatal("invalid cached tokens accepted")
	}
	request, err := completionPayload(Reservation{ReserveRequest: ReserveRequest{OutputTokens: 100}, Model: Model{UpstreamModel: "model-a"}}, CompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal(request, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "model-a" || payload["max_tokens"] != float64(100) || payload["stream"] != false {
		t.Fatalf("unexpected provider payload: %v", payload)
	}
	request, err = completionPayload(Reservation{ReserveRequest: ReserveRequest{OutputTokens: 30}, Model: Model{UpstreamModel: "reasoning-model", MaxTokensParameter: "max_completion_tokens"}}, CompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	payload = map[string]any{}
	if err = json.Unmarshal(request, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["max_completion_tokens"] != float64(30) || payload["max_tokens"] != nil {
		t.Fatalf("reasoning token parameter not respected: %v", payload)
	}
	request, err = completionPayload(Reservation{ReserveRequest: ReserveRequest{OutputTokens: 30}, Model: Model{UpstreamModel: "reasoning-model", ReasoningEffort: "high"}}, CompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	payload = map[string]any{}
	if err = json.Unmarshal(request, &payload); err != nil || payload["reasoning_effort"] != "high" {
		t.Fatalf("openai_compatible reasoning_effort not mapped: %v %v", payload, err)
	}
	respPayload, err := protocolPayload("openai_responses", "b1", Reservation{ReserveRequest: ReserveRequest{OutputTokens: 30}, Model: Model{UpstreamModel: "gpt-6", ReasoningEffort: "medium"}}, CompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	payload = map[string]any{}
	if err = json.Unmarshal(respPayload, &payload); err != nil {
		t.Fatal(err)
	}
	if rm, _ := payload["reasoning"].(map[string]any); rm["effort"] != "medium" {
		t.Fatalf("openai_responses reasoning.effort not mapped: %v", payload)
	}
	anthPayload, err := protocolPayload("anthropic_messages", "b1", Reservation{ReserveRequest: ReserveRequest{OutputTokens: 8192}, Model: Model{UpstreamModel: "claude", ReasoningEffort: "medium"}}, CompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	payload = map[string]any{}
	if err = json.Unmarshal(anthPayload, &payload); err != nil {
		t.Fatal(err)
	}
	if tm, _ := payload["thinking"].(map[string]any); tm["type"] != "enabled" || tm["budget_tokens"] != float64(4096) {
		t.Fatalf("anthropic_messages thinking not mapped: %v", payload)
	}
	gemPayload, err := protocolPayload("gemini_generate_content", "b1", Reservation{ReserveRequest: ReserveRequest{OutputTokens: 2048}, Model: Model{UpstreamModel: "gemini", ReasoningEffort: "low"}}, CompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	payload = map[string]any{}
	if err = json.Unmarshal(gemPayload, &payload); err != nil {
		t.Fatal(err)
	}
	gc, _ := payload["generationConfig"].(map[string]any)
	if tc, _ := gc["thinkingConfig"].(map[string]any); tc["thinkingBudget"] != float64(1024) {
		t.Fatalf("gemini_generate_content thinkingBudget not mapped: %v", payload)
	}
	if !ValidReasoningEffort("max") || anthropicThinkingBudget("max", 40000) != 32000 || geminiThinkingBudget("max", 30000) != 24576 {
		t.Fatal("max reasoning effort budget mapping failed")
	}
}
