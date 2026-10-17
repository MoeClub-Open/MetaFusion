package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/publicweb"
)

type Store struct {
	DB            *sql.DB
	EncryptionKey []byte
	// CompletionClient is an optional code-only test transport. Production leaves
	// it nil so every connection uses the shared publicweb DNS validation.
	CompletionClient *http.Client
}

func NewStore(db *sql.DB, key []byte) *Store {
	return &Store{DB: db, EncryptionKey: append([]byte(nil), key...)}
}
func (s *Store) CredentialsAvailable() bool { return len(s.EncryptionKey) == 32 }

type scanner interface{ Scan(...any) error }

func dbError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func jsonBytes(v any) []byte { b, _ := json.Marshal(v); return b }
func nullableID(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func pageBounds(limit, offset int) (int, int) {
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
func validID(id string) bool { _, err := uuid.Parse(id); return err == nil }
func ensureID(id string) (string, error) {
	if id == "" {
		return uuid.NewString(), nil
	}
	if !validID(id) {
		return "", fmt.Errorf("assistant_invalid_id")
	}
	return id, nil
}

const providerColumns = `id,name,base_url,protocol,enabled,octet_length(encrypted_key)>0,key_hint,monthly_budget_microusd,created_at,updated_at`

func scanProvider(row scanner) (Provider, error) {
	var p Provider
	err := row.Scan(&p.ID, &p.Name, &p.BaseURL, &p.Protocol, &p.Enabled, &p.HasKey, &p.KeyHint, &p.MonthlyBudgetMicroUSD, &p.CreatedAt, &p.UpdatedAt)
	return p, dbError(err)
}
func (s *Store) Providers(ctx context.Context) ([]Provider, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+providerColumns+` FROM assistant.providers ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Provider{}
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) SaveProvider(ctx context.Context, in ProviderInput) (Provider, error) {
	id, err := ensureID(in.ID)
	if err != nil {
		return Provider{}, err
	}
	in.ID = id
	in.Name = strings.TrimSpace(in.Name)
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if in.Protocol == "" {
		in.Protocol = "openai_compatible"
	}
	if in.Name == "" || len(in.Name) > 200 || in.Protocol != "openai_compatible" || in.MonthlyBudgetMicroUSD < 0 {
		return Provider{}, fmt.Errorf("assistant_invalid_provider")
	}
	u, err := publicweb.ValidateURL(ctx, in.BaseURL)
	if err != nil || u.Scheme != "https" || u.RawQuery != "" {
		return Provider{}, fmt.Errorf("assistant_invalid_provider_url")
	}
	var encrypted any
	hint := ""
	if in.APIKey != nil {
		secret := strings.TrimSpace(*in.APIKey)
		if len(secret) > 16000 {
			return Provider{}, fmt.Errorf("assistant_invalid_key")
		}
		if secret != "" {
			b, e := s.encryptSecret(secret, "provider:"+id)
			if e != nil {
				return Provider{}, e
			}
			encrypted = b
			hint = keyHint(secret)
		} else {
			if !s.CredentialsAvailable() {
				return Provider{}, ErrEncryptionUnavailable
			}
			encrypted = []byte{}
		}
	}
	return scanProvider(s.DB.QueryRowContext(ctx, `INSERT INTO assistant.providers(id,name,base_url,protocol,enabled,encrypted_key,key_hint,monthly_budget_microusd) VALUES($1,$2,$3,$4,$5,COALESCE($6::bytea,''::bytea),$7,$8) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,base_url=EXCLUDED.base_url,protocol=EXCLUDED.protocol,enabled=EXCLUDED.enabled,encrypted_key=COALESCE($6::bytea,assistant.providers.encrypted_key),key_hint=CASE WHEN $6::bytea IS NULL THEN assistant.providers.key_hint ELSE $7 END,monthly_budget_microusd=EXCLUDED.monthly_budget_microusd,updated_at=now() RETURNING `+providerColumns, id, in.Name, in.BaseURL, in.Protocol, in.Enabled, encrypted, hint, in.MonthlyBudgetMicroUSD))
}
func (s *Store) DeleteProvider(ctx context.Context, id string) error {
	return s.deleteOne(ctx, `DELETE FROM assistant.providers WHERE id=$1`, id)
}
func (s *Store) deleteOne(ctx context.Context, query string, args ...any) error {
	res, err := s.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

const modelColumns = `id,provider_id,upstream_model,name,enabled,input_price_microusd_per_million,cached_input_price_microusd_per_million,output_price_microusd_per_million,input_credits_per_million,output_credits_per_million,max_output_tokens,max_tokens_parameter,context_window,created_at,updated_at`

func scanModel(row scanner) (Model, error) {
	var m Model
	err := row.Scan(&m.ID, &m.ProviderID, &m.UpstreamModel, &m.Name, &m.Enabled, &m.InputPriceMicroUSDPerMillion, &m.CachedInputPriceMicroUSDPerMillion, &m.OutputPriceMicroUSDPerMillion, &m.InputCreditsPerMillion, &m.OutputCreditsPerMillion, &m.MaxOutputTokens, &m.MaxTokensParameter, &m.ContextWindow, &m.CreatedAt, &m.UpdatedAt)
	return m, dbError(err)
}
func (s *Store) Models(ctx context.Context, enabledOnly bool) ([]Model, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+modelColumns+` FROM assistant.models WHERE NOT $1 OR (enabled AND provider_id IN(SELECT id FROM assistant.providers WHERE enabled)) ORDER BY name,id`, enabledOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Model{}
	for rows.Next() {
		m, e := scanModel(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func validModel(m Model) bool {
	return validID(m.ProviderID) && strings.TrimSpace(m.Name) != "" && len(m.Name) <= 200 && strings.TrimSpace(m.UpstreamModel) != "" && len(m.UpstreamModel) <= 200 && m.InputPriceMicroUSDPerMillion >= 0 && m.CachedInputPriceMicroUSDPerMillion >= 0 && m.OutputPriceMicroUSDPerMillion >= 0 && m.InputCreditsPerMillion >= 0 && m.OutputCreditsPerMillion >= 0 && m.MaxOutputTokens > 0 && m.ContextWindow >= m.MaxOutputTokens && m.ContextWindow <= 10000000 && (m.MaxTokensParameter == "max_tokens" || m.MaxTokensParameter == "max_completion_tokens")
}
func (s *Store) SaveModel(ctx context.Context, m Model) (Model, error) {
	id, err := ensureID(m.ID)
	if err != nil {
		return Model{}, err
	}
	m.ID = id
	if m.MaxTokensParameter == "" {
		m.MaxTokensParameter = "max_tokens"
	}
	if !validModel(m) {
		return Model{}, fmt.Errorf("assistant_invalid_model")
	}
	return scanModel(s.DB.QueryRowContext(ctx, `INSERT INTO assistant.models(id,provider_id,upstream_model,name,enabled,input_price_microusd_per_million,cached_input_price_microusd_per_million,output_price_microusd_per_million,input_credits_per_million,output_credits_per_million,max_output_tokens,context_window,max_tokens_parameter) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(id) DO UPDATE SET provider_id=EXCLUDED.provider_id,upstream_model=EXCLUDED.upstream_model,name=EXCLUDED.name,enabled=EXCLUDED.enabled,input_price_microusd_per_million=EXCLUDED.input_price_microusd_per_million,cached_input_price_microusd_per_million=EXCLUDED.cached_input_price_microusd_per_million,output_price_microusd_per_million=EXCLUDED.output_price_microusd_per_million,input_credits_per_million=EXCLUDED.input_credits_per_million,output_credits_per_million=EXCLUDED.output_credits_per_million,max_output_tokens=EXCLUDED.max_output_tokens,context_window=EXCLUDED.context_window,max_tokens_parameter=EXCLUDED.max_tokens_parameter,updated_at=now() RETURNING `+modelColumns, m.ID, m.ProviderID, m.UpstreamModel, m.Name, m.Enabled, m.InputPriceMicroUSDPerMillion, m.CachedInputPriceMicroUSDPerMillion, m.OutputPriceMicroUSDPerMillion, m.InputCreditsPerMillion, m.OutputCreditsPerMillion, m.MaxOutputTokens, m.ContextWindow, m.MaxTokensParameter))
}
func (s *Store) DeleteModel(ctx context.Context, id string) error {
	return s.deleteOne(ctx, `DELETE FROM assistant.models WHERE id=$1`, id)
}

const settingsColumns = `enabled,COALESCE(default_model_id::text,''),registration_credits,accepted_edit_credits,reward_effective_at,monthly_budget_microusd,max_tool_rounds,updated_at`

func scanSettings(row scanner) (Settings, error) {
	var v Settings
	err := row.Scan(&v.Enabled, &v.DefaultModelID, &v.RegistrationCredits, &v.AcceptedEditCredits, &v.RewardEffectiveAt, &v.MonthlyBudgetMicroUSD, &v.MaxToolRounds, &v.UpdatedAt)
	return v, dbError(err)
}
func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
	return scanSettings(s.DB.QueryRowContext(ctx, `SELECT `+settingsColumns+` FROM assistant.settings WHERE singleton`))
}
func (s *Store) SaveSettings(ctx context.Context, v Settings) (Settings, error) {
	if v.RegistrationCredits < 0 || v.AcceptedEditCredits < 0 || v.MonthlyBudgetMicroUSD < 0 || v.MaxToolRounds < 1 || v.MaxToolRounds > 12 || v.RewardEffectiveAt.IsZero() || (v.DefaultModelID != "" && !validID(v.DefaultModelID)) {
		return Settings{}, fmt.Errorf("assistant_invalid_settings")
	}
	return scanSettings(s.DB.QueryRowContext(ctx, `UPDATE assistant.settings SET enabled=$1,default_model_id=$2,registration_credits=$3,accepted_edit_credits=$4,reward_effective_at=$5,monthly_budget_microusd=$6,max_tool_rounds=$7,updated_at=now() WHERE singleton RETURNING `+settingsColumns, v.Enabled, nullableID(v.DefaultModelID), v.RegistrationCredits, v.AcceptedEditCredits, v.RewardEffectiveAt, v.MonthlyBudgetMicroUSD, v.MaxToolRounds))
}

const byoColumns = `id,owner_id,name,base_url,upstream_model,enabled,octet_length(encrypted_key)>0,key_hint,created_at,updated_at`

func scanBYO(row scanner) (BYOCredential, error) {
	var b BYOCredential
	err := row.Scan(&b.ID, &b.OwnerID, &b.Name, &b.BaseURL, &b.UpstreamModel, &b.Enabled, &b.HasKey, &b.KeyHint, &b.CreatedAt, &b.UpdatedAt)
	return b, dbError(err)
}
func (s *Store) BYOCredentials(ctx context.Context, ownerID string, admin bool, limit, offset int) (Page[BYOCredential], error) {
	limit, offset = pageBounds(limit, offset)
	p := Page[BYOCredential]{Items: []BYOCredential{}, Limit: limit, Offset: offset}
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM assistant.byo_credentials WHERE ($2 AND $1='') OR owner_id=$1`, ownerID, admin).Scan(&p.Total); err != nil {
		return p, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+byoColumns+` FROM assistant.byo_credentials WHERE ($2 AND $1='') OR owner_id=$1 ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4`, ownerID, admin, limit, offset)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		b, e := scanBYO(rows)
		if e != nil {
			return p, e
		}
		p.Items = append(p.Items, b)
	}
	return p, rows.Err()
}
func (s *Store) SaveBYOCredential(ctx context.Context, ownerID string, in BYOCredentialInput, admin bool) (BYOCredential, error) {
	id, err := ensureID(in.ID)
	if err != nil {
		return BYOCredential{}, err
	}
	in.ID = id
	if ownerID == "" {
		return BYOCredential{}, fmt.Errorf("assistant_owner_required")
	}
	in.Name = strings.TrimSpace(in.Name)
	in.UpstreamModel = strings.TrimSpace(in.UpstreamModel)
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if in.Name == "" || len(in.Name) > 200 || len(in.UpstreamModel) > 200 {
		return BYOCredential{}, fmt.Errorf("assistant_invalid_credential")
	}
	u, err := publicweb.ValidateURL(ctx, in.BaseURL)
	if err != nil || u.Scheme != "https" || u.RawQuery != "" {
		return BYOCredential{}, fmt.Errorf("assistant_invalid_provider_url")
	}
	var encrypted any
	hint := ""
	if in.APIKey != nil {
		secret := strings.TrimSpace(*in.APIKey)
		if secret == "" || len(secret) > 16000 {
			return BYOCredential{}, fmt.Errorf("assistant_invalid_key")
		}
		b, e := s.encryptSecret(secret, "byo:"+id)
		if e != nil {
			return BYOCredential{}, e
		}
		encrypted = b
		hint = keyHint(secret)
	}
	var exists bool
	err = s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM assistant.byo_credentials WHERE id=$1)`, id).Scan(&exists)
	if err != nil {
		return BYOCredential{}, err
	}
	if !exists && encrypted == nil {
		return BYOCredential{}, fmt.Errorf("assistant_key_required")
	}
	return scanBYO(s.DB.QueryRowContext(ctx, `INSERT INTO assistant.byo_credentials(id,owner_id,name,base_url,enabled,encrypted_key,key_hint,upstream_model) VALUES($1,$2,$3,$4,$5,COALESCE($6::bytea,''::bytea),$7,$9) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,base_url=EXCLUDED.base_url,upstream_model=EXCLUDED.upstream_model,enabled=EXCLUDED.enabled,encrypted_key=COALESCE($6::bytea,assistant.byo_credentials.encrypted_key),key_hint=CASE WHEN $6::bytea IS NULL THEN assistant.byo_credentials.key_hint ELSE $7 END,updated_at=now() WHERE assistant.byo_credentials.owner_id=$2 OR $8 RETURNING `+byoColumns, id, ownerID, in.Name, in.BaseURL, in.Enabled, encrypted, hint, admin, in.UpstreamModel))
}
func (s *Store) DeleteBYOCredential(ctx context.Context, ownerID, id string, admin bool) error {
	return s.deleteOne(ctx, `DELETE FROM assistant.byo_credentials WHERE id=$1 AND (owner_id=$2 OR $3)`, id, ownerID, admin)
}

func (s *Store) Budgets(ctx context.Context) ([]BudgetUsage, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT scope,month,spent_microusd,reserved_microusd FROM assistant.budget_months ORDER BY month DESC,scope`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BudgetUsage{}
	for rows.Next() {
		var v BudgetUsage
		if err = rows.Scan(&v.Scope, &v.Month, &v.SpentMicroUSD, &v.ReservedMicroUSD); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RecoverReservations releases only abandoned calls. A provider call may have incurred
// cost before a process crash; its failed status makes the missing usage explicit.
func (s *Store) RecoverReservations(ctx context.Context, before time.Time) (int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM assistant.usage WHERE status='reserved' AND created_at<$1 LIMIT 200`, before)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for i, id := range ids {
		if _, err = s.Settle(ctx, id, TokenUsage{}, "failed", "usage_unknown_after_interruption"); err != nil {
			return i, err
		}
	}
	return len(ids), nil
}
