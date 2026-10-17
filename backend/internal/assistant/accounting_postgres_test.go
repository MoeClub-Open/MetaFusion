package assistant

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/migrator"
	"github.com/metafusion/metafusion-app/internal/testutil"
	"github.com/metafusion/metafusion-app/migrations"
)

func accountingFixture(t *testing.T) (*Store, Model, string) {
	t.Helper()
	db := testutil.Database(t)
	ctx := context.Background()
	if err := migrator.New(db, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	s := NewStore(db, bytes.Repeat([]byte{0x42}, 32))
	providerID := uuid.NewString()
	modelID := uuid.NewString()
	ownerID := uuid.NewString()
	encrypted, err := s.encryptSecret("integration-test-secret", "provider:"+providerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO assistant.providers(id,name,base_url,protocol,enabled,encrypted_key) VALUES($1,'test','https://api.example.com/v1','openai_compatible',true,$2)`, providerID, encrypted); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO assistant.models(id,provider_id,upstream_model,name,enabled,input_price_microusd_per_million,output_price_microusd_per_million,input_credits_per_million,output_credits_per_million,max_output_tokens,context_window) VALUES($1,$2,'test-model','Test',true,1000000,1000000,1000000,1000000,100,1000)`, modelID, providerID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE assistant.settings SET enabled=true,default_model_id=$1,registration_credits=100,accepted_edit_credits=10`, modelID); err != nil {
		t.Fatal(err)
	}
	models, err := s.Models(ctx, false)
	if err != nil || len(models) != 1 {
		t.Fatalf("models: %v %v", models, err)
	}
	return s, models[0], ownerID
}

func TestPostgresConcurrentBudgetAndIdempotentSettlement(t *testing.T) {
	s, m, ownerID := accountingFixture(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(`UPDATE assistant.settings SET monthly_budget_microusd=30`); err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	var winner string
	var mu sync.Mutex
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := ReserveRequest{ID: uuid.NewString(), UserID: ownerID, ModelID: m.ID, InputTokens: 10, OutputTokens: 10, Purpose: "test"}
			_, err := s.Reserve(ctx, in)
			if err == nil {
				successes.Add(1)
				mu.Lock()
				winner = in.ID
				mu.Unlock()
			} else if !errors.Is(err, ErrBudgetExceeded) {
				t.Errorf("reserve: %v", err)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("budget overspent: %d reservations", successes.Load())
	}
	// Changing current prices must not alter an already reserved call's snapshot.
	if _, err := s.DB.Exec(`UPDATE assistant.models SET input_price_microusd_per_million=9000000,input_credits_per_million=9000000 WHERE id=$1`, m.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, err := s.Settle(ctx, winner, TokenUsage{InputTokens: 3, OutputTokens: 2}, "completed", "")
			if err != nil || u.Credits != 5 || u.CostMicroUSD != 5 {
				t.Errorf("settle: %+v %v", u, err)
			}
		}()
	}
	wg.Wait()
	b, err := s.SyncCredits(ctx, ownerID)
	if err != nil || b.Credits != 95 || b.ReservedCredits != 0 {
		t.Fatalf("double settlement: %+v %v", b, err)
	}
	budgets, err := s.Budgets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range budgets {
		if b.SpentMicroUSD != 5 || b.ReservedMicroUSD != 0 {
			t.Fatalf("budget mismatch: %+v", b)
		}
	}
	var entries int
	if err = s.DB.QueryRow(`SELECT count(*) FROM assistant.credit_entries WHERE kind='usage' AND reference_id=$1`, winner).Scan(&entries); err != nil || entries != 1 {
		t.Fatalf("ledger replay: %d %v", entries, err)
	}
}

func TestPostgresCreditsReservationAndReplayProtection(t *testing.T) {
	s, m, ownerID := accountingFixture(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(`UPDATE assistant.settings SET registration_credits=25`); err != nil {
		t.Fatal(err)
	}
	in := ReserveRequest{ID: uuid.NewString(), UserID: ownerID, ModelID: m.ID, InputTokens: 10, OutputTokens: 10, Purpose: "test"}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Reserve(ctx, in); err != nil {
				t.Errorf("same-id replay: %v", err)
			}
		}()
	}
	wg.Wait()
	if _, err := s.Reserve(ctx, ReserveRequest{ID: uuid.NewString(), UserID: ownerID, ModelID: m.ID, InputTokens: 5, OutputTokens: 5, Purpose: "test"}); !errors.Is(err, ErrInsufficientCredits) {
		t.Fatalf("concurrent reserved credits available twice: %v", err)
	}
	changed := in
	changed.OutputTokens = 11
	if _, err := s.Reserve(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("different payload reuses id: %v", err)
	}
	if _, err := s.Settle(ctx, in.ID, TokenUsage{}, "cancelled", ""); err != nil {
		t.Fatal(err)
	}
	b, err := s.SyncCredits(ctx, ownerID)
	if err != nil || b.AvailableCredits != 25 {
		t.Fatalf("uninvoked reservation not released: %+v %v", b, err)
	}
}

func TestPostgresRewardPolicyAndUniqueness(t *testing.T) {
	s, _, ownerID := accountingFixture(t)
	ctx := context.Background()
	effective := time.Now().UTC()
	if _, err := s.DB.Exec(`UPDATE assistant.settings SET reward_effective_at=$1`, effective); err != nil {
		t.Fatal(err)
	}
	insert := func(decision string, applied bool, when time.Time) {
		t.Helper()
		status := "reviewed"
		if decision == "pending" {
			status = "pending_review"
		}
		_, err := s.DB.Exec(`INSERT INTO catalog.commits(id,actor_id,request_hash,request,result,review_status,review_decision,reviewed_at,created_at,author_snapshot) VALUES($1,$2,'test','{}',jsonb_build_object('applied',$3::boolean),$4,$5,$6,$6,jsonb_build_object('id',$2::text))`, uuid.NewString(), ownerID, applied, status, decision, when)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("approved", true, effective.Add(time.Second))
	insert("exempt", true, effective.Add(time.Second))
	insert("approved", true, effective.Add(-time.Second))
	insert("pending", false, effective.Add(time.Second))
	insert("rejected", false, effective.Add(time.Second))
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := s.SyncCredits(ctx, ownerID)
			if err != nil || b.Credits != 120 {
				t.Errorf("reward: %+v %v", b, err)
			}
		}()
	}
	wg.Wait()
	entries, err := s.CreditEntries(ctx, ownerID, false, 50, 0)
	if err != nil || entries.Total != 3 {
		t.Fatalf("duplicate rewards: %+v %v", entries, err)
	}
	adjustment := CreditAdjustment{ID: uuid.NewString(), UserID: ownerID, Credits: 7, Note: "test adjustment"}
	for i := 0; i < 2; i++ {
		b, err := s.AdjustCredits(ctx, adjustment)
		if err != nil || b.Credits != 127 {
			t.Fatalf("adjustment replay %+v %v", b, err)
		}
	}
	adjustment.Credits = 8
	if _, err = s.AdjustCredits(ctx, adjustment); !errors.Is(err, ErrConflict) {
		t.Fatalf("adjustment changed on replay: %v", err)
	}
}

func TestPostgresBYOIsolationAndNoPlatformCharges(t *testing.T) {
	s, m, ownerID := accountingFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	encrypted, err := s.encryptSecret("owner-test-key", "byo:"+id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`INSERT INTO assistant.byo_credentials(id,owner_id,name,base_url,encrypted_key) VALUES($1,$2,'Own','https://api.example.com/v1',$3)`, id, ownerID, encrypted); err != nil {
		t.Fatal(err)
	}
	other := uuid.NewString()
	in := ReserveRequest{ID: uuid.NewString(), UserID: other, ModelID: m.ID, BYOCredentialID: id, InputTokens: 10, OutputTokens: 10, Purpose: "test"}
	if _, err = s.Reserve(ctx, in); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner credential usable: %v", err)
	}
	if err = s.DeleteBYOCredential(ctx, other, id, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner credential deletable: %v", err)
	}
	list, err := s.BYOCredentials(ctx, other, false, 50, 0)
	if err != nil || list.Total != 0 {
		t.Fatalf("cross-owner credential listed: %+v %v", list, err)
	}
	in.ID = uuid.NewString()
	in.UserID = ownerID
	if _, err = s.Reserve(ctx, in); err != nil {
		t.Fatal(err)
	}
	u, err := s.Settle(ctx, in.ID, TokenUsage{InputTokens: 10, OutputTokens: 5, CachedInputTokens: 3}, "completed", "")
	if err != nil || u.Credits != 0 || u.CostMicroUSD != 0 || u.Usage.CachedInputTokens != 3 {
		t.Fatalf("BYO accounting: %+v %v", u, err)
	}
	budgets, err := s.Budgets(ctx)
	if err != nil || len(budgets) != 0 {
		t.Fatalf("BYO consumes platform budget: %+v %v", budgets, err)
	}
}

func TestPostgresUnknownProviderUsageIsConservativelySettled(t *testing.T) {
	s, m, ownerID := accountingFixture(t)
	ctx := context.Background()
	in := ReserveRequest{ID: uuid.NewString(), UserID: ownerID, ModelID: m.ID, InputTokens: 10, OutputTokens: 10, Purpose: "test"}
	if _, err := s.Reserve(ctx, in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE assistant.usage SET invoked_at=now() WHERE id=$1`, in.ID); err != nil {
		t.Fatal(err)
	}
	u, err := s.Settle(ctx, in.ID, TokenUsage{}, "failed", "assistant_provider_transport_failed")
	if err != nil || u.Status != "unknown" || !u.UsageEstimated || u.Credits != 20 || u.CostMicroUSD != 20 {
		t.Fatalf("unknown usage refunded: %+v %v", u, err)
	}
	b, err := s.SyncCredits(ctx, ownerID)
	if err != nil || b.AvailableCredits != 80 {
		t.Fatalf("unknown cost ledger: %+v %v", b, err)
	}
}
