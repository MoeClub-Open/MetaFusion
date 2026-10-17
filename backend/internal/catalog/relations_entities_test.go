package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// relListResponse 关系列表响应的契约形状：items 是边、entities 是端点/主体摘要表、
// subject_id 显式标出被查询实体自己。
type relListResponse struct {
	Items     []EntityLink               `json:"items"`
	Entities  map[string]json.RawMessage `json:"entities"`
	SubjectID string                     `json:"subject_id"`
}

func decodeRelList(t *testing.T, w *httptest.ResponseRecorder) relListResponse {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("GET relations = %d: %s", w.Code, w.Body.String())
	}
	var out relListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析关系响应失败：%v (%s)", err, w.Body.String())
	}
	return out
}

func entityKeys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// assertRelationEntitiesCoverage 断言映射覆盖**本页返回的每条关系的两端**且含 subject_id 指向的主体。
// 它只依赖 items 里真实返回的边，因此分页/截断（limit）后同样成立：
// "某端不在本页"不是缺席的理由，只要边在响应里，两端就得在映射里。
func assertRelationEntitiesCoverage(t *testing.T, res relListResponse, wantSubject string) {
	t.Helper()
	if res.SubjectID != wantSubject {
		t.Errorf("subject_id=%q，期望被查询实体自身 %q", res.SubjectID, wantSubject)
	}
	if _, ok := res.Entities[res.SubjectID]; !ok {
		t.Errorf("entities 缺少被查询实体自身 %s（现有键：%v）", res.SubjectID, sortedEntityIDs(res))
	}
	for _, it := range res.Items {
		for _, end := range []struct{ role, id string }{{"source_id", it.SourceID}, {"target_id", it.TargetID}} {
			if _, ok := res.Entities[end.id]; !ok {
				t.Errorf("关系 %s(%s) 的 %s=%s 不在 entities 映射里（现有键：%v）",
					it.RuleCode, it.Key, end.role, end.id, sortedEntityIDs(res))
			}
		}
	}
}

func sortedEntityIDs(res relListResponse) []string {
	ids := make([]string, 0, len(res.Entities))
	for id := range res.Entities {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// TestPostgresRelationListEntitiesCoverBothEnds 锁住 GET /entities/:id/links 的实体映射契约：
// entities 覆盖每条关系的两端并含被查询实体自身，subject_id 标出主体，主体摘要与其它条目同形状。
// 旧实现只解析"另一端"——主体自己整段缺席（调用方按 entities[it.source_id] 渲染时主体恒为 ?），
// 属性引用边（via）两端都不是本实体、旧实现只补了其中一端。
func TestPostgresRelationListEntitiesCoverBothEnds(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	subject := f.save(Entity{Kind: "agent", Title: "关系主体角色"})
	work := f.save(Entity{Kind: "work", Title: "主体登场作品"})
	actor := f.save(Entity{Kind: "agent", Title: "某声优"})
	group := f.save(Entity{Kind: "agent", Title: "所属团体"})
	peer := f.save(Entity{Kind: "agent", Title: "另一团体"})
	relate := func(r Relation) {
		t.Helper()
		if _, err := f.s.SaveRelation(ctx, RelationEdit{Relation: r, ExpectedVersion: 0,
			EditNote: "关系响应端点覆盖用例", Sources: fixtureSources()}, f.u); err != nil {
			t.Fatalf("建立关系失败：%v", err)
		}
	}
	// 三种端点关系各一条：主体是 source、主体是 target、主体既不是端点也不是对端
	// （只作为 voiced_by.character 属性的取值，读路径用 via 标出）。
	relate(Relation{Type: "member_of", SourceID: subject.ID, TargetID: group.ID})
	relate(Relation{Type: "member_of", SourceID: peer.ID, TargetID: subject.ID})
	relate(Relation{Type: "voiced_by", SourceID: work.ID, TargetID: actor.ID, Attributes: map[string]any{"character": subject.ID}})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	HTTP{Store: f.s}.Register(r)
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	res := decodeRelList(t, get("/api/catalog/entities/"+subject.ID+"/links"))
	if len(res.Items) != 3 {
		t.Fatalf("应返回 3 条关系（source/target/via 各一），实际 %d 条：%+v", len(res.Items), res.Items)
	}
	assertRelationEntitiesCoverage(t, res, subject.ID)

	// 主体摘要必须与其它条目同形状，且字段值取自库里那一行。
	selfRaw, ok := res.Entities[subject.ID]
	if !ok {
		t.Fatalf("entities 缺少被查询实体自身 %s（现有键：%v）", subject.ID, sortedEntityIDs(res))
	}
	var self Entity
	if err := json.Unmarshal(selfRaw, &self); err != nil {
		t.Fatal(err)
	}
	if self.ID != subject.ID || self.Kind != "agent" || self.Title != subject.Title || self.State != "active" {
		t.Errorf("主体摘要与库内实体不一致：%+v", self)
	}
	// 同 kind 的两条摘要（主体与另一 agent 对端）键集必须一致：形状随 kind 走，不因"是自己"缩水。
	peerID := ""
	for _, it := range res.Items {
		if it.RuleCode == "relation:member_of" && it.SourceID != subject.ID {
			peerID = it.SourceID
		}
	}
	if peerID == "" {
		t.Fatal("用例前置条件失效：没有主体作为 target 的关系")
	}
	if got, want := entityKeys(t, selfRaw), entityKeys(t, res.Entities[peerID]); len(got) != len(want) {
		t.Errorf("主体摘要形状与同 kind 对端不一致：self=%v peer=%v", got, want)
	} else {
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("主体摘要形状与同 kind 对端不一致：self=%v peer=%v", got, want)
				break
			}
		}
	}
	// via 边两端都不是本实体，两端都得补上（旧实现只补 source，target 缺席）。
	var via EntityLink
	for _, it := range res.Items {
		if len(it.Via) > 0 {
			via = it
		}
	}
	if via.Key == "" {
		t.Fatal("用例前置条件失效：没有读到属性引用边（via）")
	}
	for _, end := range []string{work.ID, actor.ID} {
		if _, ok := res.Entities[end]; !ok {
			t.Errorf("via 关系 %s 的端点 %s 缺失", via.Key, end)
		}
	}

	// 只被实体型属性引用、自己不是任何一条边端点的实体（角色侧看"谁为它配音"）：
	// 它出现在映射里的唯一通道就是"补主体"——只解析端点（含旧实现"只补另一端"）必定缺席。
	charOnly := f.save(Entity{Kind: "agent", Title: "只被属性引用的角色"})
	relate(Relation{Type: "voiced_by", SourceID: work.ID, TargetID: peer.ID, Attributes: map[string]any{"character": charOnly.ID}})
	charRes := decodeRelList(t, get("/api/catalog/entities/"+charOnly.ID+"/links"))
	if len(charRes.Items) != 1 || strings.Join(charRes.Items[0].Via, ",") != "character" {
		t.Fatalf("用例前置条件失效：属性引用主体应读到 1 条 via=character 的边，实际 %+v", charRes.Items)
	}
	assertRelationEntitiesCoverage(t, charRes, charOnly.ID)
	for _, end := range []string{work.ID, peer.ID, charOnly.ID} {
		if _, ok := charRes.Entities[end]; !ok {
			t.Errorf("属性引用主体的响应缺少 %s（现有键：%v）", end, sortedEntityIDs(charRes))
		}
	}

	// 分页/截断生效时（limit）：映射仍覆盖本页返回的每一条关系的两端 + 主体。
	limited := decodeRelList(t, get("/api/catalog/entities/"+subject.ID+"/links?limit=1"))
	if len(limited.Items) == 0 {
		t.Fatal("limit=1 返回了空列表")
	}
	if len(limited.Items) != 1 {
		t.Fatalf("limit=1 must return one link, got %d", len(limited.Items))
	}
	assertRelationEntitiesCoverage(t, limited, subject.ID)
}

func TestPostgresRelationAttributeQueryHidesDraftEndpoint(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	character := f.save(Entity{Kind: "agent", Title: "公开角色"})
	work := f.save(Entity{Kind: "work", Title: "公开作品"})
	publicActor := f.save(Entity{Kind: "agent", Title: "公开演员"})
	draftActor := f.save(Entity{Kind: "agent", Title: "未公开演员", State: "active"})
	for _, actor := range []Entity{publicActor, draftActor} {
		if _, err := f.s.SaveRelation(ctx, RelationEdit{
			Relation: Relation{Type: "voiced_by", SourceID: work.ID, TargetID: actor.ID,
				Attributes: map[string]any{"character": character.ID}},
			EditNote: "关系可见性用例", Sources: fixtureSources(),
		}, f.u); err != nil {
			t.Fatal(err)
		}
	}

	ownerRelations, err := f.s.Relations(ctx, character.ID, &f.u)
	if err != nil || len(ownerRelations) != 2 {
		t.Fatalf("有权限的编辑者应看到两条关系：count=%d err=%v", len(ownerRelations), err)
	}
	draftActor = f.retire(draftActor)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	HTTP{Store: f.s}.Register(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/catalog/entities/"+character.ID+"/links", nil))
	res := decodeRelList(t, w)
	if len(res.Items) != 1 || res.Items[0].TargetID != publicActor.ID || strings.Join(res.Items[0].Via, ",") != "character" {
		t.Fatalf("匿名关系列表只应包含公开端点：%+v", res.Items)
	}
	assertRelationEntitiesCoverage(t, res, character.ID)
	if strings.Contains(w.Body.String(), draftActor.ID) {
		t.Fatal("匿名关系响应泄露了草稿端点 ID")
	}
}
