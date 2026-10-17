package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/metafusion/metafusion-app/internal/publicweb"
)

// Complete performs exactly one upstream request. Claiming invoked_at before the
// network call prevents replaying a charged provider request after a timeout.
func (s *Store) Complete(ctx context.Context, in CompletionRequest) (CompletionResponse, error) {
	if !s.CredentialsAvailable() {
		return CompletionResponse{}, ErrEncryptionUnavailable
	}
	r, _, err := scanReservation(s.DB.QueryRowContext(ctx, `SELECT `+reservationColumns+` FROM assistant.usage WHERE id=$1`, in.ReservationID))
	if err != nil {
		return CompletionResponse{}, err
	}
	if r.Status != "reserved" {
		return CompletionResponse{}, ErrConflict
	}
	if len(in.Messages) == 0 || len(in.Messages) > 200 || len(in.Tools) > 32 {
		return CompletionResponse{}, fmt.Errorf("assistant_invalid_messages")
	}
	var baseURL string
	var encrypted []byte
	identity := "provider:" + r.ProviderID
	if r.BYOCredentialID != "" {
		identity = "byo:" + r.BYOCredentialID
		err = s.DB.QueryRowContext(ctx, `SELECT base_url,encrypted_key FROM assistant.byo_credentials WHERE id=$1 AND owner_id=$2 AND enabled`, r.BYOCredentialID, r.UserID).Scan(&baseURL, &encrypted)
	} else {
		err = s.DB.QueryRowContext(ctx, `SELECT base_url,encrypted_key FROM assistant.providers WHERE id=$1 AND enabled`, r.ProviderID).Scan(&baseURL, &encrypted)
	}
	if err != nil {
		return CompletionResponse{}, dbError(err)
	}
	key, err := s.decryptSecret(encrypted, identity)
	if err != nil {
		return CompletionResponse{}, err
	}
	if key == "" {
		return CompletionResponse{}, fmt.Errorf("assistant_provider_key_required")
	}
	ctx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	u, err := publicweb.ValidateURL(ctx, strings.TrimRight(baseURL, "/")+"/chat/completions")
	if err != nil || u.Scheme != "https" {
		return CompletionResponse{}, fmt.Errorf("assistant_invalid_provider_url")
	}
	body, err := completionPayload(r, in)
	if err != nil {
		return CompletionResponse{}, err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE assistant.usage SET invoked_at=now() WHERE id=$1 AND status='reserved' AND invoked_at IS NULL`, r.ID)
	if err != nil {
		return CompletionResponse{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return CompletionResponse{}, err
	}
	if n != 1 {
		return CompletionResponse{}, ErrConflict
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("assistant_provider_request_failed")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	client := s.CompletionClient
	if client == nil {
		client = publicweb.NewClient(55 * time.Second)
	}
	response, err := client.Do(req)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("assistant_provider_transport_failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return CompletionResponse{}, fmt.Errorf("assistant_provider_http_%d", response.StatusCode)
	}
	return decodeCompletion(io.LimitReader(response.Body, 8*1024*1024))
}

func completionPayload(r Reservation, in CompletionRequest) ([]byte, error) {
	parameter := r.Model.MaxTokensParameter
	if parameter == "" {
		parameter = "max_tokens"
	}
	if parameter != "max_tokens" && parameter != "max_completion_tokens" {
		return nil, fmt.Errorf("assistant_invalid_token_parameter")
	}
	payload := map[string]any{"model": r.Model.UpstreamModel, "messages": in.Messages, parameter: r.OutputTokens, "stream": false}
	if len(in.Tools) > 0 {
		payload["tools"] = in.Tools
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(body) > 2*1024*1024 {
		return nil, fmt.Errorf("assistant_request_too_large")
	}
	return body, nil
}

func decodeCompletion(reader io.Reader) (CompletionResponse, error) {
	var raw struct {
		ID      string `json:"id"`
		Choices []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			PromptDetails    struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(reader).Decode(&raw); err != nil {
		return CompletionResponse{}, fmt.Errorf("assistant_provider_invalid_response")
	}
	if raw.Usage == nil {
		return CompletionResponse{}, fmt.Errorf("assistant_provider_usage_missing")
	}
	out := CompletionResponse{ID: raw.ID, Usage: TokenUsage{InputTokens: raw.Usage.PromptTokens, OutputTokens: raw.Usage.CompletionTokens, CachedInputTokens: raw.Usage.PromptDetails.CachedTokens}}
	if out.Usage.InputTokens < 0 || out.Usage.OutputTokens < 0 || out.Usage.CachedInputTokens < 0 || out.Usage.CachedInputTokens > out.Usage.InputTokens {
		return CompletionResponse{}, fmt.Errorf("assistant_provider_invalid_usage")
	}
	if len(raw.Choices) != 1 {
		return out, fmt.Errorf("assistant_provider_invalid_choices")
	}
	out.Message = raw.Choices[0].Message
	out.FinishReason = raw.Choices[0].FinishReason
	if out.Message.Role != "assistant" {
		return out, fmt.Errorf("assistant_provider_invalid_role")
	}
	for _, call := range out.Message.ToolCalls {
		if call.ID == "" || call.Type != "function" || call.Function.Name == "" || !json.Valid([]byte(call.Function.Arguments)) {
			return out, fmt.Errorf("assistant_provider_invalid_tool_call")
		}
	}
	return out, nil
}
