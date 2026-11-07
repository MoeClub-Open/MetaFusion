package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/metafusion/metafusion-app/internal/publicweb"
)

type SearchProvider struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Kind              string    `json:"kind"`
	BaseURL           string    `json:"base_url"`
	Enabled           bool      `json:"enabled"`
	IsDefault         bool      `json:"is_default"`
	HasKey            bool      `json:"has_key"`
	KeyHint           string    `json:"key_hint"`
	CreditsPerRequest int64     `json:"credits_per_request"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type SearchProviderInput struct {
	SearchProvider
	APIKey *string `json:"api_key,omitempty"`
}

type SearchInput struct {
	Query       string `json:"query,omitempty"`
	URL         string `json:"url,omitempty"`
	Count       int    `json:"count,omitempty"`
	NeedContent bool   `json:"need_content,omitempty"`
}

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
	Summary string `json:"summary,omitempty"`
	Content string `json:"content,omitempty"`
}

type SearchResponse struct {
	Results []SearchResult `json:"results,omitempty"`
	Title   string         `json:"title,omitempty"`
	Content string         `json:"content,omitempty"`
	Credits int64          `json:"credits"`
}

type SearchOptions struct {
	RequestID     string
	UserID        string
	ProviderID    string
	BYOProviderID string
	Purpose       string
	Budget        *CreditBudget
}

type SearchRequestRecord struct {
	ID                   string    `json:"id"`
	UserID               string    `json:"user_id"`
	ProviderID           string    `json:"provider_id"`
	Purpose              string    `json:"purpose"`
	Operation            string    `json:"operation"`
	Status               string    `json:"status"`
	Credits              int64     `json:"credits"`
	UpstreamCostMicroCNY int64     `json:"upstream_cost_microcny"`
	Error                string    `json:"error"`
	CreatedAt            time.Time `json:"created_at"`
}

const searchProviderColumns = `id,name,kind,base_url,enabled,is_default,octet_length(encrypted_key)>0,key_hint,credits_per_request,updated_at`

func scanSearchProvider(row scanner) (SearchProvider, error) {
	var p SearchProvider
	err := row.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.Enabled, &p.IsDefault, &p.HasKey, &p.KeyHint, &p.CreditsPerRequest, &p.UpdatedAt)
	return p, dbError(err)
}

func (s *Store) SearchProviders(ctx context.Context, public bool) ([]SearchProvider, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+searchProviderColumns+` FROM assistant.search_providers WHERE NOT $1 OR enabled ORDER BY is_default DESC,name,id`, public)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SearchProvider{}
	for rows.Next() {
		p, e := scanSearchProvider(rows)
		if e != nil {
			return nil, e
		}
		if public {
			p.KeyHint = ""
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) SaveSearchProvider(ctx context.Context, in SearchProviderInput) (SearchProvider, error) {
	id, err := ensureID(in.ID)
	if err != nil {
		return SearchProvider{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if in.Name == "" || (in.Kind != "duckduckgo" && in.Kind != "deepseek") || in.CreditsPerRequest < 0 || in.CreditsPerRequest > 1000000 || in.IsDefault && !in.Enabled {
		return SearchProvider{}, errors.New("assistant_invalid_search_provider")
	}
	u, err := publicweb.ValidateURL(ctx, in.BaseURL)
	if err != nil || u.Scheme != "https" || u.RawQuery != "" {
		return SearchProvider{}, errors.New("assistant_invalid_provider_url")
	}
	if in.Kind == "duckduckgo" {
		in.CreditsPerRequest = 0
		if in.BaseURL != "https://html.duckduckgo.com" {
			return SearchProvider{}, errors.New("assistant_invalid_search_provider")
		}
	}
	var encrypted any
	hint := ""
	if in.APIKey != nil {
		key := strings.TrimSpace(*in.APIKey)
		if key != "" {
			b, e := s.encryptSecret(key, "search:"+id)
			if e != nil {
				return SearchProvider{}, e
			}
			encrypted = b
			hint = keyHint(key)
		} else {
			encrypted = []byte{}
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return SearchProvider{}, err
	}
	defer tx.Rollback()
	// Serialize default changes instead of exposing a moment with two defaults.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(740212)`); err != nil {
		return SearchProvider{}, err
	}
	if in.IsDefault {
		if _, err = tx.ExecContext(ctx, `UPDATE assistant.search_providers SET is_default=false WHERE is_default AND id<>$1`, id); err != nil {
			return SearchProvider{}, err
		}
	}
	p, err := scanSearchProvider(tx.QueryRowContext(ctx, `INSERT INTO assistant.search_providers(id,name,kind,base_url,enabled,is_default,encrypted_key,key_hint,credits_per_request)
 VALUES($1,$2,$3,$4,$5,$6,COALESCE($7::bytea,''::bytea),$8,$9) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,kind=EXCLUDED.kind,base_url=EXCLUDED.base_url,enabled=EXCLUDED.enabled,is_default=EXCLUDED.is_default,encrypted_key=COALESCE($7::bytea,assistant.search_providers.encrypted_key),key_hint=CASE WHEN $7::bytea IS NULL THEN assistant.search_providers.key_hint ELSE $8 END,credits_per_request=EXCLUDED.credits_per_request,updated_at=now() RETURNING `+searchProviderColumns, id, in.Name, in.Kind, in.BaseURL, in.Enabled, in.IsDefault, encrypted, hint, in.CreditsPerRequest))
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}

// ValidateOwnedBYOProvider is also used when a background job binds its billing
// owner. The model and the settings payload cannot select somebody else's key.
func (s *Store) ValidateOwnedBYOProvider(ctx context.Context, owner, id string) error {
	if id == "" {
		return nil
	}
	var ok bool
	err := s.DB.QueryRowContext(ctx, `SELECT enabled AND octet_length(encrypted_key)>0 FROM assistant.byo_providers WHERE id=$1 AND owner_id=$2`, id, owner).Scan(&ok)
	if err != nil {
		return dbError(err)
	}
	if !ok {
		return ErrDisabled
	}
	return nil
}

func (s *Store) SearchRequests(ctx context.Context, owner string, admin bool, limit, offset int) (Page[SearchRequestRecord], error) {
	limit, offset = pageBounds(limit, offset)
	out := Page[SearchRequestRecord]{Items: []SearchRequestRecord{}, Limit: limit, Offset: offset}
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM assistant.search_requests WHERE ($1 AND $2='') OR user_id=$2`, admin, owner).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,user_id,provider_id,purpose,operation,status,credits,upstream_cost_microcny,error,created_at FROM assistant.search_requests WHERE ($1 AND $2='') OR user_id=$2 ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4`, admin, owner, limit, offset)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r SearchRequestRecord
		if err = rows.Scan(&r.ID, &r.UserID, &r.ProviderID, &r.Purpose, &r.Operation, &r.Status, &r.Credits, &r.UpstreamCostMicroCNY, &r.Error, &r.CreatedAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, r)
	}
	return out, rows.Err()
}

func budgetCommitted(ctx context.Context, tx *sql.Tx, user, prefix string, month time.Time) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx, `SELECT
 (SELECT COALESCE(sum(CASE WHEN status='reserved' THEN reserved_credits ELSE credits END),0) FROM assistant.usage WHERE user_id=$1 AND starts_with(purpose,$2) AND budget_month=$3 AND status IN ('reserved','completed','failed','unknown')) +
 (SELECT COALESCE(sum(credits),0) FROM assistant.search_requests WHERE user_id=$1 AND starts_with(purpose,$2) AND budget_month=$3 AND status IN ('reserved','completed','unknown'))`, user, prefix, month).Scan(&n)
	return n, err
}

func totalBudgetCommitted(ctx context.Context, tx *sql.Tx, user, prefix string) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx, `SELECT
 (SELECT COALESCE(sum(CASE WHEN status='reserved' THEN reserved_credits ELSE credits END),0) FROM assistant.usage WHERE user_id=$1 AND starts_with(purpose,$2) AND status IN ('reserved','completed','failed','unknown')) +
 (SELECT COALESCE(sum(credits),0) FROM assistant.search_requests WHERE user_id=$1 AND starts_with(purpose,$2) AND status IN ('reserved','completed','unknown'))`, user, prefix).Scan(&n)
	return n, err
}

func (s *Store) reserveSearch(ctx context.Context, opt SearchOptions, p SearchProvider, operation string, in SearchInput) (*SearchResponse, int64, error) {
	if _, err := s.SyncCredits(ctx, opt.UserID); err != nil {
		return nil, 0, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, opt.RequestID); err != nil {
		return nil, 0, err
	}
	var status, owner, pid, op, purpose, byoProvider string
	var previous, result []byte
	var credits int64
	err = tx.QueryRowContext(ctx, `SELECT status,user_id,provider_id,operation,purpose,COALESCE(byo_provider_id::text,''),input,result,credits FROM assistant.search_requests WHERE id=$1`, opt.RequestID).Scan(&status, &owner, &pid, &op, &purpose, &byoProvider, &previous, &result, &credits)
	if err == nil {
		var old SearchInput
		_ = json.Unmarshal(previous, &old)
		if owner != opt.UserID || pid != p.ID || op != operation || purpose != opt.Purpose || byoProvider != opt.BYOProviderID || old != in {
			return nil, 0, ErrConflict
		}
		if status != "completed" && status != "empty" {
			return nil, 0, ErrConflict
		}
		var response SearchResponse
		if err = json.Unmarshal(result, &response); err != nil {
			return nil, 0, err
		}
		return &response, credits, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return nil, 0, err
	}
	credits = p.CreditsPerRequest
	if opt.BYOProviderID != "" {
		credits = 0
	}
	b, err := scanBalance(tx.QueryRowContext(ctx, `SELECT user_id,credits,reserved_credits,updated_at FROM assistant.accounts WHERE user_id=$1 FOR UPDATE`, opt.UserID))
	if err != nil {
		return nil, 0, err
	}
	if b.AvailableCredits < credits {
		return nil, 0, ErrInsufficientCredits
	}
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if opt.Budget != nil && credits > 0 {
		budget := opt.Budget
		if budget.PurposePrefix == "" && !budget.EnforceTotal {
			return nil, 0, ErrBudgetExceeded
		}
		if budget.PurposePrefix != "" {
			if budget.MonthlyLimit <= 0 || !strings.HasPrefix(opt.Purpose, budget.PurposePrefix) {
				return nil, 0, ErrBudgetExceeded
			}
			n, e := budgetCommitted(ctx, tx, opt.UserID, budget.PurposePrefix, month)
			if e != nil {
				return nil, 0, e
			}
			if n+credits > budget.MonthlyLimit {
				return nil, 0, ErrBudgetExceeded
			}
		}
		if budget.EnforceTotal {
			if budget.TotalLimit <= 0 || budget.TotalPrefix == "" || !strings.HasPrefix(opt.Purpose, budget.TotalPrefix) {
				return nil, 0, ErrBudgetExceeded
			}
			n, e := totalBudgetCommitted(ctx, tx, opt.UserID, budget.TotalPrefix)
			if e != nil {
				return nil, 0, e
			}
			if n+credits > budget.TotalLimit {
				return nil, 0, ErrBudgetExceeded
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE assistant.accounts SET reserved_credits=reserved_credits+$2,updated_at=now() WHERE user_id=$1`, opt.UserID, credits); err != nil {
		return nil, 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO assistant.search_requests(id,user_id,provider_id,byo_provider_id,purpose,operation,input,status,credits,budget_month) VALUES($1,$2,$3,$4,$5,$6,$7,'reserved',$8,$9)`, opt.RequestID, opt.UserID, p.ID, nullableID(opt.BYOProviderID), opt.Purpose, operation, jsonBytes(in), credits, month)
	if err != nil {
		return nil, 0, err
	}
	return nil, credits, tx.Commit()
}

func (s *Store) settleSearch(ctx context.Context, id, status, code string, response SearchResponse, upstreamCost int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner, current string
	var reserved int64
	if err = tx.QueryRowContext(ctx, `SELECT user_id,status,credits FROM assistant.search_requests WHERE id=$1 FOR UPDATE`, id).Scan(&owner, &current, &reserved); err != nil {
		return err
	}
	if current != "reserved" {
		return tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM assistant.accounts WHERE user_id=$1 FOR UPDATE`, owner); err != nil {
		return err
	}
	credits := reserved
	if status != "completed" && status != "unknown" {
		credits = 0
	}
	if _, err = tx.ExecContext(ctx, `UPDATE assistant.accounts SET reserved_credits=reserved_credits-$2,updated_at=now() WHERE user_id=$1`, owner, reserved); err != nil {
		return err
	}
	if credits > 0 {
		if err = creditTx(ctx, tx, owner, "usage", id, -credits, "web "+status); err != nil {
			return err
		}
	}
	response.Credits = credits
	_, err = tx.ExecContext(ctx, `UPDATE assistant.search_requests SET status=$2,credits=$3,result=$4,error=$5,upstream_cost_microcny=$6,finished_at=now() WHERE id=$1`, id, status, credits, jsonBytes(response), code, upstreamCost)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) recoverSearchReservations(ctx context.Context, before time.Time) (int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM assistant.search_requests WHERE status='reserved' AND created_at<$1 LIMIT 200`, before)
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
		if err = s.settleSearch(ctx, id, "unknown", "usage_unknown_after_interruption", SearchResponse{}, 0); err != nil {
			return i, err
		}
	}
	return len(ids), nil
}
