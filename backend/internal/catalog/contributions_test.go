package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestContributionCalendarUsesWholeUTCYear(t *testing.T) {
	for _, tc := range []struct{ year, days int }{{2024, 366}, {2026, 365}} {
		days := contributionCalendar(tc.year, map[string]int64{"2024-02-29": 3, "2026-01-01": 2})
		if len(days) != tc.days || days[0].Date != time.Date(tc.year, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02") || days[len(days)-1].Date[5:] != "12-31" {
			t.Fatalf("calendar %d: first=%+v last=%+v days=%d", tc.year, days[0], days[len(days)-1], len(days))
		}
		if tc.year == 2024 && (days[59].Date != "2024-02-29" || days[59].Count != 3) {
			t.Fatalf("leap day lost: %+v", days[59])
		}
		if days[1].Count != 0 {
			t.Fatalf("days without merged activity must be explicit zeroes: %+v", days[1])
		}
	}
	if year, err := contributionYear(0); err != nil || year != time.Now().UTC().Year() {
		t.Fatalf("default UTC year: %d %v", year, err)
	}
	for _, year := range []int{1969, 10000, -1} {
		if _, err := contributionYear(year); err == nil {
			t.Fatalf("invalid year accepted: %d", year)
		}
	}
}

func TestContributionPageClamping(t *testing.T) {
	for _, tc := range []struct{ page, size, wantPage, wantSize int }{
		{0, 0, 1, 20}, {-3, -1, 1, 20}, {2, 50, 2, 50}, {5, 101, 5, 20},
	} {
		if page, size := contributionPage(tc.page, tc.size); page != tc.wantPage || size != tc.wantSize {
			t.Fatalf("page(%d,%d)=(%d,%d), want (%d,%d)", tc.page, tc.size, page, size, tc.wantPage, tc.wantSize)
		}
	}
	if got := contributionOffset(int(^uint(0)>>1), 100); got < 0 {
		t.Fatalf("a very large page produced a negative SQL offset: %d", got)
	}
}

func TestUserContributionsRejectsBadParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		path, code string
		status     int
	}{
		{"/api/users/not-a-uuid/contributions", "not_found", http.StatusNotFound},
		{"/api/users/11111111-1111-1111-1111-111111111111/contributions?year=no", "invalid_year", http.StatusBadRequest},
		{"/api/users/11111111-1111-1111-1111-111111111111/contributions?year=1969", "invalid_year", http.StatusBadRequest},
		{"/api/users/11111111-1111-1111-1111-111111111111/contributions?year=0", "invalid_year", http.StatusBadRequest},
	} {
		w := httptest.NewRecorder()
		gateEngine(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	if _, err := (&Store{}).UserContributions(context.Background(), "not-a-uuid", 2026, 1, 20); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("invalid user ID should be not found: %v", err)
	}
}

// The fixture stores immutable commit objects and associates them with retained
// main-catalog revisions. Several original authors can point at the same merged
// revision; that association, rather than the revision's single actor, is credit.
func TestPostgresUserContributionsMergedActivityAndPrivacy(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	author, coauthor := fixtureUser("editor"), fixtureUser("editor")
	work := f.save(Entity{Kind: "work", Title: "Public work"})
	release := f.save(Entity{Kind: "release", Title: "Public release"})
	agent := f.save(Entity{Kind: "agent", Title: "Public composer"})
	hidden := f.save(Entity{Kind: "work", Title: "Secret removed object"})
	legacy := f.save(Entity{Kind: "collection", Title: "Historical collection"})
	relation, err := f.s.SaveRelation(ctx, RelationEdit{Relation: Relation{Type: "composed_by", SourceID: work.ID, TargetID: agent.ID}, EditNote: "relation fixture"}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	type object struct {
		target, id string
		base       int64
		before     any
		after      any
		associate  bool
	}
	entityObject := func(e Entity, base int64) object {
		var before any
		if base > 0 {
			before = map[string]any{"kind": e.Kind, "title": "Before"}
		}
		return object{"entity", e.ID, base, before, e, true}
	}
	commit := func(u User, message, authored, merged string, objects ...object) string {
		t.Helper()
		id := uuid.NewString()
		var mergedAt any
		if merged != "" {
			mergedAt = merged
		}
		if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.commits(id,actor_id,request_hash,request,result,author_snapshot,message,created_at,merged_at,merged_by)
            VALUES($1,$2,'fixture','{}','{}',$3,$4,$5,$6,$7)`, id, u.ID,
			encode(map[string]any{"username": u.Username, "permissions": []string{"private-permission-snapshot"}}), message, authored, mergedAt, f.u.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.commit_authors(commit_id,user_id,name) VALUES($1,$2,$3)`, id, u.ID, u.Username); err != nil {
			t.Fatal(err)
		}
		for _, o := range objects {
			var before, after any
			if o.before != nil {
				before = encode(o.before)
			}
			if o.after != nil {
				after = encode(o.after)
			}
			if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.commit_objects(commit_id,target,target_id,base_version,before_document,after_document) VALUES($1,$2,$3,$4,$5,$6)`, id, o.target, o.id, o.base, before, after); err != nil {
				t.Fatal(err)
			}
			if o.associate {
				if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.revision_commits(revision_id,commit_id) SELECT id,$2 FROM catalog.revisions WHERE target_id=$1 ORDER BY id LIMIT 1`, o.id, id); err != nil {
					t.Fatal(err)
				}
			}
		}
		return id
	}
	first := commit(author, "Create public objects", "2025-12-31T23:00:00Z", "2026-01-01T00:30:00Z", entityObject(work, 0), entityObject(release, 0))
	coauthored := commit(coauthor, "Coauthor contribution", "2025-12-31T23:30:00Z", "2026-01-01T00:30:00Z", entityObject(work, 1))
	if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.commit_authors(commit_id,user_id,name) VALUES($1,$2,$3)`, first, coauthor.ID, coauthor.Username); err != nil {
		t.Fatal(err)
	}
	relationCommit := commit(author, "Add composer relationship", "2026-01-01T18:00:00-05:00", "2026-01-01T19:15:00-05:00", object{"relation", relation.ID, 0, nil, relation, true})
	commit(author, "Secret private fork", "2026-01-02T00:00:00Z", "", entityObject(work, 1))
	noRevision := entityObject(work, 1)
	noRevision.associate = false
	commit(author, "No actual main revision", "2026-01-02T00:00:00Z", "2026-01-02T01:00:00Z", noRevision)
	commit(author, "Secret removed object only", "2026-01-02T00:00:00Z", "2026-01-02T01:00:00Z", entityObject(hidden, 0))
	partial := commit(author, "Secret removed object in mixed commit", "2026-01-03T00:00:00Z", "2026-01-03T01:00:00Z", entityObject(work, 1), entityObject(hidden, 0))
	// A hidden no-op is still private metadata of this commit, even though it
	// must not add another changed object or contribution day.
	if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.commit_objects(commit_id,target,target_id,base_version,before_document,after_document) VALUES($1,'entity',$2,1,$3,$3)`, partial, agent.ID, encode(agent)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DB.ExecContext(ctx, `UPDATE catalog.revisions SET actor_id=$2,actor_name='Historical author',edit_note='Historical edit',created_at='2026-01-01T01:00:00Z' WHERE target_id=$1`, legacy.ID, author.ID); err != nil {
		t.Fatal(err)
	}
	workspaceID, prID := uuid.NewString(), uuid.NewString()
	if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.workspaces(id,owner_id,name) VALUES($1,$2,'Merged collaboration')`, workspaceID, author.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.pull_requests(id,workspace_id,author_id,title,status,source_head_id,merged_at,merged_by) VALUES($1,$2,$3,'Merged work','merged',$4,'2026-01-02T00:15:00Z',$5)`, prID, workspaceID, author.ID, relationCommit, f.u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DB.ExecContext(ctx, `UPDATE catalog.commits SET pull_request_id=$1 WHERE id IN ($2,$3,$4)`, prID, first, relationCommit, coauthored); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Lifecycle(ctx, hidden.ID, LifecycleEdit{ExpectedVersion: hidden.Version, EditNote: "remove fixture"}, f.u); err != nil {
		t.Fatal(err)
	}
	read := func(userID string, year, page, size int) UserContributions {
		t.Helper()
		out, err := f.s.UserContributions(ctx, userID, year, page, size)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := read(author.ID, 2026, 1, 20)
	want := UserContributionStats{MergedCommits: 3, ChangedEntities: 3, CreatedEntities: 2, MergedPullRequests: 1, LegacyRevisions: 1}
	if out.Summary != want || out.Total != 4 || len(out.Items) != 4 || out.Timezone != "UTC" || len(out.Calendar) != 365 {
		t.Fatalf("merged contribution projection: summary=%+v total=%d items=%+v", out.Summary, out.Total, out.Items)
	}
	if out.Calendar[0].Count != 1 || out.Calendar[1].Count != 1 || out.Calendar[2].Count != 1 {
		t.Fatalf("calendar must use UTC merge days, one count per commit: %+v", out.Calendar[:3])
	}
	var foundRelation, foundPartial bool
	for _, item := range out.Items {
		if !item.Legacy && (item.Author.ID != author.ID || item.Author.Name != author.Username || item.MergedBy == nil || item.MergedBy.ID != f.u.ID) {
			t.Fatalf("author and merger attribution changed: %+v", item)
		}
		if item.ID == relationCommit {
			foundRelation = true
			if item.ChangesCount != 1 || len(item.Entities) != 2 || item.PullRequestID != prID {
				t.Fatalf("relation endpoints or PR attribution missing: %+v", item)
			}
		}
		if item.ID == partial {
			foundPartial = true
			if item.CommitID != "" || item.PullRequestID != "" || item.Message != "" || item.ChangesCount != 1 || len(item.Entities) != 1 || item.Entities[0].ID != work.ID {
				t.Fatalf("partially hidden commit leaked metadata: %+v", item)
			}
		}
	}
	if !foundRelation || !foundPartial {
		t.Fatalf("relation/mixed contribution missing: %+v", out.Items)
	}
	raw, _ := json.Marshal(out)
	for _, secret := range []string{"Secret", "private-permission-snapshot", "No actual main revision"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("private activity leaked %q: %s", secret, raw)
		}
	}
	other := read(coauthor.ID, 2026, 1, 20)
	if other.Summary.MergedCommits != 2 || other.Summary.LegacyRevisions != 0 || len(other.Items) != 2 {
		t.Fatalf("coauthor lost attribution on shared revision: %+v", other)
	}
	for _, item := range other.Items {
		if item.CommitID == first && (item.Author.ID != author.ID || len(item.Authors) != 2) {
			t.Fatalf("shared staged authors replaced original committer: %+v", item)
		}
		if item.CommitID == coauthored && (item.Author.ID != coauthor.ID || len(item.Authors) != 1) {
			t.Fatalf("original author lost on shared revision: %+v", item)
		}
	}
	if prior := read(author.ID, 2025, 1, 20); prior.Total != 0 || len(prior.Items) != 0 {
		t.Fatalf("authored year must not be mistaken for merged year: %+v", prior)
	}
	if last := read(author.ID, 2026, 2, 3); last.Total != 4 || len(last.Items) != 1 || last.Summary != want {
		t.Fatalf("pagination changed annual summary or total: %+v", last)
	}
	if empty := read(fixtureUser("member").ID, 2026, 1, 20); empty.Total != 0 || len(empty.Items) != 0 || len(empty.Calendar) != 365 {
		t.Fatalf("empty profile: %+v", empty)
	}
	if huge := read(author.ID, 2026, int(^uint(0)>>1), 100); huge.Total != 4 || len(huge.Items) != 0 {
		t.Fatalf("oversized page must be an empty page, not an overflowing offset: %+v", huge)
	}
	// PR visibility includes other authors' commit objects, including no-ops.
	// The visible commit's own link remains usable, but its PR must not become
	// an unavailable link or reveal another contributor's hidden object.
	if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.commit_objects(commit_id,target,target_id,base_version,before_document,after_document) VALUES($1,'entity',$2,1,$3,$3)`, coauthored, hidden.ID, encode(hidden)); err != nil {
		t.Fatal(err)
	}
	masked := read(author.ID, 2026, 1, 20)
	if masked.Summary.MergedCommits != 3 || masked.Summary.MergedPullRequests != 0 || masked.Total != 4 {
		t.Fatalf("another author's hidden PR object changed contribution credit: %+v", masked)
	}
	for _, item := range masked.Items {
		if item.PullRequestID != "" {
			t.Fatalf("PR with a hidden object was linked: %+v", item)
		}
		if item.ID == first && (item.CommitID != first || item.Message != "Create public objects") {
			t.Fatalf("public commit lost its independent visible projection: %+v", item)
		}
	}
	for _, item := range read(coauthor.ID, 2026, 1, 20).Items {
		if item.ID == coauthored && (item.CommitID != "" || item.Message != "") {
			t.Fatalf("hidden no-op object leaked commit metadata: %+v", item)
		}
	}
}

func TestPostgresUserContributionsLegacyTargetsAndMalformedSnapshots(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	author := fixtureUser("editor")
	work := f.save(Entity{Kind: "work", Title: "Historical public work"})
	agent := f.save(Entity{Kind: "agent", Title: "Historical public participant"})
	hidden := f.save(Entity{Kind: "work", Title: "Removed historical work"})
	if _, err := f.s.Lifecycle(ctx, hidden.ID, LifecycleEdit{ExpectedVersion: hidden.Version, EditNote: "remove fixture"}, f.u); err != nil {
		t.Fatal(err)
	}
	endpoints := map[string]any{"source_id": work.ID, "target_id": agent.ID}
	for _, tc := range []struct {
		target   string
		note     string
		snapshot map[string]any
	}{
		{work.ID, "Visible entity with malformed historical endpoints", map[string]any{"source_id": "invalid", "target_id": "invalid"}},
		{"historical-relation:not-a-uuid", "Historical textual relation ID", endpoints},
		{uuid.NewString(), "Historical missing relation UUID", endpoints},
		// An existing hidden entity must not be reinterpreted as a relation,
		// even when its retained snapshot happens to contain public endpoints.
		{hidden.ID, "Hidden entity with public endpoints", endpoints},
		{"malformed-source", "Malformed source ID", map[string]any{"source_id": "not-a-uuid", "target_id": agent.ID}},
		{"malformed-target", "Malformed target ID", map[string]any{"source_id": work.ID, "target_id": "00000000-0000-0000-0000-00000000000z"}},
		{"hidden-endpoint", "Hidden relation endpoint", map[string]any{"source_id": work.ID, "target_id": hidden.ID}},
	} {
		if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.revisions(target_id,version,actor_id,actor_name,edit_note,sources,snapshot,created_at)
			VALUES($1,1,$2,'Historical author',$3,'[]',$4,'2026-01-01T00:00:00Z')`, tc.target, author.ID, tc.note, encode(tc.snapshot)); err != nil {
			t.Fatal(err)
		}
	}
	out, err := f.s.UserContributions(ctx, author.ID, 2026, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 3 || out.Summary.LegacyRevisions != 3 || out.Summary.MergedCommits != 0 || len(out.Items) != 3 {
		t.Fatalf("legacy visibility changed: %+v", out)
	}
	for _, item := range out.Items {
		if !item.Legacy || strings.Contains(item.Message, "Hidden") || strings.HasPrefix(item.Message, "Malformed") {
			t.Fatalf("invalid or hidden revision leaked: %+v", item)
		}
		wantEntities := 2
		if item.Message == "Visible entity with malformed historical endpoints" {
			wantEntities = 1
		}
		if len(item.Entities) != wantEntities {
			t.Fatalf("historical endpoint projection changed: %+v", item)
		}
	}
}
