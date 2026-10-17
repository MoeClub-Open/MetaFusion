package assistant

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
)

func scanBalance(row scanner) (Balance, error) {
	var b Balance
	err := row.Scan(&b.UserID, &b.Credits, &b.ReservedCredits, &b.UpdatedAt)
	b.AvailableCredits = b.Credits - b.ReservedCredits
	return b, dbError(err)
}

func (s *Store) SyncCredits(ctx context.Context, userID string) (Balance, error) {
	if userID == "" {
		return Balance{}, fmt.Errorf("assistant_owner_required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Balance{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO assistant.accounts(user_id) VALUES($1) ON CONFLICT DO NOTHING`, userID); err != nil {
		return Balance{}, err
	}
	if _, err = scanBalance(tx.QueryRowContext(ctx, `SELECT user_id,credits,reserved_credits,updated_at FROM assistant.accounts WHERE user_id=$1 FOR UPDATE`, userID)); err != nil {
		return Balance{}, err
	}
	settings, err := scanSettings(tx.QueryRowContext(ctx, `SELECT `+settingsColumns+` FROM assistant.settings WHERE singleton`))
	if err != nil {
		return Balance{}, err
	}
	if err = creditTx(ctx, tx, userID, "registration", userID, settings.RegistrationCredits, ""); err != nil {
		return Balance{}, err
	}
	// The migration activation time prevents a first visit from replaying all old edits.
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM catalog.commits WHERE actor_id=$1 AND (result->>'applied')='true' AND ((review_decision='approved' AND reviewed_at >= $2) OR (review_decision='exempt' AND created_at >= $2)) AND NOT EXISTS(SELECT 1 FROM assistant.credit_entries e WHERE e.user_id=$1 AND e.kind='accepted_edit' AND e.reference_id=catalog.commits.id::text) ORDER BY created_at,id`, userID, settings.RewardEffectiveAt)
	if err != nil {
		return Balance{}, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return Balance{}, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Balance{}, err
	}
	for _, id := range ids {
		if err = creditTx(ctx, tx, userID, "accepted_edit", id, settings.AcceptedEditCredits, ""); err != nil {
			return Balance{}, err
		}
	}
	b, err := scanBalance(tx.QueryRowContext(ctx, `SELECT user_id,credits,reserved_credits,updated_at FROM assistant.accounts WHERE user_id=$1`, userID))
	if err != nil {
		return b, err
	}
	return b, tx.Commit()
}

func creditTx(ctx context.Context, tx *sql.Tx, userID, kind, ref string, amount int64, note string) error {
	res, err := tx.ExecContext(ctx, `INSERT INTO assistant.credit_entries(id,user_id,kind,reference_id,credits,note) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(user_id,kind,reference_id) DO NOTHING`, uuid.NewString(), userID, kind, ref, amount, note)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE assistant.accounts SET credits=credits+$2,updated_at=now() WHERE user_id=$1`, userID, amount)
	return err
}

func (s *Store) AdjustCredits(ctx context.Context, in CreditAdjustment) (Balance, error) {
	if !validID(in.ID) || in.UserID == "" || strings.TrimSpace(in.Note) == "" || len(in.Note) > 1000 {
		return Balance{}, fmt.Errorf("assistant_invalid_adjustment")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Balance{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO assistant.accounts(user_id) VALUES($1) ON CONFLICT DO NOTHING`, in.UserID); err != nil {
		return Balance{}, err
	}
	b, err := scanBalance(tx.QueryRowContext(ctx, `SELECT user_id,credits,reserved_credits,updated_at FROM assistant.accounts WHERE user_id=$1 FOR UPDATE`, in.UserID))
	if err != nil {
		return b, err
	}
	var existingAmount int64
	var existingNote string
	err = tx.QueryRowContext(ctx, `SELECT credits,note FROM assistant.credit_entries WHERE user_id=$1 AND kind='adjustment' AND reference_id=$2`, in.UserID, in.ID).Scan(&existingAmount, &existingNote)
	if err == nil {
		if existingAmount != in.Credits || existingNote != in.Note {
			return b, ErrConflict
		}
		return b, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return b, err
	}
	if in.Credits < 0 && (in.Credits == math.MinInt64 || b.AvailableCredits < -in.Credits) {
		return b, ErrInsufficientCredits
	}
	if err = creditTx(ctx, tx, in.UserID, "adjustment", in.ID, in.Credits, in.Note); err != nil {
		return b, err
	}
	b, err = scanBalance(tx.QueryRowContext(ctx, `SELECT user_id,credits,reserved_credits,updated_at FROM assistant.accounts WHERE user_id=$1`, in.UserID))
	if err != nil {
		return b, err
	}
	return b, tx.Commit()
}

// priced computes ceil(tokens*rate/1e6) without integer overflow or floating point.
func priced(tokens, rate int64) (int64, error) {
	if tokens < 0 || rate < 0 {
		return 0, fmt.Errorf("assistant_invalid_usage")
	}
	n := new(big.Int).Mul(big.NewInt(tokens), big.NewInt(rate))
	n.Add(n, big.NewInt(999999))
	n.Quo(n, big.NewInt(1000000))
	if !n.IsInt64() {
		return 0, fmt.Errorf("assistant_amount_overflow")
	}
	return n.Int64(), nil
}
func addAmounts(values ...int64) (int64, error) {
	var sum int64
	for _, v := range values {
		if v < 0 || sum > math.MaxInt64-v {
			return 0, fmt.Errorf("assistant_amount_overflow")
		}
		sum += v
	}
	return sum, nil
}
func quote(m Model, u TokenUsage) (credits, cost int64, err error) {
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.CachedInputTokens < 0 || u.CachedInputTokens > u.InputTokens {
		return 0, 0, fmt.Errorf("assistant_invalid_usage")
	}
	parts := make([]int64, 5)
	pairs := [][2]int64{{u.InputTokens, m.InputCreditsPerMillion}, {u.OutputTokens, m.OutputCreditsPerMillion}, {u.InputTokens - u.CachedInputTokens, m.InputPriceMicroUSDPerMillion}, {u.CachedInputTokens, m.CachedInputPriceMicroUSDPerMillion}, {u.OutputTokens, m.OutputPriceMicroUSDPerMillion}}
	for i, p := range pairs {
		parts[i], err = priced(p[0], p[1])
		if err != nil {
			return 0, 0, err
		}
	}
	credits, err = addAmounts(parts[0], parts[1])
	if err != nil {
		return 0, 0, err
	}
	cost, err = addAmounts(parts[2:]...)
	return
}

func reserveHash(in ReserveRequest) string {
	sum := sha256.Sum256(jsonBytes(in))
	return hex.EncodeToString(sum[:])
}

const usageColumns = `id,user_id,provider_id,model_id,COALESCE(byo_credential_id::text,''),purpose,COALESCE(entity_id::text,''),status,error,input_tokens,output_tokens,cached_input_tokens,usage_estimated,credits,cost_microusd,price_snapshot,created_at,settled_at`

func scanUsage(row scanner) (UsageRecord, error) {
	var u UsageRecord
	var snapshot []byte
	err := row.Scan(&u.ID, &u.UserID, &u.ProviderID, &u.ModelID, &u.BYOCredentialID, &u.Purpose, &u.EntityID, &u.Status, &u.Error, &u.Usage.InputTokens, &u.Usage.OutputTokens, &u.Usage.CachedInputTokens, &u.UsageEstimated, &u.Credits, &u.CostMicroUSD, &snapshot, &u.CreatedAt, &u.SettledAt)
	if err != nil {
		return u, dbError(err)
	}
	err = json.Unmarshal(snapshot, &u.PriceSnapshot)
	return u, err
}
func scanReservation(row scanner) (Reservation, string, error) {
	var r Reservation
	var snapshot []byte
	var hash string
	err := row.Scan(&r.ID, &r.UserID, &r.ModelID, &r.BYOCredentialID, &r.InputTokens, &r.OutputTokens, &r.Purpose, &r.EntityID, &r.ProviderID, &r.ReservedCredits, &r.ReservedCostMicroUSD, &r.Status, &r.CreatedAt, &snapshot, &hash)
	if err != nil {
		return r, "", dbError(err)
	}
	err = json.Unmarshal(snapshot, &r.Model)
	return r, hash, err
}

const reservationColumns = `id,user_id,model_id,COALESCE(byo_credential_id::text,''),input_limit,output_limit,purpose,COALESCE(entity_id::text,''),provider_id,reserved_credits,reserved_cost_microusd,status,created_at,price_snapshot,request_hash`

func (s *Store) Reserve(ctx context.Context, in ReserveRequest) (Reservation, error) {
	if !s.CredentialsAvailable() {
		return Reservation{}, ErrEncryptionUnavailable
	}
	if !validID(in.ID) || in.UserID == "" || !validID(in.ModelID) || in.InputTokens < 1 || in.OutputTokens < 1 || len(in.Purpose) > 100 || in.Purpose == "" || (in.EntityID != "" && !validID(in.EntityID)) || (in.BYOCredentialID != "" && !validID(in.BYOCredentialID)) {
		return Reservation{}, fmt.Errorf("assistant_invalid_reservation")
	}
	if _, err := s.SyncCredits(ctx, in.UserID); err != nil {
		return Reservation{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Reservation{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, in.ID); err != nil {
		return Reservation{}, err
	}
	existing, hash, err := scanReservation(tx.QueryRowContext(ctx, `SELECT `+reservationColumns+` FROM assistant.usage WHERE id=$1`, in.ID))
	if err == nil {
		if hash != reserveHash(in) {
			return Reservation{}, ErrConflict
		}
		return existing, tx.Commit()
	}
	if err != ErrNotFound {
		return Reservation{}, err
	}
	settings, err := scanSettings(tx.QueryRowContext(ctx, `SELECT `+settingsColumns+` FROM assistant.settings WHERE singleton`))
	if err != nil {
		return Reservation{}, err
	}
	if !settings.Enabled {
		return Reservation{}, ErrDisabled
	}
	m, err := scanModel(tx.QueryRowContext(ctx, `SELECT `+modelColumns+` FROM assistant.models WHERE id=$1`, in.ModelID))
	if err != nil {
		return Reservation{}, err
	}
	if !m.Enabled {
		return Reservation{}, ErrDisabled
	}
	if in.OutputTokens > m.MaxOutputTokens || in.InputTokens > m.ContextWindow-in.OutputTokens {
		return Reservation{}, fmt.Errorf("assistant_context_limit")
	}
	var providerEnabled bool
	var providerBudget int64
	var hasKey bool
	err = tx.QueryRowContext(ctx, `SELECT enabled,monthly_budget_microusd,octet_length(encrypted_key)>0 FROM assistant.providers WHERE id=$1`, m.ProviderID).Scan(&providerEnabled, &providerBudget, &hasKey)
	if err != nil {
		return Reservation{}, dbError(err)
	}
	if !providerEnabled {
		return Reservation{}, ErrDisabled
	}
	reservationPricing := m
	if m.CachedInputPriceMicroUSDPerMillion > reservationPricing.InputPriceMicroUSDPerMillion {
		reservationPricing.InputPriceMicroUSDPerMillion = m.CachedInputPriceMicroUSDPerMillion
	}
	credits, cost, err := quote(reservationPricing, TokenUsage{InputTokens: in.InputTokens, OutputTokens: in.OutputTokens})
	if err != nil {
		return Reservation{}, err
	}
	month := time.Now().UTC()
	month = time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	if in.BYOCredentialID != "" {
		var available bool
		var upstreamModel string
		err = tx.QueryRowContext(ctx, `SELECT enabled AND octet_length(encrypted_key)>0,upstream_model FROM assistant.byo_credentials WHERE id=$1 AND owner_id=$2`, in.BYOCredentialID, in.UserID).Scan(&available, &upstreamModel)
		if err != nil {
			return Reservation{}, dbError(err)
		}
		if !available {
			return Reservation{}, ErrDisabled
		}
		if upstreamModel != "" {
			m.UpstreamModel = upstreamModel
		}
		credits, cost = 0, 0
	} else {
		if !hasKey {
			return Reservation{}, fmt.Errorf("assistant_provider_key_required")
		}
		b, e := scanBalance(tx.QueryRowContext(ctx, `SELECT user_id,credits,reserved_credits,updated_at FROM assistant.accounts WHERE user_id=$1 FOR UPDATE`, in.UserID))
		if e != nil {
			return Reservation{}, e
		}
		if b.AvailableCredits < credits {
			return Reservation{}, ErrInsufficientCredits
		}
		if _, err = tx.ExecContext(ctx, `UPDATE assistant.accounts SET reserved_credits=reserved_credits+$2,updated_at=now() WHERE user_id=$1`, in.UserID, credits); err != nil {
			return Reservation{}, err
		}
		for _, b := range []struct {
			scope string
			limit int64
		}{{"platform", settings.MonthlyBudgetMicroUSD}, {m.ProviderID, providerBudget}} {
			if err = reserveBudgetTx(ctx, tx, b.scope, month, b.limit, cost); err != nil {
				return Reservation{}, err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO assistant.usage(id,user_id,provider_id,model_id,byo_credential_id,purpose,entity_id,status,request_hash,price_snapshot,input_limit,output_limit,reserved_credits,reserved_cost_microusd,budget_month) VALUES($1,$2,$3,$4,$5,$6,$7,'reserved',$8,$9,$10,$11,$12,$13,$14)`, in.ID, in.UserID, m.ProviderID, in.ModelID, nullableID(in.BYOCredentialID), in.Purpose, nullableID(in.EntityID), reserveHash(in), jsonBytes(m), in.InputTokens, in.OutputTokens, credits, cost, month)
	if err != nil {
		return Reservation{}, err
	}
	r, _, err := scanReservation(tx.QueryRowContext(ctx, `SELECT `+reservationColumns+` FROM assistant.usage WHERE id=$1`, in.ID))
	if err != nil {
		return r, err
	}
	return r, tx.Commit()
}

func reserveBudgetTx(ctx context.Context, tx *sql.Tx, scope string, month time.Time, limit, amount int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO assistant.budget_months(scope,month) VALUES($1,$2) ON CONFLICT DO NOTHING`, scope, month); err != nil {
		return err
	}
	var spent, reserved int64
	if err := tx.QueryRowContext(ctx, `SELECT spent_microusd,reserved_microusd FROM assistant.budget_months WHERE scope=$1 AND month=$2 FOR UPDATE`, scope, month).Scan(&spent, &reserved); err != nil {
		return err
	}
	total, err := addAmounts(spent, reserved, amount)
	if err != nil {
		return err
	}
	if limit > 0 && total > limit {
		return ErrBudgetExceeded
	}
	_, err = tx.ExecContext(ctx, `UPDATE assistant.budget_months SET reserved_microusd=reserved_microusd+$3 WHERE scope=$1 AND month=$2`, scope, month, amount)
	return err
}

func (s *Store) Settle(ctx context.Context, id string, usage TokenUsage, status, errorText string) (UsageRecord, error) {
	if status != "completed" && status != "failed" && status != "cancelled" && status != "unknown" {
		return UsageRecord{}, fmt.Errorf("assistant_invalid_status")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return UsageRecord{}, err
	}
	defer tx.Rollback()
	r, _, err := scanReservation(tx.QueryRowContext(ctx, `SELECT `+reservationColumns+` FROM assistant.usage WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return UsageRecord{}, err
	}
	if r.Status != "reserved" {
		out, e := scanUsage(tx.QueryRowContext(ctx, `SELECT `+usageColumns+` FROM assistant.usage WHERE id=$1`, id))
		if e != nil {
			return out, e
		}
		return out, tx.Commit()
	}
	var invokedAt sql.NullTime
	if err = tx.QueryRowContext(ctx, `SELECT invoked_at FROM assistant.usage WHERE id=$1`, id).Scan(&invokedAt); err != nil {
		return UsageRecord{}, err
	}
	estimated := false
	if status != "completed" && invokedAt.Valid && usage.InputTokens == 0 && usage.OutputTokens == 0 && !knownNoCharge(errorText) {
		usage = TokenUsage{InputTokens: r.InputTokens, OutputTokens: r.OutputTokens}
		status = "unknown"
		estimated = true
		if errorText == "" {
			errorText = "assistant_provider_usage_unknown"
		}
	}
	credits, cost, err := quote(r.Model, usage)
	if err != nil {
		return UsageRecord{}, err
	}
	if estimated {
		credits, cost = r.ReservedCredits, r.ReservedCostMicroUSD
	}
	if r.BYOCredentialID != "" {
		credits, cost = 0, 0
	} else {
		if _, err = tx.ExecContext(ctx, `SELECT user_id FROM assistant.accounts WHERE user_id=$1 FOR UPDATE`, r.UserID); err != nil {
			return UsageRecord{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE assistant.accounts SET reserved_credits=reserved_credits-$2,updated_at=now() WHERE user_id=$1`, r.UserID, r.ReservedCredits); err != nil {
			return UsageRecord{}, err
		}
		if err = creditTx(ctx, tx, r.UserID, "usage", id, -credits, ""); err != nil {
			return UsageRecord{}, err
		}
		for _, scope := range []string{"platform", r.ProviderID} {
			if _, err = tx.ExecContext(ctx, `UPDATE assistant.budget_months SET reserved_microusd=reserved_microusd-$3,spent_microusd=spent_microusd+$4 WHERE scope=$1 AND month=(SELECT budget_month FROM assistant.usage WHERE id=$2)`, scope, id, r.ReservedCostMicroUSD, cost); err != nil {
				return UsageRecord{}, err
			}
		}
	}
	if len(errorText) > 1000 {
		errorText = errorText[:1000]
	}
	_, err = tx.ExecContext(ctx, `UPDATE assistant.usage SET status=$2,error=$3,input_tokens=$4,output_tokens=$5,cached_input_tokens=$6,credits=$7,cost_microusd=$8,usage_estimated=$9,settled_at=now() WHERE id=$1`, id, status, errorText, usage.InputTokens, usage.OutputTokens, usage.CachedInputTokens, credits, cost, estimated)
	if err != nil {
		return UsageRecord{}, err
	}
	out, err := scanUsage(tx.QueryRowContext(ctx, `SELECT `+usageColumns+` FROM assistant.usage WHERE id=$1`, id))
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func knownNoCharge(code string) bool {
	switch code {
	case "assistant_provider_http_400", "assistant_provider_http_401", "assistant_provider_http_403", "assistant_provider_http_404", "assistant_provider_http_405", "assistant_provider_http_413", "assistant_provider_http_422", "assistant_provider_http_429":
		return true
	}
	return false
}

func (s *Store) Accounts(ctx context.Context, limit, offset int) (Page[Balance], error) {
	limit, offset = pageBounds(limit, offset)
	p := Page[Balance]{Items: []Balance{}, Limit: limit, Offset: offset}
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM assistant.accounts`).Scan(&p.Total); err != nil {
		return p, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id,credits,reserved_credits,updated_at FROM assistant.accounts ORDER BY updated_at DESC,user_id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		b, e := scanBalance(rows)
		if e != nil {
			return p, e
		}
		p.Items = append(p.Items, b)
	}
	return p, rows.Err()
}
func (s *Store) CreditEntries(ctx context.Context, userID string, admin bool, limit, offset int) (Page[CreditEntry], error) {
	limit, offset = pageBounds(limit, offset)
	p := Page[CreditEntry]{Items: []CreditEntry{}, Limit: limit, Offset: offset}
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM assistant.credit_entries WHERE ($2 AND $1='') OR user_id=$1`, userID, admin).Scan(&p.Total); err != nil {
		return p, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,user_id,kind,reference_id,credits,note,created_at FROM assistant.credit_entries WHERE ($2 AND $1='') OR user_id=$1 ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4`, userID, admin, limit, offset)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var v CreditEntry
		if err = rows.Scan(&v.ID, &v.UserID, &v.Kind, &v.ReferenceID, &v.Credits, &v.Note, &v.CreatedAt); err != nil {
			return p, err
		}
		p.Items = append(p.Items, v)
	}
	return p, rows.Err()
}
func (s *Store) UsageRecords(ctx context.Context, userID string, admin bool, limit, offset int) (Page[UsageRecord], error) {
	limit, offset = pageBounds(limit, offset)
	p := Page[UsageRecord]{Items: []UsageRecord{}, Limit: limit, Offset: offset}
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM assistant.usage WHERE ($2 AND $1='') OR user_id=$1`, userID, admin).Scan(&p.Total); err != nil {
		return p, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+usageColumns+` FROM assistant.usage WHERE ($2 AND $1='') OR user_id=$1 ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4`, userID, admin, limit, offset)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		v, e := scanUsage(rows)
		if e != nil {
			return p, e
		}
		p.Items = append(p.Items, v)
	}
	return p, rows.Err()
}
