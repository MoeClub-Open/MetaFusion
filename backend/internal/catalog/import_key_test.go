package catalog

import (
	"context"
	"testing"

	"github.com/metafusion/metafusion-app/internal/testutil"
)

// 内部幂等键的分层：格式（validateExternalIDs，离线）与"谁能声明新键"
// （guardImportKey，看写入来源）分开，这里锁后者。
func TestGuardImportKey(t *testing.T) {
	key := map[string]string{"metafusion_import": "bangumi:subject:123"}
	for _, c := range []struct {
		name    string
		next    map[string]string
		prev    map[string]string
		wantErr bool
	}{
		{"不带内部键", map[string]string{"bangumi": "123"}, nil, false},
		{"新声明内部键", key, nil, true},
		{"改值", map[string]string{"metafusion_import": "bangumi:subject:124"}, key, true},
		{"同值写回（PUT 全量替换）", key, key, false},
		{"空值不算声明", map[string]string{"metafusion_import": "  "}, nil, false},
		{"删除内部键", map[string]string{}, key, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := guardImportKey(c.next, c.prev)
			if c.wantErr != (err != nil) {
				t.Fatalf("guardImportKey(%v, %v) = %v, wantErr=%v", c.next, c.prev, err, c.wantErr)
			}
			if err != nil && err.Error() != "invalid_import_key" {
				t.Fatalf("错误码应为 invalid_import_key，实际 %v", err)
			}
		})
	}
}

// 旧导入标识仅可保留或删除，新声明或改写必须被拒绝。
func TestPostgresForgedImportKeyIsRejected(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: testutil.Database(t)}
	if err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	u := fixtureUser("admin")
	sources := fixtureSources()
	entity := func(ids map[string]string) Entity {
		return Entity{Kind: "work", Title: "导入键归属用例", Attributes: map[string]any{},
			ExternalIDs: ids, Translations: map[string]Translation{"en": {Title: "import key"}}}
	}
	forged := map[string]string{"metafusion_import": "bangumi:subject:633836"}
	if _, err := s.Save(ctx, Edit{Entity: entity(forged), EditNote: "n", Sources: sources}, u); err == nil {
		t.Fatal("手工载荷新声明内部幂等键被放行")
	}
	created, err := s.Save(ctx, Edit{Entity: entity(nil), EditNote: "n", Sources: sources}, u)
	if err != nil {
		t.Fatal(err)
	}
	// 在测试库模拟导入器退役前已存在的历史数据。
	if _, err := s.DB.ExecContext(ctx, `UPDATE catalog.entities SET document=jsonb_set(document, '{external_ids}', $1::jsonb) WHERE id=$2`, `{"metafusion_import":"bangumi:subject:633836"}`, created.ID); err != nil {
		t.Fatal(err)
	}
	created.ExternalIDs = forged
	// 常规编辑（读全量再写回）带着既有键，必须放行。
	created.Title = "导入键归属用例（改）"
	updated, err := s.Save(ctx, Edit{Entity: created, ExpectedVersion: created.Version, EditNote: "n", Sources: sources}, u)
	if err != nil {
		t.Fatalf("保留既有内部键的常规编辑被拒：%v", err)
	}
	updated.ExternalIDs = map[string]string{"metafusion_import": "bangumi:subject:633837"}
	if _, err := s.Save(ctx, Edit{Entity: updated, ExpectedVersion: updated.Version, EditNote: "n", Sources: sources}, u); err == nil {
		t.Fatal("手工改写内部幂等键被放行")
	}
}
