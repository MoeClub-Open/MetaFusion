package catalog

import (
	"context"
	"sync"
	"testing"
)

// 请求摘要稳定性（纯逻辑）：同一逻辑载荷（map 构造顺序不同）哈希一致，
// 载荷变化哈希即变——同键不同载荷的 409 判定就靠它。
func TestRequestHashStable(t *testing.T) {
	a := Edit{Entity: Entity{Kind: "work", Title: "哈希", Attributes: map[string]any{"language": "ja", "tags": []any{"x"}}}}
	b := Edit{Entity: Entity{Kind: "work", Title: "哈希", Attributes: map[string]any{"tags": []any{"x"}, "language": "ja"}}}
	if requestHash(a) != requestHash(b) {
		t.Fatal("同一逻辑载荷的摘要应稳定")
	}
	b.Entity.Title = "哈希改"
	if requestHash(a) == requestHash(b) {
		t.Fatal("载荷变化摘要必须变化")
	}
	// 未导出的幂等声明不进摘要：声明本身不改变"请求内容"。
	a.idempotency = &IdempotencyClaim{Key: "test-only"}
	if requestHash(a) != requestHash(Edit{Entity: a.Entity}) {
		t.Fatal("未导出字段不得影响摘要")
	}
}

// R1 真库：同键同载荷重放同实体、同键不同载荷 409、不同键新建、重启（新 Store 句柄，
// 不读任何进程内存）同结果、并发同键只建一个。无 MF_V2_TEST_DSN 时跳过。
func TestPostgresIdempotentEntityCreate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	mkEdit := func(title string) Edit {
		return Edit{Entity: Entity{Kind: "work", Title: title, Status: "active"}, EditNote: "r1", Sources: fixtureSources()}
	}
	withKey := func(e Edit, key string) Edit {
		e.idempotency = &IdempotencyClaim{Operation: IdempotencyOpEntityCreate, UserID: f.u.ID, Key: key, Hash: requestHash(e)}
		return e
	}
	countTitle := func(title string) int {
		t.Helper()
		var n int
		if err := f.s.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.entities WHERE title=$1`, title).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	first, err := f.s.Save(ctx, withKey(mkEdit("幂等作品"), "k1"), f.u)
	if err != nil {
		t.Fatal(err)
	}
	// 同键同载荷：重放同一实体，不新增行。
	second, err := f.s.Save(ctx, withKey(mkEdit("幂等作品"), "k1"), f.u)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("重放应返回同一实体：%s vs %s", second.ID, first.ID)
	}
	if n := countTitle("幂等作品"); n != 1 {
		t.Fatalf("重放不得新增行，实际 %d", n)
	}
	// 同键不同载荷：409 语义冲突，不返回旧结果。
	if _, err := f.s.Save(ctx, withKey(mkEdit("幂等作品改"), "k1"), f.u); err == nil || err.Error() != "idempotency_conflict" {
		t.Fatalf("同键不同载荷应报 idempotency_conflict，实际 %v", err)
	}
	// 不同键：新建。
	other, err := f.s.Save(ctx, withKey(mkEdit("幂等作品"), "k2"), f.u)
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == first.ID {
		t.Fatal("不同键应新建实体")
	}
	// 重启语义：全新 Store 句柄（不共享任何进程内存状态，全部状态在 PG）同键重放同结果。
	restarted := &Store{DB: f.s.DB}
	replay, err := restarted.Save(ctx, withKey(mkEdit("幂等作品"), "k1"), f.u)
	if err != nil {
		t.Fatal(err)
	}
	if replay.ID != first.ID {
		t.Fatalf("重启后重放应返回同一实体：%s vs %s", replay.ID, first.ID)
	}
	// 并发同键：只建一个，全部拿到同一 ID。
	const workers = 8
	ids := make([]string, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := f.s.Save(ctx, withKey(mkEdit("并发幂等"), "k-concurrent"), f.u)
			if err == nil {
				ids[i] = out.ID
			}
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发同键 worker %d 失败：%v", i, err)
		}
		if ids[i] != ids[0] {
			t.Fatalf("并发同键应返回同一 ID：%s vs %s", ids[i], ids[0])
		}
	}
	if n := countTitle("并发幂等"); n != 1 {
		t.Fatalf("并发同键只应建一个实体，实际 %d", n)
	}
}

// R1 关系路径：同键重放同一条边，同键换属性即冲突。无 MF_V2_TEST_DSN 时跳过。
func TestPostgresIdempotentRelationCreate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.save(Entity{Kind: "agent", Title: "幂等甲"})
	b := f.save(Entity{Kind: "agent", Title: "幂等乙"})
	mkEdit := func(role string) RelationEdit {
		return RelationEdit{
			Relation: Relation{Type: "member_of", SourceID: a.ID, TargetID: b.ID, Attributes: map[string]any{"credit_role": role}},
			EditNote: "r1", Sources: fixtureSources(),
		}
	}
	withKey := func(e RelationEdit, key string) RelationEdit {
		e.idempotency = &IdempotencyClaim{Operation: IdempotencyOpRelationCreate, UserID: f.u.ID, Key: key, Hash: requestHash(e)}
		return e
	}
	first, err := f.s.SaveRelation(ctx, withKey(mkEdit("Vo."), "rk1"), f.u)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.s.SaveRelation(ctx, withKey(mkEdit("Vo."), "rk1"), f.u)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("关系重放应返回同一条边：%s vs %s", second.ID, first.ID)
	}
	if _, err := f.s.SaveRelation(ctx, withKey(mkEdit("Gt."), "rk1"), f.u); err == nil || err.Error() != "idempotency_conflict" {
		t.Fatalf("关系同键不同载荷应报 idempotency_conflict，实际 %v", err)
	}
	var n int
	if err := f.s.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.relations WHERE type='member_of' AND source_id=$1 AND target_id=$2`, a.ID, b.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("重放不得新增边，实际 %d", n)
	}
}
