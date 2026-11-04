package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestEntityBatchValidationAndListIDsFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	HTTP{Store: &Store{}}.Register(r)

	many := make([]string, 101)
	for i := range many {
		many[i] = `"` + uuid.NewString() + `"`
	}
	for _, tc := range []struct {
		payload string
		code    string
	}{
		{`{"ids":[]}`, "ids_required"},
		{`{"ids":["bad-uuid"]}`, "invalid_id"},
		{`{"ids":[` + strings.Join(many, ",") + `]}`, "too_many_ids"},
		{`{"ids":[],"extra":true}`, "invalid_payload"},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/catalog/entities/batch", strings.NewReader(tc.payload))
		req.RemoteAddr = "198.51.100.44:12345"
		r.ServeHTTP(w, req)
		if w.Code != 400 || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("payload %s: got %d %s, want 400 %s", tc.payload, w.Code, w.Body.String(), tc.code)
		}
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/catalog/entities?ids=not-a-uuid", nil)
	req.RemoteAddr = "198.51.100.44:12345"
	r.ServeHTTP(w, req)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_id") {
		t.Fatalf("GET /entities?ids=not-a-uuid: got %d %s", w.Code, w.Body.String())
	}

	idA, idB := uuid.NewString(), uuid.NewString()
	args := []any{}
	ctx := withListFilterDefinitions(context.Background(), Defaults())
	opts := ListOptions{IDs: []string{idB, idA}}
	parts, err := listFilter(ctx, &Store{}, opts, nil, &args)
	if err != nil || !strings.Contains(strings.Join(parts, " AND "), "id = ANY(") {
		t.Fatalf("listFilter IDs predicate missing: parts=%v err=%v", parts, err)
	}
	orderClause, err := entityListOrderClause(opts, &args)
	if err != nil || !strings.Contains(orderClause, "array_position") {
		t.Fatalf("entityListOrderClause should preserve requested IDs order by default: %q err=%v", orderClause, err)
	}
}

func TestPostgresEntityBatchHydratesStructureAndCovers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "batch work"})
	exp := f.save(Entity{Kind: "expression", WorkID: work.ID, Title: "batch expression"})
	release := f.save(Entity{
		Kind:     "release",
		Title:    "batch release",
		Pictures: PicturesJSON{{URL: "https://cdn.example.com/cover.jpg"}},
		Subjects: []Subject{{WorkID: work.ID, Role: "primary"}},
	})
	medium := f.save(Entity{Kind: "medium", ReleaseID: release.ID, Title: "disc 1"})
	track := f.save(Entity{Kind: "track", MediumID: medium.ID, Title: "track 1", Contents: []Inclusion{{ExpressionID: exp.ID, Position: 0}}})
	missing := uuid.NewString()

	res, err := f.s.EntityBatch(ctx, EntityBatchRequest{IDs: []string{work.ID, release.ID, track.ID, missing, work.ID}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 || len(res.Missing) != 1 || res.Missing[0] != missing {
		t.Fatalf("unexpected batch items/missing: %+v", res)
	}
	if len(res.Items[release.ID].Subjects) != 1 || res.Items[release.ID].Subjects[0].WorkID != work.ID {
		t.Fatalf("release subjects not hydrated: %+v", res.Items[release.ID])
	}
	if res.Items[track.ID].MediumID != medium.ID || len(res.Items[track.ID].Contents) != 1 || res.Items[track.ID].Contents[0].ExpressionID != exp.ID {
		t.Fatalf("track structure not hydrated: %+v", res.Items[track.ID])
	}
	if res.Covers[work.ID].URL != "https://cdn.example.com/cover.jpg" {
		t.Fatalf("inherited display cover missing for work: %+v", res.Covers)
	}
}
