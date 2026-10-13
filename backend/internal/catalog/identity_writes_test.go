package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestCommitIdentityUpdateReviewValidation(t *testing.T) {
	for _, tc := range []struct {
		path string
		need bool
	}{
		{"/title", true}, {"/external_ids", true}, {"/external_ids/wikidata", true},
		{"/translations", true}, {"/translations/zh-CN", true}, {"/translations/zh-CN/title", true},
		{"/translations/zh-CN/summary", false}, {"/translations/zh-CN/aliases", false},
		{"/attributes/language", false}, {"/pictures", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			op := CommitOperation{Target: "entity", Action: "update", ID: uuid.NewString(), BaseVersion: 1, Patch: []CommitPatch{patchValue(tc.path, "value")}}
			in := CatalogCommit{ID: uuid.NewString(), DefinitionsETag: "etag", EditNote: "review", Sources: commitFixtureSources(), Operations: []CommitOperation{op}}
			if got := commitNeedsIdentityReview(op); got != tc.need {
				t.Fatalf("needs review=%v want=%v", got, tc.need)
			}
			err := validateCommit(in)
			if tc.need && (err == nil || err.Error() != "identity_review_required") {
				t.Fatalf("missing review: %v", err)
			}
			if !tc.need && err != nil {
				t.Fatal(err)
			}
			in.Operations[0].ReviewedCandidateIDs = []string{}
			err = validateCommit(in)
			if tc.need && err != nil {
				t.Fatal(err)
			}
			if !tc.need && (err == nil || err.Error() != "invalid_identity_review") {
				t.Fatalf("unexpected review: %v", err)
			}
		})
	}
	id := uuid.NewString()
	op := CommitOperation{Target: "entity", Action: "update", ID: uuid.NewString(), BaseVersion: 1, Patch: []CommitPatch{patchValue("/title", "value")}}
	in := CatalogCommit{ID: uuid.NewString(), DefinitionsETag: "etag", EditNote: "review", Sources: commitFixtureSources(), Operations: []CommitOperation{op}}
	for _, ids := range [][]string{{"bad"}, {id, id}, {"@local:"}} {
		in.Operations[0].ReviewedCandidateIDs = ids
		if err := validateCommit(in); err == nil || err.Error() != "invalid_identity_review" {
			t.Fatalf("accepted invalid review %v: %v", ids, err)
		}
	}
}

func TestPostgresCommitIdentityUpdatesAndCanonicalSelf(t *testing.T) {
	for _, tc := range []struct {
		name  string
		patch CommitPatch
	}{
		{"title", patchValue("/title", "Taken identity")},
		{"localized title", patchValue("/translations/en/title", "Taken identity")},
		{"localized row", patchValue("/translations/en", Translation{Title: "Taken identity"})},
		{"external ID", patchValue("/external_ids/wikidata", "Q9001")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			taken := f.save(Entity{Kind: "work", Title: "Taken identity", ExternalIDs: map[string]string{"wikidata": "Q9001"}})
			own := f.save(Entity{Kind: "work", Title: "Own identity"})
			in := fixtureCommit(f, updateCommit(own, tc.patch))
			for _, preview := range []bool{true, false} {
				_, err := f.s.PushCommit(ctx, in, f.u, preview)
				var identity *commitIdentityError
				if !errors.As(err, &identity) || !sameIDSet(identity.CandidateIDs, []string{taken.ID}) {
					t.Fatalf("identity conflict: %v %+v", err, identity)
				}
			}
			cur, err := f.s.Get(ctx, own.ID, &f.u)
			if err != nil || cur.Version != own.Version || cur.Title != own.Title {
				t.Fatalf("conflict wrote entity: %+v %v", cur, err)
			}
			if _, err = f.s.CommitReceipt(ctx, in.ID, f.u); err == nil {
				t.Fatal("rejected write persisted a receipt")
			}
			in.Operations[0].ReviewedCandidateIDs = []string{taken.ID}
			if _, err = f.s.PushCommit(ctx, in, f.u, false); err != nil {
				t.Fatalf("reviewed identity: %v", err)
			}
		})
	}
	t.Run("canonical aliases of the edited entity", func(t *testing.T) {
		f := newFixture(t)
		ctx := context.Background()
		alias := f.save(Entity{Kind: "work", Title: "Historical identity"})
		own := f.save(Entity{Kind: "work", Title: "Current identity"})
		batch2Merge(t, f, alias, own.ID)
		own, err := f.s.Get(ctx, own.ID, &f.u)
		if err != nil {
			t.Fatal(err)
		}
		in := fixtureCommit(f, updateCommit(own, patchValue("/title", "Historical identity")))
		if _, err = f.s.PushCommit(ctx, in, f.u, false); err != nil {
			t.Fatalf("own canonical alias must not require a candidate review: %v", err)
		}
	})
}

func TestPostgresIdentityCreateUpdateRace(t *testing.T) {
	for _, otherAPI := range []bool{false, true} {
		for turn := 0; turn < 3; turn++ {
			f := newFixture(t)
			ctx := context.Background()
			own := f.save(Entity{Kind: "work", Title: "Before rename", Status: "draft"})
			update := fixtureCommit(f, updateCommit(own, patchValue("/title", "Shared identity")))
			create := fixtureCommit(f, CommitOperation{Target: "entity", Action: "create", Ref: "work", Document: json.RawMessage(`{"kind":"work","title":"Shared identity"}`)})
			start := make(chan struct{})
			errs := make(chan error, 2)
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				_, err := f.s.PushCommit(ctx, create, f.u, false)
				errs <- err
			}()
			go func() {
				defer wg.Done()
				<-start
				var err error
				if otherAPI {
					own.Title = "Shared identity"
					_, err = f.s.Save(ctx, Edit{Entity: own, ExpectedVersion: own.Version, EditNote: "public rename", Sources: fixtureSources(), identityReview: true}, f.u)
				} else {
					_, err = f.s.PushCommit(ctx, update, f.u, false)
				}
				errs <- err
			}()
			close(start)
			wg.Wait()
			close(errs)
			success, conflict := 0, 0
			for err := range errs {
				if err == nil {
					success++
					continue
				}
				var identity *commitIdentityError
				if !errors.As(err, &identity) {
					t.Fatalf("other API=%v unexpected race error: %v", otherAPI, err)
				}
				conflict++
			}
			if success != 1 || conflict != 1 {
				t.Fatalf("other API=%v success=%d conflict=%d", otherAPI, success, conflict)
			}
		}
	}
}

func TestPostgresIdentityUpdateUpdateRace(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.save(Entity{Kind: "work", Title: "First original", Status: "draft"})
	second := f.save(Entity{Kind: "work", Title: "Second original", Status: "draft"})
	commits := []CatalogCommit{
		fixtureCommit(f, updateCommit(first, patchValue("/title", "Shared renamed identity"))),
		fixtureCommit(f, updateCommit(second, patchValue("/title", "Shared renamed identity"))),
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, in := range commits {
		wg.Add(1)
		go func(in CatalogCommit) {
			defer wg.Done()
			<-start
			_, err := f.s.PushCommit(ctx, in, f.u, false)
			errs <- err
		}(in)
	}
	close(start)
	wg.Wait()
	close(errs)
	success, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			success++
			continue
		}
		var identity *commitIdentityError
		if !errors.As(err, &identity) {
			t.Fatal(err)
		}
		conflicts++
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("duplicate renamed identities: successes=%d conflicts=%d", success, conflicts)
	}
}

func TestPostgresImporterSharesIdentityTermLocks(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e := Entity{Kind: "work", Title: "Locked importer identity", Status: "draft"}
	holder, err := f.s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Rollback() })
	keys := map[string]bool{}
	if err = addIdentityWriteKeys(ctx, holder, e, keys); err != nil {
		t.Fatal(err)
	}
	if err = lockIdentityWriteKeys(ctx, holder, keys); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.s.importerSave(ctx, e, f.u, "import identity lock", fixtureSources())
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		if err = f.s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
            WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%pg_advisory_xact_lock%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("internal importer bypassed held identity lock: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !waiting {
		t.Fatal("importer did not wait for the shared identity term lock")
	}
	if err = holder.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("importer did not resume after the identity lock was released")
	}
}

func TestPostgresPublicEntityIdentityReviewAndReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newFixture(t)
	taken := f.save(Entity{Kind: "work", Title: "Taken public identity", Status: "draft"})
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("catalog_user", &f.u); c.Next() })
	HTTP{Store: f.s}.Register(r)
	request := Edit{Entity: Entity{Kind: "work", Title: taken.Title}, EditNote: "public identity", Sources: fixtureSources()}
	call := func(method, path string, body Edit, key string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(encode(body)))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		r.ServeHTTP(w, req)
		return w
	}
	if w := call(http.MethodPost, "/api/catalog/entities", request, ""); w.Code != 409 || !strings.Contains(w.Body.String(), taken.ID) {
		t.Fatalf("public create bypassed identity review: %d %s", w.Code, w.Body.String())
	}
	request.ReviewedCandidateIDs = []string{taken.ID}
	key := uuid.NewString()
	w := call(http.MethodPost, "/api/catalog/entities", request, key)
	if w.Code != 200 {
		t.Fatalf("reviewed public create: %d %s", w.Code, w.Body.String())
	}
	if replay := call(http.MethodPost, "/api/catalog/entities", request, key); replay.Code != 200 || replay.Body.String() != w.Body.String() {
		t.Fatalf("public replay rechecked its own created identity: %d %s", replay.Code, replay.Body.String())
	}
	var created Entity
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	own := f.save(Entity{Kind: "work", Title: "Before public rename", Status: "draft"})
	own.Title = taken.Title
	put := Edit{Entity: own, ExpectedVersion: own.Version, EditNote: "rename", Sources: fixtureSources()}
	if w := call(http.MethodPut, "/api/catalog/entities/"+own.ID, put, ""); w.Code != 409 || !strings.Contains(w.Body.String(), "identity_candidates_changed") {
		t.Fatalf("public update bypassed identity review: %d %s", w.Code, w.Body.String())
	}
	put.ReviewedCandidateIDs = []string{taken.ID, created.ID}
	if w := call(http.MethodPut, "/api/catalog/entities/"+own.ID, put, ""); w.Code != 200 {
		t.Fatalf("public update rejected reviewed candidates: %d %s", w.Code, w.Body.String())
	}
}
