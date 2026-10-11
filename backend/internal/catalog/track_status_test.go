package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

const trackStatusPath = "/api/catalog/tracks/00000000-0000-0000-0000-000000000001/status"

func TestTrackStatusRouteGateAndStrictBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	plain := fixtureUser("member")
	third := fixtureUser("admin")
	third.IsThirdParty = true
	valid := `{"status":"pending_review","expected_version":1,"edit_note":"review","sources":[{"kind":"url","citation":"fixture","url":"https://example.org/fixture"}]}`
	for _, tc := range []struct {
		name string
		u    *User
		body string
		code int
		err  string
	}{
		{"anonymous", nil, valid, 401, "authentication_required"},
		{"third party", &third, valid, 403, "forbidden"},
		{"contents", &plain, strings.TrimSuffix(valid, "}") + `,"contents":[]}`, 400, "invalid_payload"},
		{"title", &plain, strings.TrimSuffix(valid, "}") + `,"title":"replacement"}`, 400, "invalid_payload"},
		{"entity", &plain, strings.TrimSuffix(valid, "}") + `,"entity":{}}`, 400, "invalid_payload"},
		{"target id", &plain, strings.TrimSuffix(valid, "}") + `,"target_id":"replacement"}`, 400, "invalid_payload"},
		{"malformed", &plain, "{", 400, "invalid_payload"},
		{"extra json", &plain, valid + `{}`, 400, "invalid_payload"},
		{"missing version", &plain, strings.Replace(valid, `"expected_version":1,`, "", 1), 400, "invalid_payload"},
		{"zero version", &plain, strings.Replace(valid, `"expected_version":1`, `"expected_version":0`, 1), 400, "invalid_payload"},
		{"negative version", &plain, strings.Replace(valid, `"expected_version":1`, `"expected_version":-1`, 1), 400, "invalid_payload"},
		{"deleted", &plain, strings.Replace(valid, "pending_review", "deleted", 1), 400, "invalid_status"},
		{"merged", &plain, strings.Replace(valid, "pending_review", "merged", 1), 400, "invalid_status"},
		{"unknown state", &plain, strings.Replace(valid, "pending_review", "accepted", 1), 400, "invalid_status"},
		{"missing evidence", &plain, `{"status":"draft","expected_version":1}`, 400, "evidence_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			// No DB is configured: all these requests must stop at the input or
			// identity gate before audit-summary and authoritative reads.
			gateEngine(tc.u).ServeHTTP(w, httptest.NewRequest(http.MethodPatch, trackStatusPath, strings.NewReader(tc.body)))
			if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.err) {
				t.Fatalf("got %d %s, want %d %s", w.Code, w.Body.String(), tc.code, tc.err)
			}
		})
	}
}

func TestOpenAPITrackStatusEndpoint(t *testing.T) {
	doc := OpenAPI()
	patch := doc["paths"].(map[string]any)["/catalog/tracks/{id}/status"].(map[string]any)["patch"].(map[string]any)
	if patch["security"] == nil {
		t.Fatal("status editing must require authentication")
	}
	request := patch["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if request["$ref"] != "#/components/schemas/TrackStatusEdit" {
		t.Fatalf("request: %+v", request)
	}
	response := patch["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if response["$ref"] != "#/components/schemas/Entity" {
		t.Fatalf("response: %+v", response)
	}
	s := doc["components"].(map[string]any)["schemas"].(map[string]any)["TrackStatusEdit"].(map[string]any)
	keys := []string{"status", "expected_version", "edit_note", "sources"}
	if s["type"] != "object" || s["additionalProperties"] != false || !reflect.DeepEqual(s["required"], keys) {
		t.Fatalf("strict four-key schema: %+v", s)
	}
	props := s["properties"].(map[string]any)
	if len(props) != len(keys) {
		t.Fatalf("unexpected status properties: %+v", props)
	}
	for i, typ := range []string{"string", "integer", "string", "array"} {
		if props[keys[i]].(map[string]any)["type"] != typ {
			t.Fatalf("%s type: %+v", keys[i], props[keys[i]])
		}
	}
	if !reflect.DeepEqual(props["status"].(map[string]any)["enum"], []string{"draft", "pending_review", "published"}) ||
		props["expected_version"].(map[string]any)["minimum"] != 1 || props["edit_note"].(map[string]any)["minLength"] != 1 ||
		props["sources"].(map[string]any)["minItems"] != 1 {
		t.Fatalf("validation contract: %+v", props)
	}
}

func newStatusTrack(t *testing.T, f fixture, owner User, contents int) (Entity, []Entity) {
	t.Helper()
	w := f.save(Entity{Kind: "work", Title: "Status test work"})
	u := f.save(Entity{Kind: "content_unit", Title: "Status test unit", WorkID: w.ID})
	r := f.save(Entity{Kind: "release", Title: "Status test release", Subjects: []Subject{{WorkID: w.ID, Role: "primary"}}})
	m := f.save(Entity{Kind: "medium", Title: "Status test CD", ReleaseID: r.ID, Attributes: map[string]any{"format": "cd"}})
	var es []Entity
	var cs []Inclusion
	for i := 0; i < contents; i++ {
		e := f.save(Entity{Kind: "expression", Title: "Status test recording", WorkID: w.ID, ContentUnitID: u.ID})
		es = append(es, e)
		cs = append(cs, Inclusion{ExpressionID: e.ID, Position: i, Locator: Locator{"relative_to": "track", "chapter": "unchanged locator"}})
	}
	track := (fixture{t, f.s, owner}).save(Entity{Kind: "track", Title: "  Preserve title verbatim  ", Status: "draft", MediumID: m.ID,
		Translations: map[string]Translation{"en": {Title: "Status test track"}}, Contents: cs})
	return track, es
}

func statusInput(e Entity, status string) TrackStatusEdit {
	return TrackStatusEdit{Status: status, ExpectedVersion: e.Version, EditNote: "status only", Sources: fixtureSources()}
}

// Include physical row identities: deleting/reinserting an identical inclusion
// is still a regression for a status-only edit, even if its JSON looks equal.
func statusUnchangedFacts(t *testing.T, s *Store, id string) string {
	t.Helper()
	var facts string
	err := s.DB.QueryRow(`SELECT jsonb_build_object(
		'entity', (to_jsonb(e)-'status'-'version'-'updated_at'-'document') || jsonb_build_object('document',e.document-'status'-'version'-'updated_at'),
		'track', (SELECT to_jsonb(tr) || jsonb_build_object('xmin',tr.xmin::text,'ctid',tr.ctid::text) FROM catalog.tracks tr WHERE tr.id=e.id),
		'contents', (SELECT COALESCE(jsonb_agg(to_jsonb(c) || jsonb_build_object('xmin',c.xmin::text,'ctid',c.ctid::text) ORDER BY c.position),'[]'::jsonb)
			FROM catalog.track_contents c WHERE c.track_id=e.id))::text FROM catalog.entities e WHERE e.id=$1`, id).Scan(&facts)
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func statusCounts(t *testing.T, s *Store, id string) [2]int {
	t.Helper()
	var counts [2]int
	if err := s.DB.QueryRow(`SELECT (SELECT count(*) FROM catalog.revisions WHERE target_id=$1),
		(SELECT count(*) FROM catalog.outbox WHERE entity_id=$1)`, id).Scan(&counts[0], &counts[1]); err != nil {
		t.Fatal(err)
	}
	return counts
}

func statusRequest(s *Store, u *User, id string, in TrackStatusEdit) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	unpublishEngine(s, u).ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/api/catalog/tracks/"+id+"/status", strings.NewReader(encode(in))))
	return w
}

func TestTrackStatusPreservesHiddenFactsOnPostgres(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := fixtureUser("editor")
	track, expressions := newStatusTrack(t, f, owner, 2)
	if _, err := f.s.Unpublish(ctx, expressions[1].ID, UnpublishEdit{ExpectedVersion: expressions[1].Version, EditNote: "hide recording", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatal(err)
	}
	// A status edit must preserve even opaque document keys outside Entity's
	// current Go projection and the exact persisted title formatting.
	if _, err := f.s.DB.Exec(`UPDATE catalog.entities SET document=document || '{"opaque":{"keep":[1,null,"a"]},"title":"  exact title  "}'::jsonb,title='  exact title  ' WHERE id=$1`, track.ID); err != nil {
		t.Fatal(err)
	}
	read, err := f.s.Get(ctx, track.ID, &owner)
	if err != nil || len(read.Contents) != 1 {
		t.Fatalf("fixture projection: %+v, %v", read, err)
	}
	before := statusUnchangedFacts(t, f.s, track.ID)
	w := statusRequest(f.s, &owner, track.ID, statusInput(track, "pending_review"))
	if w.Code != 200 {
		t.Fatalf("pending review: %d %s", w.Code, w.Body.String())
	}
	var next Entity
	if err := json.Unmarshal(w.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if next.ID != track.ID || next.Status != "pending_review" || next.Version != track.Version+1 || next.Title != "  exact title  " ||
		len(next.Contents) != 1 || next.Contents[0].ExpressionID != expressions[0].ID {
		t.Fatalf("status response or visibility changed: %+v", next)
	}
	if after := statusUnchangedFacts(t, f.s, track.ID); after != before {
		t.Fatalf("status edit changed non-status/physical facts\nbefore=%s\nafter=%s", before, after)
	}
	var consistent bool
	if err = f.s.DB.QueryRow(`SELECT status=document->>'status' AND version=(document->>'version')::bigint
		AND updated_at=(document->>'updated_at')::timestamptz AND status='pending_review' AND version=$2 FROM catalog.entities WHERE id=$1`, track.ID, next.Version).Scan(&consistent); err != nil || !consistent {
		t.Fatalf("status columns/document disagree: %v, %v", consistent, err)
	}
	var complete bool
	if err = f.s.DB.QueryRow(`SELECT r.snapshot=o.payload AND jsonb_array_length(r.snapshot->'contents')=2
		AND r.snapshot->'opaque'='{"keep":[1,null,"a"]}'::jsonb AND r.snapshot->>'medium_id'=$3
		AND r.actor_id=$4 AND r.edit_note='status only' AND r.sources=$5::jsonb AND o.type='entity.saved'
		FROM catalog.revisions r JOIN catalog.outbox o ON o.entity_id=r.target_id AND o.version=r.version
		WHERE r.target_id=$1 AND r.version=$2`, track.ID, next.Version, track.MediumID, owner.ID, encode(fixtureSources())).Scan(&complete); err != nil || !complete {
		t.Fatalf("revision/outbox must contain full current facts: %v, %v", complete, err)
	}
	if got := statusCounts(t, f.s, track.ID); got != [2]int{2, 2} {
		t.Fatalf("exactly one revision/event per edit: %v", got)
	}
	// Publishing must validate the hidden inclusion, not just the public
	// response projection or a whole-entity payload supplied by the caller.
	w = statusRequest(f.s, &owner, track.ID, statusInput(next, "published"))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_reference") {
		t.Fatalf("hidden publication bypassed Save's gate: %d %s", w.Code, w.Body.String())
	}
	stored, err := get(ctx, f.s.DB, track.ID)
	if err != nil || stored.Version != next.Version || stored.Status != next.Status || len(stored.Contents) != 2 || statusUnchangedFacts(t, f.s, track.ID) != before || statusCounts(t, f.s, track.ID) != [2]int{2, 2} {
		t.Fatalf("failed publication changed persisted facts: %+v, %v", stored, err)
	}
}

func TestTrackStatusPublicationAndHTTPFailuresOnPostgres(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := fixtureUser("editor")
	track, _ := newStatusTrack(t, f, owner, 1)
	before := statusUnchangedFacts(t, f.s, track.ID)
	w := statusRequest(f.s, &owner, track.ID, statusInput(track, "published"))
	if w.Code != 200 {
		t.Fatalf("normal publication: %d %s", w.Code, w.Body.String())
	}
	var published Entity
	if err := json.Unmarshal(w.Body.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	if published.Status != "published" || published.Version != track.Version+1 || len(published.Contents) != 1 || statusUnchangedFacts(t, f.s, track.ID) != before {
		t.Fatalf("normal publication changed facts: %+v", published)
	}
	for _, tc := range []struct {
		name string
		id   string
		in   TrackStatusEdit
		code int
		err  string
	}{
		{"stale version", track.ID, statusInput(track, "published"), 409, "version_conflict"},
		{"published demotion", track.ID, statusInput(published, "draft"), 400, "use_lifecycle_endpoint"},
		{"published review", track.ID, statusInput(published, "pending_review"), 400, "use_lifecycle_endpoint"},
		{"non track", track.MediumID, statusInput(Entity{Version: 1}, "published"), 400, "not_track"},
		{"invalid id", "not-an-id", statusInput(published, "published"), 400, "invalid_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := statusRequest(f.s, &f.u, tc.id, tc.in)
			if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.err) {
				t.Fatalf("got %d %s, want %d %s", w.Code, w.Body.String(), tc.code, tc.err)
			}
		})
	}
	stored, err := get(ctx, f.s.DB, track.ID)
	if err != nil || stored.Version != published.Version || statusUnchangedFacts(t, f.s, track.ID) != before || statusCounts(t, f.s, track.ID) != [2]int{2, 2} {
		t.Fatalf("rejected writes changed state: %+v, %v", stored, err)
	}
}

func TestTrackStatusPublicationRequirementsOnPostgres(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		err  string
	}{
		{"translations", "translation_required"},
		{"medium visibility", "invalid_reference"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			track, _ := newStatusTrack(t, f, f.u, 1)
			if tc.name == "translations" {
				track.Translations = nil
				track = f.save(track)
			} else {
				medium, err := get(ctx, f.s.DB, track.MediumID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.s.Unpublish(ctx, medium.ID, UnpublishEdit{ExpectedVersion: medium.Version, EditNote: "hide medium", Sources: fixtureSources()}, f.u); err != nil {
					t.Fatal(err)
				}
			}
			facts, counts := statusUnchangedFacts(t, f.s, track.ID), statusCounts(t, f.s, track.ID)
			w := statusRequest(f.s, &f.u, track.ID, statusInput(track, "published"))
			if w.Code != 400 || !strings.Contains(w.Body.String(), tc.err) {
				t.Fatalf("publication gate: %d %s, want %s", w.Code, w.Body.String(), tc.err)
			}
			stored, err := get(ctx, f.s.DB, track.ID)
			if err != nil || stored.Status != "draft" || stored.Version != track.Version || statusCounts(t, f.s, track.ID) != counts || statusUnchangedFacts(t, f.s, track.ID) != facts {
				t.Fatalf("rejected publication left changes: %+v, %v", stored, err)
			}
		})
	}
}

func TestTrackStatusPermissionsMatchSaveOnPostgres(t *testing.T) {
	f := newFixture(t)
	owner := fixtureUser("editor")
	track, _ := newStatusTrack(t, f, owner, 0)
	plainOwner := owner
	plainOwner.Permissions = nil
	outsider := fixtureUser("editor")
	for _, tc := range []struct {
		name   string
		u      User
		status string
		want   int
	}{
		{"unrelated editor cannot edit private track", outsider, "pending_review", 403},
		{"owner without edit cannot publish", plainOwner, "published", 403},
		{"owner without edit can request review", plainOwner, "pending_review", 200},
		{"owner editor can publish", owner, "published", 200},
		{"plain owner cannot edit published", plainOwner, "published", 403},
		{"unrelated editor can edit published", outsider, "published", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := statusRequest(f.s, &tc.u, track.ID, statusInput(track, tc.status))
			if w.Code != tc.want {
				t.Fatalf("got %d %s, want %d", w.Code, w.Body.String(), tc.want)
			}
			if tc.want == 200 {
				if err := json.Unmarshal(w.Body.Bytes(), &track); err != nil {
					t.Fatal(err)
				}
			} else if !strings.Contains(w.Body.String(), "forbidden") {
				t.Fatalf("wrong permission error: %s", w.Body.String())
			}
		})
	}
	private, _ := newStatusTrack(t, f, owner, 0)
	w := statusRequest(f.s, &f.u, private.ID, statusInput(private, "published"))
	if w.Code != 200 {
		t.Fatalf("lifecycle manager can approve another user's draft: %d %s", w.Code, w.Body.String())
	}
	if _, err := f.s.Lifecycle(context.Background(), private.ID, LifecycleEdit{ExpectedVersion: private.Version + 1, EditNote: "terminal fixture", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatal(err)
	}
	w = statusRequest(f.s, &f.u, private.ID, statusInput(Entity{Version: private.Version + 2}, "published"))
	if w.Code != 403 || !strings.Contains(w.Body.String(), "forbidden") {
		t.Fatalf("terminal entity must not be revived: %d %s", w.Code, w.Body.String())
	}
}

func TestTrackStatusRollbackAndConcurrencyOnPostgres(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	track, _ := newStatusTrack(t, f, f.u, 1)
	before := statusUnchangedFacts(t, f.s, track.ID)
	// Fail after the entity update and revision INSERT, at outbox INSERT.
	// All three writes must roll back together.
	if _, err := f.s.DB.Exec(`CREATE FUNCTION catalog.fail_status_outbox() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'test outbox failure'; END $$;
		CREATE TRIGGER fail_status_outbox BEFORE INSERT ON catalog.outbox FOR EACH ROW EXECUTE FUNCTION catalog.fail_status_outbox()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.EditTrackStatus(ctx, track.ID, statusInput(track, "published"), f.u); err == nil || !strings.Contains(err.Error(), "test outbox failure") {
		t.Fatalf("outbox failure should abort the edit: %v", err)
	}
	stored, err := get(ctx, f.s.DB, track.ID)
	if err != nil || stored.Version != track.Version || stored.Status != track.Status || statusUnchangedFacts(t, f.s, track.ID) != before || statusCounts(t, f.s, track.ID) != [2]int{1, 1} {
		t.Fatalf("failed transaction left changes: %+v, %v", stored, err)
	}
	if _, err := f.s.DB.Exec(`DROP TRIGGER fail_status_outbox ON catalog.outbox; DROP FUNCTION catalog.fail_status_outbox()`); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := f.s.EditTrackStatus(ctx, track.ID, statusInput(track, "published"), f.u)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	var saved, conflicted int
	for err := range errs {
		if err == nil {
			saved++
		} else if errors.Is(err, errVersionConflict) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if saved != 1 || conflicted != 1 || statusCounts(t, f.s, track.ID) != [2]int{2, 2} || statusUnchangedFacts(t, f.s, track.ID) != before {
		t.Fatalf("concurrent same-version edits: saved=%d conflicted=%d", saved, conflicted)
	}
}

func TestTrackStatusHTTPAuditOnPostgres(t *testing.T) {
	f := newAuditFixture(t)
	track, _ := newStatusTrack(t, fixture{t, f.s, *f.user}, *f.user, 1)
	path := "/api/catalog/tracks/" + track.ID + "/status"
	w := f.do(f.engine(f.user), http.MethodPatch, path, encode(statusInput(track, "published")), "rid-track-status-success")
	f.expect(http.StatusOK, w, "Track status")
	row := f.waitRow("rid-track-status-success")
	if row.action != "entity.status_changed" || row.result != "success" || row.status != 200 || row.targetID != track.ID || row.targetType != "entity" ||
		row.method != http.MethodPatch || row.route != "/api/catalog/tracks/:id/status" || row.username != f.user.Username {
		t.Fatalf("status audit: %+v", row)
	}
	var changes map[string]any
	if err := json.Unmarshal([]byte(row.changes), &changes); err != nil {
		t.Fatal(err)
	}
	status := changes["status"].(map[string]any)
	if status["before"] != "draft" || status["after"] != "published" || changes["contents"] != nil {
		t.Fatalf("status audit delta: %+v", changes)
	}
	w = f.do(f.engine(f.user), http.MethodPatch, path, encode(statusInput(track, "published")), "rid-track-status-conflict")
	f.expect(http.StatusConflict, w, "Track status conflict")
	row = f.waitRow("rid-track-status-conflict")
	if row.action != "entity.status_changed" || row.result != "failure" || row.status != 409 || row.errorCode != "version_conflict" || row.targetID != track.ID {
		t.Fatalf("failed status audit: %+v", row)
	}
	if got := statusCounts(t, f.s, track.ID); got != [2]int{2, 2} {
		t.Fatalf("failed audit request changed revisions/outbox: %v", got)
	}
}
