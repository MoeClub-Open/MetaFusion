package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// 启动路径不得 CrashLoop（2026-09 事故：悬挂引用 → impact 报 invalid_reference → log.Fatalf →
// 容器 CrashLoop → 网关 502）。这里跑的正是那条启动路径的最后一跳：Initialize → EnsureSeedDefinitions。
// 断言三件事：启动不失败、已发布定义确实被补新、状态信号把"有多少悬挂引用"说清楚。
func TestPostgresStartupSeedMergeToleratesDanglingReferences(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rows := func() int {
		t.Helper()
		var n int
		if err := f.s.DB.QueryRowContext(ctx, "SELECT count(*) FROM catalog.definition_config").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// 存量实例形态：已发布文档里没有 air_date（旧版本），下次启动应当补入。
	v, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := v.Document
	delete(old.Fields, "air_date")
	delete(old.Templates, "content_unit_default")
	f.publish(old, v.ETag)

	// 事故形态的脏数据：attributes.publisher 指向的那一行已经不存在。
	gone := f.save(Entity{Kind: "agent", Title: "已被删除的发行主体"})
	f.save(Entity{Kind: "release", Title: "悬挂引用发行版", Attributes: map[string]any{"publisher": gone.ID}})
	if _, err = f.s.DB.ExecContext(ctx, "DELETE FROM catalog.entities WHERE id=$1", gone.ID); err != nil {
		t.Fatal(err)
	}

	// 关键断言：悬挂引用只警告，启动不失败。
	if err = f.s.Initialize(ctx); err != nil {
		t.Fatalf("悬挂引用不该让启动失败（本次事故就是这里 CrashLoop）：%v", err)
	}
	after, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.ETag == v.ETag {
		t.Fatal("种子新增项没有发布")
	}
	if _, ok := after.Document.Fields["air_date"]; !ok {
		t.Fatal("新增种子项没进已发布文档")
	}

	st := f.s.DefinitionStatus()
	if st.ETag != after.ETag {
		t.Fatalf("状态信号的 published_id=%s，want %s", st.ETag, after.ETag)
	}
	if st.Degraded || st.PendingError != "" {
		t.Fatalf("悬挂引用是警告，不该把状态标成降级：%+v", st)
	}
	if st.DanglingReferences == 0 {
		t.Fatal("状态信号必须报出悬挂引用条数")
	}
	if st.CheckedAt == "" {
		t.Fatal("状态信号必须带检查时刻")
	}
	// 这份结构就是 /health 的 definitions：键名是运维与探针的读取契约，锁住它。
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err = json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"etag", "checked_at", "degraded", "pending_items", "dangling_references"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("状态信号缺 %s 键：%s", k, string(b))
		}
	}

	// 幂等：没有新增项的第二次启动既不写库，也不换已发布版本（但悬挂引用照报）。
	before, beforeETag := rows(), after.ETag
	if err = f.s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if n := rows(); n != before {
		t.Fatalf("重复启动新增了定义行：%d -> %d", before, n)
	}
	v3, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v3.ETag != beforeETag {
		t.Fatalf("重复启动换了已发布版本：%s -> %s", beforeETag, v3.ETag)
	}
	if got := f.s.DefinitionStatus(); got.DanglingReferences == 0 {
		t.Fatal("没有待补项的启动也要报出悬挂引用，否则重启一次就看不到它了")
	}
}

// 定义非法才是阻断项：失败必须**零写入**（不起草、不发布），已发布定义保持生效，状态信号给出
// 可诊断原因；而且重复启动不再新增定义行——事故里每次重启都在 catalog.definition_config 留下一行
// 发不出去的草稿（id 9..18 共 10 行就是这么来的）。
func TestPostgresStartupSeedMergeBlocksWithoutDraftPileUp(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rows := func() int {
		t.Helper()
		var n int
		if err := f.s.DB.QueryRowContext(ctx, "SELECT count(*) FROM catalog.definition_config").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	v, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := v.Document
	delete(old.Fields, "air_date")
	delete(old.Templates, "content_unit_default")
	f.publish(old, v.ETag)
	// f.publish 换了一版：判定基准是**当前**已发布版本。
	cur, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// 定义冲突（不是悬挂引用）：实体带了一个未声明的属性键。
	// 写路径今天会拒绝它，所以它同样只能是存量数据。
	// attributes 必须非空：jsonb_set 只能往存在的对象里加键（对 null 不会创建子键）。
	work := f.save(Entity{Kind: "work", Title: "带野字段的作品", Attributes: map[string]any{"language": "ja"}})
	if _, err = f.s.DB.ExecContext(ctx, "UPDATE catalog.entities SET document = jsonb_set(document, '{attributes,legacy_orphan}', to_jsonb('x'::text)) WHERE id=$1", work.ID); err != nil {
		t.Fatal(err)
	}

	before := rows()
	err = f.s.Initialize(ctx)
	var seedErr *DefinitionSeedError
	if !errors.As(err, &seedErr) {
		t.Fatalf("定义非法必须以 *DefinitionSeedError 报出（调用方据此降级启动而不是退出），得到 %v", err)
	}
	if seedErr.ETag != cur.ETag {
		t.Fatalf("可降级错误应带上被保留的已发布版本 %s，得到 %s", cur.ETag, seedErr.ETag)
	}
	if !strings.Contains(seedErr.Reason, "definition_impact") {
		t.Fatalf("可降级错误应含可诊断原因，得到 %q", seedErr.Reason)
	}
	v2, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v2.ETag != cur.ETag {
		t.Fatalf("定义非法时不该改动已发布版本：%s -> %s", cur.ETag, v2.ETag)
	}
	if n := rows(); n != before {
		t.Fatalf("失败的合并写入了草稿行（事故里的垃圾草稿）：%d -> %d", before, n)
	}

	st := f.s.DefinitionStatus()
	if !st.Degraded || st.ETag != cur.ETag {
		t.Fatalf("状态信号应标记降级且保留已发布版本：%+v", st)
	}
	if st.PendingItems == 0 {
		t.Fatal("状态信号应报出未生效的种子项数")
	}
	if !strings.Contains(st.PendingError, "unknown_field") {
		t.Fatalf("状态信号的原因要能定位：%q", st.PendingError)
	}
	if st.DanglingReferences != 0 {
		t.Fatalf("本用例没有悬挂引用：%+v", st)
	}

	// 幂等：重复启动依然零写入。
	if err = f.s.Initialize(ctx); err == nil {
		t.Fatal("定义非法时第二次启动同样应报出可降级错误")
	}
	if n := rows(); n != before {
		t.Fatalf("重复失败合并仍在堆草稿行：%d -> %d", before, n)
	}
}
