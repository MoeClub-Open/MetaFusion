package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/testutil"
)

type fixture struct {
	t *testing.T
	s *Store
	u User
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	s := &Store{DB: testutil.Database(t)}
	if err := s.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 夹具用户不落库：账号归账号服务，目录表没有指向 auth 的外键，见 users_test.go。
	return fixture{t, s, fixtureUser("admin")}
}
func (f fixture) save(e Entity) Entity {
	f.t.Helper()
	if e.State == "" {
		e.State = "active"
	}
	// 发布态要求至少一条翻译（真实契约）：在这里补齐，免得每个调用点各自记这件事。
	if e.State == "active" && len(e.Translations) == 0 {
		e.Translations = map[string]Translation{"en": {Title: e.Title}}
	}
	// Match HTTP decoding for dynamic JSON values.
	var copy Entity
	if err := json.Unmarshal([]byte(encode(e)), &copy); err != nil {
		f.t.Fatal(err)
	}
	out, err := f.s.Save(context.Background(), Edit{Entity: copy, ExpectedVersion: e.Version, EditNote: "acceptance fixture", Sources: fixtureSources()}, f.u)
	if err != nil {
		f.t.Fatalf("save %s: %v", e.Title, err)
	}
	return out
}
func fixtureSources() []Source {
	return []Source{{Kind: "url", Citation: "isolated acceptance fixture", URL: "https://example.org/fixture"}}
}
func (f fixture) publish(d Definitions, base string) {
	f.t.Helper()
	if _, err := f.s.SaveDefinitions(context.Background(), d, base, f.u, "configure fixture", fixtureSources()); err != nil {
		f.t.Fatal(err)
	}
}

func TestPostgresDynamicDefinitions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v, _ := f.s.Definitions(ctx)
	d := v.Document
	d.Vocabularies["custom_format"] = Vocabulary{Names: names("新增介质", "New formats"), Terms: map[string]Term{"cassette": {Names: names("磁带", "Cassette"), Enabled: true}}}
	d.Fields["custom_format"] = Field{Names: names("新增格式", "Custom format"), Type: "enum", Vocabulary: "custom_format", ApplicableKinds: []string{"work"}, Enabled: true, Searchable: true, Comparable: true}
	d.Relations["edited_by"] = RelationDefinition{Names: names("编辑", "Edited by"), ReverseNames: names("编辑了", "Editor of"), SourceKinds: []string{"work"}, TargetKinds: []string{"agent"}, Fields: []string{"context", "character", "language"}, ParticipantSlot: "person", CountsAsCredit: true, Enabled: true}
	f.publish(d, v.ETag)
	w := f.save(Entity{Kind: "work", Title: "个人影像集", Attributes: map[string]any{"custom_format": "cassette", "language": "ja"}})
	if items, err := f.s.List(ctx, ListOptions{Field: "custom_format", Value: "cassette"}, nil); err != nil || len(items) != 1 {
		t.Fatalf("dynamic filter: %v %d", err, len(items))
	}
	actor := f.save(Entity{Kind: "agent", Title: "演职人员"})
	for _, character := range []string{"角色一", "角色二"} {
		c := f.save(Entity{Kind: "agent", Title: character})
		_, err := f.s.SaveRelation(ctx, RelationEdit{Relation: Relation{Type: "edited_by", SourceID: w.ID, TargetID: actor.ID, Attributes: map[string]any{"character": c.ID, "language": "zh-CN", "context": w.ID}}, EditNote: "role fixture", Sources: fixtureSources()}, f.u)
		if err != nil {
			t.Fatal(err)
		}
	}
	if relations, err := f.s.Relations(ctx, w.ID, nil); err != nil || len(relations) != 2 {
		t.Fatalf("multiple roles: %v %d", err, len(relations))
	}
	v, _ = f.s.Definitions(ctx)
	d = v.Document
	term := d.Vocabularies["custom_format"].Terms["cassette"]
	term.Enabled = false
	d.Vocabularies["custom_format"].Terms["cassette"] = term
	field := d.Fields["custom_format"]
	field.Enabled = false
	d.Fields["custom_format"] = field
	f.publish(d, v.ETag)
	w.Attributes["language"] = "en-US"
	w = f.save(w)
	if w.Attributes["custom_format"] != "cassette" {
		t.Fatal("retired term lost during unrelated edit")
	}
	if _, err := f.s.Save(ctx, Edit{Entity: Entity{Kind: "work", Title: "invalid reuse", State: "active", Attributes: map[string]any{"custom_format": "cassette"}}, Sources: fixtureSources(), EditNote: "invalid"}, f.u); err == nil {
		t.Fatal("retired field reused")
	}
	v, _ = f.s.Definitions(ctx)
	delete(v.Document.Fields, "custom_format")
	if _, err := f.s.SaveDefinitions(ctx, v.Document, v.ETag, f.u, "invalid publish", fixtureSources()); err == nil {
		t.Fatal("referenced field deleted")
	}
}

func TestPostgresLegacyPublicationStateRejected(t *testing.T) {
	f := newFixture(t)
	for _, state := range []string{"draft", "pending_review", "published"} {
		_, err := f.s.Save(context.Background(), Edit{Entity: Entity{Kind: "work", Title: "retired state", State: state}, EditNote: "fixture", Sources: fixtureSources()}, f.u)
		if err == nil {
			t.Fatalf("legacy publication state %s accepted", state)
		}
	}
}

func (f fixture) retire(e Entity) Entity {
	f.t.Helper()
	out, err := f.s.Lifecycle(context.Background(), e.ID, LifecycleEdit{ExpectedVersion: e.Version, EditNote: "retire visibility fixture", Sources: fixtureSources()}, f.u)
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func TestPostgresReleaseComparisonAndReuse(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	song := f.save(Entity{Kind: "work", Title: "Song"})
	album := f.save(Entity{Kind: "work", Title: "Album"})
	mv := f.save(Entity{Kind: "work", Title: "Music video"})
	x := f.save(Entity{Kind: "expression", Title: "Recording", WorkID: song.ID})
	mvx := f.save(Entity{Kind: "expression", Title: "Video", WorkID: mv.ID})
	ids := []string{}
	for i, name := range []string{"Standard", "Limited", "Regional"} {
		r := f.save(Entity{Kind: "release", Title: name, Subjects: []Subject{{WorkID: album.ID, Role: "primary"}, {WorkID: song.ID, Role: "compilation", Position: 1}, {WorkID: mv.ID, Role: "supplement", Position: 2}}, Attributes: map[string]any{"packaging": "box", "attachments": []any{map[string]any{"label": map[string]any{"en-US": "Booklet", "zh-CN": "小册子"}}}, "store_bonuses": []any{map[string]any{"label": map[string]any{"en-US": "Shop card", "zh-CN": "店铺卡片"}}}}})
		ids = append(ids, r.ID)
		m := f.save(Entity{Kind: "medium", Title: "CD", ReleaseID: r.ID, Attributes: map[string]any{"format": "cd"}})
		f.save(Entity{Kind: "track", Title: "Song", MediumID: m.ID, Number: "A1", Contents: []Inclusion{{ExpressionID: x.ID}}})
		if i == 1 {
			bd := f.save(Entity{Kind: "medium", Title: "Bonus BD", ReleaseID: r.ID, Attributes: map[string]any{"format": "bd", "role": "supplement"}})
			f.save(Entity{Kind: "track", Title: "MV", MediumID: bd.ID, Contents: []Inclusion{{ExpressionID: mvx.ID}}})
		}
		if i == 2 {
			for j := 1; j <= 100; j++ {
				f.save(Entity{Kind: "track", Title: fmt.Sprintf("Chapter %d", j), MediumID: m.ID, Position: j})
			}
		}
	}
	occ, err := f.s.Occurrences(ctx, x.ID, nil)
	if err != nil || len(occ) != 3 {
		t.Fatalf("reuse: %v %d", err, len(occ))
	}
	comparison, err := f.s.Compare(ctx, ids, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(comparison[1]["children"].([]map[string]any)) != 2 {
		t.Fatal("CD and BD not preserved")
	}
	last := comparison[2]["children"].([]map[string]any)[0]["tracks"].([]Entity)
	if len(last) != 101 {
		t.Fatalf("directory truncated: %d", len(last))
	}
}

// 账号与收藏路由已随子系统拆分离开目录服务：这里只剩目录自己的契约。
// 网关按前缀把它们分流到账号/互动服务，因此目录侧必须**不再注册**这些路由
// （否则网关配置失误时会出现两个实现同时在线的假象）。
func TestPostgresAccountRoutesAreGone(t *testing.T) {
	f := newFixture(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	HTTP{Store: f.s}.Register(r)
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		return res
	}

	// 已迁出的前缀在目录服务上必须 404。
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/auth/login", `{"username":"x","password":"y"}`},
		{http.MethodGet, "/api/auth/me", ""},
		{http.MethodPost, "/api/setup", `{"username":"x"}`},
		{http.MethodGet, "/api/admin/users", ""},
		{http.MethodGet, "/api/oidc/jwks", ""},
		{http.MethodPost, "/api/favorites/toggle", `{"target_type":"work"}`},
	} {
		if res := request(tc.method, tc.path, tc.body, ""); res.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d, want 404（该前缀已归子系统）", tc.method, tc.path, res.Code)
		}
	}

	// 目录自己的写接口仍然要求身份，且身份只来自账号服务签发的 RS256 令牌。
	key := testKey(t)
	f.s.Verifier = testVerifier(t, key)
	if res := request(http.MethodPost, "/api/catalog/workspaces", `{"name":"Anonymous copy"}`, ""); res.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous workspace = %d, want 401", res.Code)
	}
	editToken := signTestToken(t, key, func(c Claims) Claims {
		c.Permissions = []string{PermissionEntityEdit, PermissionReviewBypass}
		return c
	})
	call := func(method, path string, input, output any) {
		t.Helper()
		res := request(method, path, encode(input), editToken)
		if res.Code != http.StatusOK {
			t.Fatalf("%s %s = %d: %s", method, path, res.Code, res.Body.String())
		}
		if output != nil {
			if err := json.Unmarshal(res.Body.Bytes(), output); err != nil {
				t.Fatal(err)
			}
		}
	}
	var workspace Workspace
	call(http.MethodPost, "/api/catalog/workspaces", WorkspaceCreate{Name: "Verified working copy"}, &workspace)
	var saved WorkspaceChangesResult
	call(http.MethodPut, "/api/catalog/workspaces/"+workspace.ID+"/changes", WorkspaceChanges{ExpectedVersion: workspace.Version, Operations: []WorkspaceOperation{{Target: "entity", Action: "create", Ref: "work", Document: json.RawMessage(`{"kind":"work","title":"Verified write"}`)}}}, &saved)
	var commit CollaborationCommit
	call(http.MethodPost, "/api/catalog/workspaces/"+workspace.ID+"/commits", WorkspaceCommitCreate{ID: uuid.NewString(), ExpectedVersion: saved.Workspace.Version, Message: "Verified catalog contribution"}, &commit)
	call(http.MethodGet, "/api/catalog/workspaces/"+workspace.ID, nil, &workspace)
	var pushed WorkspacePushResult
	call(http.MethodPost, "/api/catalog/workspaces/"+workspace.ID+"/push", WorkspacePush{ExpectedVersion: workspace.Version, ExpectedHead: commit.ID}, &pushed)
	if !pushed.Applied {
		t.Fatal("authorized push did not apply")
	}
	if res := request(http.MethodPost, "/api/catalog/workspaces", `{"kind":"work","title":"Bare entity"}`, editToken); res.Code != http.StatusBadRequest {
		t.Fatalf("bare entity payload = %d, want 400", res.Code)
	}

}
