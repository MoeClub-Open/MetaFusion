package catalog

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

func nestedRelationshipQueryDefinitions(d Definitions) Definitions {
	ref := Field{Names: names4("引用", "引用", "参照", "Reference"), Type: "entity", Kinds: []string{"agent"}, Enabled: true}
	d.Fields["query_casts"] = Field{Names: names4("人员", "人員", "人物", "Cast"), Type: "list", Enabled: true,
		Items: &Field{Names: names4("记录", "記錄", "レコード", "Record"), Type: "group", Enabled: true, Fields: map[string]Field{
			"performer": ref,
			"role":      {Names: names4("角色", "角色", "役", "Role"), Type: "text", Enabled: true},
			"detail":    {Names: names4("细节", "細節", "詳細", "Detail"), Type: "group", Enabled: true, Fields: map[string]Field{"context": ref}},
		}}}
	d.Relations["query_nested"] = RelationDefinition{Names: names4("嵌套引用", "巢狀引用", "入れ子参照", "Nested references"), ReverseNames: names4("引用来源", "引用來源", "参照元", "Referenced by"), SourceKinds: []string{"agent"}, TargetKinds: []string{"work"}, Fields: []string{"query_casts"}, ParticipantSlot: "peer", Enabled: true}
	return d
}

func TestNestedRelationshipReadRules(t *testing.T) {
	d := nestedRelationshipQueryDefinitions(Defaults())
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"attribute:publisher", "attribute:attachments[].content", "attribute:store_bonuses[].store"} {
		if err := validateRelationshipQueryRules(RelationshipQueryRequest{RuleCodes: []string{code}}, d); err != nil {
			t.Fatalf("published attribute reference %s missing from read registry: %v", code, err)
		}
	}
	if err := validateRelationshipQueryRules(RelationshipQueryRequest{RuleCodes: []string{"attribute:query_casts[].performer"}}, d); err == nil {
		t.Fatal("relation-only field must not become an entity attribute edge")
	}
}

func TestPostgresNestedRelationReferenceNeighbors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.publish(nestedRelationshipQueryDefinitions(v.Document), v.ETag)
	source := f.save(Entity{Kind: "agent", Title: "source"})
	target := f.save(Entity{Kind: "work", Title: "target"})
	ref := f.save(Entity{Kind: "agent", Title: "referenced character"})
	other := f.save(Entity{Kind: "agent", Title: "other referenced character"})
	hidden := f.save(Entity{Kind: "agent", Title: "hidden reference"})
	save := func(position int, casts []any) Relation {
		t.Helper()
		r, err := f.s.SaveRelation(ctx, RelationEdit{Relation: Relation{Type: "query_nested", SourceID: source.ID, TargetID: target.ID, Position: position, Attributes: map[string]any{"query_casts": casts}}, EditNote: "nested reference fixture", Sources: fixtureSources()}, f.u)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	save(0, []any{map[string]any{"performer": ref.ID}, map[string]any{"performer": hidden.ID}})
	visible := save(1, []any{map[string]any{"performer": ref.ID}, map[string]any{"performer": other.ID, "detail": map[string]any{"context": ref.ID}}})
	save(2, []any{map[string]any{"role": ref.ID}}) // A UUID-looking text is not a reference.
	hideQueryEntity(t, f, hidden.ID)
	in := RelationshipQueryRequest{IDs: []string{ref.ID}, Direction: "incoming", RuleCodes: []string{"relation:query_nested"}, PeerKinds: []string{"work"}, Limit: queryInt(1)}
	out, err := f.s.QueryRelationships(ctx, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Pages) != 1 || len(out.Pages[0].Items) != 1 || out.Pages[0].HasMore {
		t.Fatalf("nested reference visibility before pagination: %+v", out)
	}
	link := out.Pages[0].Items[0]
	if link.Key != visible.ID || link.Direction != "incoming" || !reflect.DeepEqual(link.Via, []string{"query_casts[0].performer", "query_casts[1].detail.context"}) || len(link.References) != 3 {
		t.Fatalf("reference field paths or canonical relation identity missing: %+v", link)
	}
	for _, id := range []string{source.ID, target.ID, ref.ID, other.ID} {
		if out.Entities[id].ID != id {
			t.Fatalf("reference/endpoint summary missing: %s %+v", id, out.Entities)
		}
	}
	if strings.Contains(encode(out), hidden.ID) || len(out.Entities) != 4 {
		t.Fatalf("hidden reference leaked: %s", encode(out))
	}
	in.Direction = "outgoing"
	out, err = f.s.QueryRelationships(ctx, in, nil)
	if err != nil || len(out.Pages[0].Items) != 0 {
		t.Fatalf("third participant is an incoming reference: %+v err=%v", out, err)
	}
	page, err := f.s.EntityLinks(ctx, ref.ID, 100, 0, nil)
	if err != nil || len(page.Items) != 1 || len(page.Items[0].Via) != 2 || page.Entities[other.ID].ID != other.ID {
		t.Fatalf("single-subject nested references: %+v err=%v", page, err)
	}
}

func TestPostgresEntityAttributeReferenceNeighbors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	publisher := f.save(Entity{Kind: "agent", Title: "publisher"})
	content := f.save(Entity{Kind: "work", Title: "included content"})
	hidden := f.save(Entity{Kind: "work", Title: "hidden content"})
	release := f.save(Entity{Kind: "release", Title: "attribute owner", Subjects: []Subject{{WorkID: content.ID, Role: "primary"}}, Attributes: map[string]any{
		"publisher": publisher.ID,
		"attachments": []any{
			map[string]any{"content": content.ID, "store": publisher.ID, "label": map[string]any{"en": "visible attachment"}},
			map[string]any{"content": hidden.ID, "label": map[string]any{"en": "hidden attachment"}},
		},
	}})
	hideQueryEntity(t, f, hidden.ID)
	rules := []string{"attribute:publisher", "attribute:attachments[].content", "attribute:attachments[].store"}
	out, err := f.s.QueryRelationships(ctx, RelationshipQueryRequest{IDs: []string{release.ID}, Direction: "outgoing", RuleCodes: rules}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Pages[0].Items) != 3 || len(out.Entities) != 3 || strings.Contains(encode(out), hidden.ID) {
		t.Fatalf("visible entity attribute projection: %+v", out)
	}
	for _, link := range out.Pages[0].Items {
		if link.Class != "reference" || link.Direction != "outgoing" || link.SourceID != release.ID || link.Field == "" || len(link.References) != 1 {
			t.Fatalf("attribute edge metadata: %+v", link)
		}
	}
	out, err = f.s.QueryRelationships(ctx, RelationshipQueryRequest{IDs: []string{content.ID, publisher.ID}, Direction: "incoming", RuleCodes: rules, PeerKinds: []string{"release"}, Limit: queryInt(1)}, nil)
	if err != nil || len(out.Pages) != 2 || len(out.Pages[0].Items) != 1 || out.Pages[0].Items[0].Field != "attachments[0].content" || !out.Pages[1].HasMore {
		t.Fatalf("incoming attribute paths and per-subject pages: %+v err=%v", out, err)
	}
	out, err = f.s.QueryRelationships(ctx, RelationshipQueryRequest{IDs: []string{publisher.ID}, Direction: "incoming", RuleCodes: rules, Offset: queryInt(1), Limit: queryInt(1)}, nil)
	if err != nil || len(out.Pages[0].Items) != 1 || out.Pages[0].HasMore {
		t.Fatalf("second incoming attribute page: %+v err=%v", out, err)
	}
	hideQueryEntity(t, f, release.ID)
	out, err = f.s.QueryRelationships(ctx, RelationshipQueryRequest{IDs: []string{content.ID}, Direction: "incoming", RuleCodes: rules}, nil)
	if err != nil || len(out.Pages[0].Items) != 0 || strings.Contains(encode(out), release.ID) {
		t.Fatalf("hidden reference owner leaked: %+v err=%v", out, err)
	}
}

func TestPostgresNestedEntityAttributeReferenceNeighbors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := names4("嵌套字段", "巢狀欄位", "入れ子フィールド", "Nested field")
	v.Document.Fields["query_attribute"] = Field{Names: names, Type: "group", ApplicableKinds: []string{"work"}, Enabled: true, Fields: map[string]Field{
		"record": {Names: names, Type: "list", Enabled: true, Items: &Field{Names: names, Type: "group", Enabled: true, Fields: map[string]Field{
			"members": {Names: names, Type: "list", Enabled: true, Items: &Field{Names: names, Type: "entity", Kinds: []string{"agent"}, Enabled: true}},
		}}},
	}}
	f.publish(v.Document, v.ETag)
	ref := f.save(Entity{Kind: "agent", Title: "nested reference"})
	hidden := f.save(Entity{Kind: "agent", Title: "hidden nested reference"})
	owner := f.save(Entity{Kind: "work", Title: "nested owner", Attributes: map[string]any{
		"query_attribute": map[string]any{"record": []any{map[string]any{"members": []any{ref.ID, hidden.ID, ref.ID}}}},
	}})
	hideQueryEntity(t, f, hidden.ID)
	rule := "attribute:query_attribute.record[].members[]"
	in := RelationshipQueryRequest{IDs: []string{owner.ID}, Direction: "outgoing", RuleCodes: []string{rule}}
	out, err := f.s.QueryRelationships(ctx, in, nil)
	if err != nil || len(out.Pages[0].Items) != 2 || out.Pages[0].Items[0].Key == out.Pages[0].Items[1].Key || strings.Contains(encode(out), hidden.ID) {
		t.Fatalf("nested list references must retain each record path: %+v err=%v", out, err)
	}
	if out.Pages[0].Items[0].Field != "query_attribute.record[0].members[0]" || out.Pages[0].Items[1].Field != "query_attribute.record[0].members[2]" {
		t.Fatalf("nested list ordinals missing: %+v", out.Pages[0].Items)
	}
	in.IDs, in.Direction = []string{ref.ID}, "incoming"
	out, err = f.s.QueryRelationships(ctx, in, nil)
	if err != nil || len(out.Pages[0].Items) != 2 || len(out.Entities) != 2 || out.Entities[owner.ID].ID != owner.ID {
		t.Fatalf("nested reverse lookup: %+v err=%v", out, err)
	}
	if _, err = f.s.DB.Exec(`UPDATE catalog.entities SET document=jsonb_set(document,'{attributes,query_attribute,record}','{}'::jsonb) WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	in.IDs, in.Direction = []string{owner.ID}, "outgoing"
	out, err = f.s.QueryRelationships(ctx, in, nil)
	if err != nil || len(out.Pages[0].Items) != 0 {
		t.Fatalf("malformed historical list must not trigger a SQL cast/array error: %+v err=%v", out, err)
	}
}

func TestPostgresRelationshipDeepPagination(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "large connection degree"})
	// Bulk fixture creation still uses real PostgreSQL constraints and the
	// canonical entity/structural tables; avoid 10,102 irrelevant audited writes.
	_, err := f.s.DB.Exec(`WITH inserted AS (
 INSERT INTO catalog.entities(id,kind,version,title,state,created_by,document)
 SELECT ('00000000-0000-4000-8000-'||lpad(n::text,12,'0'))::uuid,'expression',1,'expression '||n,'active',$2::uuid,
 jsonb_build_object('id','00000000-0000-4000-8000-'||lpad(n::text,12,'0'),'kind','expression','version',1,'title','expression '||n,'position',n,'state','active','original_language','en','translations',jsonb_build_object('en',jsonb_build_object('title','expression '||n)))
 FROM generate_series(1,10102) n RETURNING id)
 INSERT INTO catalog.expressions(id,work_id) SELECT id,$1::uuid FROM inserted`, work.ID, f.u.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := RelationshipQueryRequest{IDs: []string{work.ID}, RuleCodes: []string{"structure:work_expression"}, Direction: "outgoing", Offset: queryInt(10000), Limit: queryInt(100)}
	out, err := f.s.QueryRelationships(ctx, in, nil)
	if err != nil || len(out.Pages[0].Items) != 100 || !out.Pages[0].HasMore || out.Pages[0].Items[0].Position != 10001 {
		t.Fatalf("deep first page: %+v err=%v", out, err)
	}
	in.Offset = queryInt(10100)
	out, err = f.s.QueryRelationships(ctx, in, nil)
	if err != nil || len(out.Pages[0].Items) != 2 || out.Pages[0].HasMore || out.Pages[0].Items[0].Position != 10101 {
		t.Fatalf("connections beyond 10,100 must remain queryable: %+v err=%v", out, err)
	}
}

func TestPostgresRecordAttributeReferenceNeighbors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.publish(relationshipQueryFixtureDefinitions(v.Document), v.ETag)
	ref := f.save(Entity{Kind: "agent", Title: "record reference"})
	work := f.save(Entity{Kind: "work", Title: "work"})
	expression := f.save(Entity{Kind: "expression", Title: "expression", WorkID: work.ID})
	release := f.save(Entity{Kind: "release", Title: "release", Subjects: []Subject{{WorkID: work.ID, Role: "primary", Attributes: map[string]any{"query_reference": ref.ID}}}})
	medium := f.save(Entity{Kind: "medium", Title: "medium", ReleaseID: release.ID})
	track := f.save(Entity{Kind: "track", Title: "track", MediumID: medium.ID, Contents: []Inclusion{{ExpressionID: expression.ID, Locator: Locator{"relative_to": "track", "query_reference": ref.ID}}}})
	rules := []string{"structure:release_subject", "structure:track_content"}
	out, err := f.s.QueryRelationships(ctx, RelationshipQueryRequest{IDs: []string{ref.ID}, Direction: "incoming", RuleCodes: rules}, nil)
	if err != nil || len(out.Pages[0].Items) != 2 {
		t.Fatalf("record references must be queryable from their third participant: %+v err=%v", out, err)
	}
	for _, link := range out.Pages[0].Items {
		want := []string{"attributes.query_reference"}
		if link.RuleCode == "structure:track_content" {
			want = []string{"locator.query_reference"}
		}
		if !reflect.DeepEqual(link.Via, want) || len(link.References) != 1 || link.References[0].EntityID != ref.ID {
			t.Fatalf("record reference path missing: %+v", link)
		}
	}
	for _, id := range []string{ref.ID, work.ID, expression.ID, release.ID, track.ID} {
		if out.Entities[id].ID != id {
			t.Fatalf("record endpoint/reference summary missing: %s", id)
		}
	}
	hideQueryEntity(t, f, release.ID)
	out, err = f.s.QueryRelationships(ctx, RelationshipQueryRequest{IDs: []string{ref.ID}, Direction: "incoming", RuleCodes: rules}, nil)
	if err != nil || len(out.Pages[0].Items) != 1 || out.Pages[0].Items[0].SourceID != track.ID || strings.Contains(encode(out), release.ID) {
		t.Fatalf("hidden record owner leaked: %+v err=%v", out, err)
	}
}

type relationshipReferenceCountingQuery struct {
	queryer
	loads int
}

func (q *relationshipReferenceCountingQuery) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.HasPrefix(query, "SELECT id::text,kind,state,created_by::text FROM catalog.entities WHERE id=ANY") {
		q.loads++
	}
	return q.queryer.QueryContext(ctx, query, args...)
}

func TestPostgresRelationshipReferenceLoadsAreBatched(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.publish(nestedRelationshipQueryDefinitions(v.Document), v.ETag)
	source := f.save(Entity{Kind: "agent", Title: "source"})
	target := f.save(Entity{Kind: "work", Title: "target"})
	ref := f.save(Entity{Kind: "agent", Title: "shared referenced character"})
	_, err = f.s.DB.Exec(`INSERT INTO catalog.relations(id,source_id,target_id,type,version,document)
 SELECT gen_random_uuid(),$1::uuid,$2::uuid,'query_nested',1,
 jsonb_build_object('type','query_nested','source_id',$1::text,'target_id',$2::text,'position',n,'attributes',jsonb_build_object('query_casts',jsonb_build_array(jsonb_build_object('performer',$3::text,'role',n::text))))
 FROM generate_series(1,225) n`, source.ID, target.ID, ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	q := &relationshipReferenceCountingQuery{queryer: tx}
	in, limit, offset, err := normalizeRelationshipQuery(RelationshipQueryRequest{IDs: []string{source.ID}, Direction: "outgoing", RuleCodes: []string{"relation:query_nested"}, Offset: queryInt(220)})
	if err != nil {
		t.Fatal(err)
	}
	out, err := queryRelationshipsFrom(ctx, q, in, limit, offset, nil)
	if err != nil || len(out.Pages[0].Items) != 5 || q.loads != 1 {
		t.Fatalf("225 repeated references across windows must load once: loads=%d out=%+v err=%v", q.loads, out, err)
	}
}
