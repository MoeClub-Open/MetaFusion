package catalog

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/metafusion/metafusion-app/internal/testutil"
)

func restoreDroppedTestTable(t *testing.T, f fixture, ctx context.Context, migrationFile string) {
	t.Helper()
	sql, err := fs.ReadFile(testutil.LegacyMigrations(), migrationFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DB.ExecContext(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
}

// S1：安装后的库必须通过只读兼容检查；缺表/缺列/无已发布定义时如实报不兼容，
// 而不是降级启动。无 MF_V2_TEST_DSN 时跳过（隔离库按用例建删，破坏性断言安全）。
func TestPostgresCheckSchemaContract(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.s.CheckSchemaContract(ctx); err != nil {
		t.Fatalf("安装后的库应通过兼容检查：%v", err)
	}
	var subjectNames []byte
	if err := f.s.DB.QueryRowContext(ctx, `SELECT document #> '{structure,release,subject_names}' FROM catalog.definition_config WHERE singleton=true`).Scan(&subjectNames); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DB.ExecContext(ctx, `UPDATE catalog.definition_config SET document = document #- '{structure,release,subject_names}' WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	if err := f.s.CheckSchemaContract(ctx); err == nil || !strings.Contains(err.Error(), "incompatible_schema") {
		t.Fatalf("缺少新版定义契约应报不兼容，实际 %v", err)
	}
	if _, err := f.s.DB.ExecContext(ctx, `UPDATE catalog.definition_config SET document = jsonb_set(document, '{structure,release,subject_names}', $1::jsonb, true) WHERE singleton=true`, subjectNames); err != nil {
		t.Fatal(err)
	}
	if err := f.s.CheckSchemaContract(ctx); err != nil {
		t.Fatalf("恢复新版定义契约后应通过：%v", err)
	}
	// 请求日志表只由显式迁移创建，普通请求不得在表缺失时补建。
	if _, err := f.s.DB.ExecContext(ctx, `DROP TABLE catalog.api_request_logs`); err != nil {
		t.Fatal(err)
	}
	LogRequest(ctx, f.s.DB, f.u.ID, "session", "", "GET", "/api/catalog/entities", 200, 1)
	var logTableExists bool
	if err := f.s.DB.QueryRowContext(ctx, `SELECT to_regclass('catalog.api_request_logs') IS NOT NULL`).Scan(&logTableExists); err != nil {
		t.Fatal(err)
	}
	if logTableExists {
		t.Fatal("普通请求不应补建请求日志表")
	}
	if err := f.s.CheckSchemaContract(ctx); err == nil || !strings.Contains(err.Error(), "catalog.api_request_logs") {
		t.Fatalf("缺少请求日志表应报不兼容，实际 %v", err)
	}
	restoreDroppedTestTable(t, f, ctx, "000005_api_request_logs.up.sql")
	if err := f.s.CheckSchemaContract(ctx); err != nil {
		t.Fatalf("迁移恢复请求日志表后应通过兼容检查：%v", err)
	}
	// 缺表：删幂等表即不兼容。
	if _, err := f.s.DB.ExecContext(ctx, `DROP TABLE catalog.idempotency_keys`); err != nil {
		t.Fatal(err)
	}
	err := f.s.CheckSchemaContract(ctx)
	if err == nil || !strings.Contains(err.Error(), "incompatible_schema") {
		t.Fatalf("缺表应报 incompatible_schema，实际 %v", err)
	}
	// 只重建本用例删除的表，不重放其余历史迁移。
	restoreDroppedTestTable(t, f, ctx, "000006_request_idempotency.up.sql")
	if err := f.s.CheckSchemaContract(ctx); err != nil {
		t.Fatalf("恢复后应重新通过：%v", err)
	}
	// 无已发布定义：删定义行即 definitions_missing（空库路径）。
	if _, err := f.s.DB.ExecContext(ctx, `DELETE FROM catalog.definition_config`); err != nil {
		t.Fatal(err)
	}
	err = f.s.CheckSchemaContract(ctx)
	if err == nil || !strings.Contains(err.Error(), "definitions_missing") {
		t.Fatalf("无已发布定义应报 definitions_missing，实际 %v", err)
	}
}
