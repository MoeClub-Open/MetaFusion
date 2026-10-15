package catalog

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func batch2Import(t *testing.T, f fixture, e Entity) Entity {
	t.Helper()
	if e.State == "" {
		e.State = "draft"
	}
	out, err := f.s.Save(context.Background(), Edit{Entity: e, EditNote: "batch2 fixture", Sources: fixtureSources()}, f.u)
	if err != nil {
		t.Fatalf("Save %s: %v", e.Title, err)
	}
	return out
}

func batch2Merge(t *testing.T, f fixture, src Entity, targetID string) {
	t.Helper()
	cur, err := f.s.Get(context.Background(), src.ID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Lifecycle(context.Background(), src.ID, LifecycleEdit{
		ExpectedVersion: cur.Version, TargetID: targetID,
		EditNote: "batch2 merge", Sources: fixtureSources(),
	}, f.u); err != nil {
		t.Fatalf("merge %s: %v", src.ID, err)
	}
}

func TestPostgresMergeRewritesAttributeOnlyRefs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w := f.save(Entity{Kind: "work", Title: "作品"})
	performer := f.save(Entity{Kind: "agent", Title: "配音者"})
	charOld := f.save(Entity{Kind: "agent", Title: "旧角色"})
	charNew := f.save(Entity{Kind: "agent", Title: "存活角色"})
	rel, err := f.s.SaveRelation(ctx, RelationEdit{Relation: Relation{
		Type: "voiced_by", SourceID: w.ID, TargetID: performer.ID,
		Attributes: map[string]any{"character": charOld.ID},
	}, EditNote: "cast", Sources: fixtureSources()}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	batch2Merge(t, f, charOld, charNew.ID)
	rels, err := f.s.Relations(ctx, performer.ID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	var got *Relation
	for i := range rels {
		if rels[i].ID == rel.ID {
			got = &rels[i]
		}
	}
	if got == nil {
		t.Fatal("关系边丢失")
	}
	if got.Attributes["character"] != charNew.ID {
		t.Fatalf("character 属性未指向存活身份：%v", got.Attributes)
	}
	if got.SourceID != w.ID || got.TargetID != performer.ID {
		t.Fatalf("端点不应被改动：%+v", got)
	}
	if got.Version != 2 {
		t.Fatalf("改写必须留痕（版本+1），实际 %d", got.Version)
	}
}

// X01：A→B→C 合并链的 canonical+别名集合；读侧（Occurrences）汇到存活身份。
func TestPostgresResolveIdentityChain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.save(Entity{Kind: "work", Title: "A"})
	b := f.save(Entity{Kind: "work", Title: "B"})
	c := f.save(Entity{Kind: "work", Title: "C"})
	batch2Merge(t, f, a, b.ID)
	bCur, err := f.s.Get(ctx, b.ID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	batch2Merge(t, f, bCur, c.ID)
	res, err := f.s.ResolveIdentity(ctx, a.ID, nil)
	if err != nil || res.CanonicalID != c.ID {
		t.Fatalf("旧身份应解析到存活实体：%+v %v", res, err)
	}
	if len(res.Aliases) != 2 || res.Aliases[0] != a.ID || res.Aliases[1] != b.ID {
		t.Fatalf("别名集合应按链序：%v", res.Aliases)
	}
	// 存活身份反向枚举全部历史别名（X01）：从 C 直接查回 A/B（集合口径，排序保证确定性）。
	res, err = f.s.ResolveIdentity(ctx, c.ID, nil)
	if err != nil || res.CanonicalID != c.ID {
		t.Fatalf("存活身份应解析到自身：%+v %v", res, err)
	}
	if !sameIDSet(res.Aliases, []string{a.ID, bCur.ID}) {
		t.Fatalf("存活身份应反向枚举全部历史别名：%v", res.Aliases)
	}
	if _, err = f.s.ResolveIdentity(ctx, "00000000-0000-0000-0000-000000000000", nil); err == nil {
		t.Fatal("未知 ID 应失败")
	}
	// 读侧汇别名：旧作品的表达被收录后合并作品，旧 ID 查收录回到存活链。
	oldW := f.save(Entity{Kind: "work", Title: "旧作品"})
	newW := f.save(Entity{Kind: "work", Title: "新作品"})
	x := f.save(Entity{Kind: "expression", Title: "录音", WorkID: oldW.ID})
	r := f.save(Entity{Kind: "release", Title: "发行",
		Subjects: []Subject{{WorkID: oldW.ID, Role: "primary"}}})
	m := f.save(Entity{Kind: "medium", Title: "CD", ReleaseID: r.ID})
	f.save(Entity{Kind: "track", Title: "曲目", MediumID: m.ID,
		Contents: []Inclusion{{ExpressionID: x.ID}}})
	batch2Merge(t, f, oldW, newW.ID)
	occ, err := f.s.Occurrences(ctx, oldW.ID, nil)
	if err != nil || len(occ) != 1 {
		t.Fatalf("旧 ID 收录应汇到存活链：%d %v", len(occ), err)
	}
}

// sameIDSet 按集合比较 ID 列表（反向别名排序后确定，但不断言链序）。
func sameIDSet(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, id := range got {
		seen[id]++
	}
	for _, id := range want {
		if seen[id] == 0 {
			return false
		}
		seen[id]--
	}
	return true
}

// X01 反向解析验收：A→C、B→C 两个分支再 C→D，从 D 枚举此前全部历史且去重；
// 从分支旧 ID 出发同样收齐全集（向前链 + 反向增补合并去重）。
func TestPostgresResolveIdentityReverseBranches(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.save(Entity{Kind: "work", Title: "分支甲"})
	b := f.save(Entity{Kind: "work", Title: "分支乙"})
	c := f.save(Entity{Kind: "work", Title: "汇合"})
	d := f.save(Entity{Kind: "work", Title: "存活"})
	batch2Merge(t, f, a, c.ID)
	batch2Merge(t, f, b, c.ID)
	cCur, err := f.s.Get(ctx, c.ID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	batch2Merge(t, f, cCur, d.ID)
	want := []string{a.ID, b.ID, c.ID}
	res, err := f.s.ResolveIdentity(ctx, d.ID, nil)
	if err != nil || res.CanonicalID != d.ID {
		t.Fatalf("存活身份应解析到自身：%+v %v", res, err)
	}
	if !sameIDSet(res.Aliases, want) {
		t.Fatalf("从 D 应枚举全部历史且去重：%v want %v", res.Aliases, want)
	}
	resA, err := f.s.ResolveIdentity(ctx, a.ID, nil)
	if err != nil || resA.CanonicalID != d.ID {
		t.Fatalf("分支旧 ID 应解析到存活实体：%+v %v", resA, err)
	}
	if !sameIDSet(resA.Aliases, want) {
		t.Fatalf("从分支旧 ID 应收齐全集：%v want %v", resA.Aliases, want)
	}
}

// M01/M02 机制级受控验证：锁确实落在预期键上（同键互斥、异键并发；
// 定义共享-共享不互斥、独占-共享互斥）。端到端双写时序见下面的接线用例。
//
// 编排约束（防自阻塞）：各场景拆分、上一场景事务提交后才进下一场景，同一 goroutine 内
// 永不同时持有冲突锁；探测侧与独占持有一律 lock_timeout 有界；全部事务登记清理
// （t.Cleanup 兜底回滚未提交者，CI 里不留 idle-in-transaction）。
func TestPostgresAdvisoryLockMechanism(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r1 := "11111111-1111-4111-8111-111111111111"
	r2 := "22222222-2222-4222-8222-222222222222"
	var open []*sql.Tx
	t.Cleanup(func() {
		for _, tx := range open {
			_ = tx.Rollback() // 已提交的返回 sql.ErrTxDone，忽略
		}
	})
	mustBegin := func() *sql.Tx {
		t.Helper()
		tx, err := f.s.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		open = append(open, tx)
		return tx
	}
	withTimeout := func(tx *sql.Tx) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, "SET LOCAL lock_timeout = '1s'"); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(tx *sql.Tx) {
		t.Helper()
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	rollback := func(tx *sql.Tx) {
		t.Helper()
		_ = tx.Rollback()
	}
	// 场景一：发行锁同键互斥、异键并发。
	holder := mustBegin()
	if err := lockRelease(ctx, holder, r1); err != nil {
		t.Fatal(err)
	}
	probe := mustBegin()
	withTimeout(probe)
	if err := lockRelease(ctx, probe, r1); err == nil || !strings.Contains(err.Error(), "lock timeout") {
		rollback(probe)
		t.Fatalf("同发行应互斥，实际 %v", err)
	}
	rollback(probe)
	probe2 := mustBegin()
	withTimeout(probe2)
	if err := lockRelease(ctx, probe2, r2); err != nil {
		rollback(probe2)
		t.Fatalf("不同发行不应互斥：%v", err)
	}
	commit(probe2)
	commit(holder)
	// 场景二：定义共享-共享不互斥（本场景内提交 sh1 后再进场景三，独占前无共享持有者）。
	sh1 := mustBegin()
	if err := lockDefinitionsShared(ctx, sh1); err != nil {
		t.Fatal(err)
	}
	sh2 := mustBegin()
	withTimeout(sh2)
	if err := lockDefinitionsShared(ctx, sh2); err != nil {
		rollback(sh2)
		t.Fatalf("共享-共享不应互斥：%v", err)
	}
	commit(sh2)
	commit(sh1)
	// 场景三：独占-共享互斥（独占持有也加 lock_timeout，异常持锁时有界失败而非挂死）。
	ex := mustBegin()
	withTimeout(ex)
	if err := lockDefinitionsExclusive(ctx, ex); err != nil {
		t.Fatal(err)
	}
	probe3 := mustBegin()
	withTimeout(probe3)
	if err := lockDefinitionsShared(ctx, probe3); err == nil || !strings.Contains(err.Error(), "lock timeout") {
		rollback(probe3)
		t.Fatalf("独占-共享应互斥，实际 %v", err)
	}
	rollback(probe3)
	commit(ex)
	// 场景四：独占释放后共享通过。
	after := mustBegin()
	withTimeout(after)
	if err := lockDefinitionsShared(ctx, after); err != nil {
		rollback(after)
		t.Fatalf("独占释放后共享应通过：%v", err)
	}
	commit(after)
}

// M01 接线：被占用的发行锁阻塞同发行 track 写，释放后放行。
func TestPostgresSaveBlocksOnHeldReleaseLock(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w := f.save(Entity{Kind: "work", Title: "作品"})
	r := f.save(Entity{Kind: "release", Title: "发行", Subjects: []Subject{{WorkID: w.ID, Role: "primary"}}})
	m := f.save(Entity{Kind: "medium", Title: "CD", ReleaseID: r.ID})
	raw, err := f.s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Rollback() })
	if err := lockRelease(ctx, raw, r.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.s.Save(ctx, Edit{
			Entity:   Entity{Kind: "track", Title: "曲目", MediumID: m.ID, State: "active"},
			EditNote: "m01 wiring", Sources: fixtureSources(),
		}, f.u)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("发行锁被占用时同发行 track 写应阻塞，实际已返回 %v", err)
	case <-time.After(2 * time.Second):
	}
	if err := raw.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("锁释放后 track 写应成功：%v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("锁释放后 track 写仍被阻塞")
	}
}

// M02 接线：独占发布锁阻塞实体写、共享不阻塞。
func TestPostgresSaveRespectsDefinitionsLock(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	saveWork := func() error {
		_, err := f.s.Save(ctx, Edit{
			Entity:   Entity{Kind: "work", Title: "并发作品", State: "active"},
			EditNote: "m02 wiring", Sources: fixtureSources(),
		}, f.u)
		return err
	}
	// 共享-共享：持共享时写照常完成。
	sh, err := f.s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sh.Rollback() })
	if err := lockDefinitionsShared(ctx, sh); err != nil {
		t.Fatal(err)
	}
	sharedDone := make(chan error, 1)
	go func() { sharedDone <- saveWork() }()
	select {
	case err := <-sharedDone:
		if err != nil {
			t.Fatalf("共享下写应照常完成：%v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("共享下写被意外阻塞")
	}
	if err := sh.Commit(); err != nil {
		t.Fatal(err)
	}
	// 独占-共享：持独占时写阻塞，释放后放行。
	ex, err := f.s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ex.Rollback() })
	if err := lockDefinitionsExclusive(ctx, ex); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- saveWork() }()
	select {
	case err := <-done:
		t.Fatalf("独占发布锁被占用时写应阻塞，实际已返回 %v", err)
	case <-time.After(2 * time.Second):
	}
	if err := ex.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("锁释放后写应成功：%v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("锁释放后写仍被阻塞")
	}
}
