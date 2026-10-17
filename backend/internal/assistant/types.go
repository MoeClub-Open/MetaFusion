// Package assistant owns LLM configuration, credentials, usage and credit accounting.
package assistant

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound              = errors.New("assistant_not_found")
	ErrDisabled              = errors.New("assistant_disabled")
	ErrEncryptionUnavailable = errors.New("assistant_encryption_unavailable")
	ErrInsufficientCredits   = errors.New("assistant_insufficient_credits")
	ErrBudgetExceeded        = errors.New("assistant_budget_exceeded")
	ErrConflict              = errors.New("assistant_request_conflict")
)

type Page[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type Settings struct {
	Enabled               bool      `json:"enabled"`
	DefaultModelID        string    `json:"default_model_id"`
	RegistrationCredits   int64     `json:"registration_credits"`
	AcceptedEditCredits   int64     `json:"accepted_edit_credits"`
	RewardEffectiveAt     time.Time `json:"reward_effective_at"`
	MonthlyBudgetMicroUSD int64     `json:"monthly_budget_microusd"`
	MaxToolRounds         int       `json:"max_tool_rounds"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type Provider struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	BaseURL               string    `json:"base_url"`
	Protocol              string    `json:"protocol"`
	Enabled               bool      `json:"enabled"`
	HasKey                bool      `json:"has_key"`
	KeyHint               string    `json:"key_hint"`
	MonthlyBudgetMicroUSD int64     `json:"monthly_budget_microusd"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type ProviderInput struct {
	Provider
	APIKey *string `json:"api_key,omitempty"`
}

type Model struct {
	ID                                 string    `json:"id"`
	ProviderID                         string    `json:"provider_id"`
	UpstreamModel                      string    `json:"upstream_model"`
	Name                               string    `json:"name"`
	Enabled                            bool      `json:"enabled"`
	InputPriceMicroUSDPerMillion       int64     `json:"input_price_microusd_per_million"`
	CachedInputPriceMicroUSDPerMillion int64     `json:"cached_input_price_microusd_per_million"`
	OutputPriceMicroUSDPerMillion      int64     `json:"output_price_microusd_per_million"`
	InputCreditsPerMillion             int64     `json:"input_credits_per_million"`
	OutputCreditsPerMillion            int64     `json:"output_credits_per_million"`
	MaxOutputTokens                    int64     `json:"max_output_tokens"`
	MaxTokensParameter                 string    `json:"max_tokens_parameter"`
	ContextWindow                      int64     `json:"context_window"`
	CreatedAt                          time.Time `json:"created_at"`
	UpdatedAt                          time.Time `json:"updated_at"`
}

type BYOCredential struct {
	ID            string    `json:"id"`
	OwnerID       string    `json:"owner_id"`
	Name          string    `json:"name"`
	BaseURL       string    `json:"base_url"`
	UpstreamModel string    `json:"upstream_model"`
	Enabled       bool      `json:"enabled"`
	HasKey        bool      `json:"has_key"`
	KeyHint       string    `json:"key_hint"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
type BYOCredentialInput struct {
	BYOCredential
	APIKey *string `json:"api_key,omitempty"`
}

type Balance struct {
	UserID           string    `json:"user_id"`
	Credits          int64     `json:"credits"`
	ReservedCredits  int64     `json:"reserved_credits"`
	AvailableCredits int64     `json:"available_credits"`
	UpdatedAt        time.Time `json:"updated_at"`
}
type CreditEntry struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Kind        string    `json:"kind"`
	ReferenceID string    `json:"reference_id"`
	Credits     int64     `json:"credits"`
	Note        string    `json:"note"`
	CreatedAt   time.Time `json:"created_at"`
}
type CreditAdjustment struct {
	ID      string `json:"id"`
	UserID  string `json:"user_id"`
	Credits int64  `json:"credits"`
	Note    string `json:"note"`
}
type TokenUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
}
type ReserveRequest struct {
	ID              string `json:"id"`
	UserID          string `json:"user_id"`
	ModelID         string `json:"model_id"`
	BYOCredentialID string `json:"byo_credential_id,omitempty"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	Purpose         string `json:"purpose"`
	EntityID        string `json:"entity_id,omitempty"`
}
type Reservation struct {
	ReserveRequest
	Model                Model     `json:"model"`
	ProviderID           string    `json:"provider_id"`
	ReservedCredits      int64     `json:"reserved_credits"`
	ReservedCostMicroUSD int64     `json:"reserved_cost_microusd"`
	Status               string    `json:"status"`
	CreatedAt            time.Time `json:"created_at"`
}
type UsageRecord struct {
	ID              string     `json:"id"`
	UserID          string     `json:"user_id"`
	ProviderID      string     `json:"provider_id"`
	ModelID         string     `json:"model_id"`
	BYOCredentialID string     `json:"byo_credential_id,omitempty"`
	Purpose         string     `json:"purpose"`
	EntityID        string     `json:"entity_id,omitempty"`
	Status          string     `json:"status"`
	Error           string     `json:"error,omitempty"`
	Usage           TokenUsage `json:"usage"`
	UsageEstimated  bool       `json:"usage_estimated"`
	Credits         int64      `json:"credits"`
	CostMicroUSD    int64      `json:"cost_microusd"`
	PriceSnapshot   Model      `json:"price_snapshot"`
	CreatedAt       time.Time  `json:"created_at"`
	SettledAt       *time.Time `json:"settled_at,omitempty"`
}
type BudgetUsage struct {
	Scope            string    `json:"scope"`
	Month            time.Time `json:"month"`
	SpentMicroUSD    int64     `json:"spent_microusd"`
	ReservedMicroUSD int64     `json:"reserved_microusd"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}
type Tool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}
type CompletionRequest struct {
	ReservationID string    `json:"reservation_id"`
	Messages      []Message `json:"messages"`
	Tools         []Tool    `json:"tools,omitempty"`
}
type CompletionResponse struct {
	ID           string     `json:"id"`
	Message      Message    `json:"message"`
	Usage        TokenUsage `json:"usage"`
	FinishReason string     `json:"finish_reason"`
}

type Conversation struct {
	ID              string    `json:"id"`
	OwnerID         string    `json:"owner_id"`
	Title           string    `json:"title"`
	ModelID         string    `json:"model_id"`
	BYOCredentialID string    `json:"byo_credential_id,omitempty"`
	Messages        []Message `json:"messages"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type GeneratedContent struct {
	ID           string          `json:"id"`
	EntityID     string          `json:"entity_id"`
	Locale       string          `json:"locale"`
	ContextHash  string          `json:"context_hash"`
	ModelID      string          `json:"model_id"`
	OwnerID      string          `json:"owner_id"`
	Content      json.RawMessage `json:"content"`
	Dependencies json.RawMessage `json:"dependencies"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}
