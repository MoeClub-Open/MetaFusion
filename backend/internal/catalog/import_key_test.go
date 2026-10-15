package catalog

import (
	"context"
	"testing"

	"github.com/metafusion/metafusion-app/internal/testutil"
)

func linksOf(ids map[string]string) []ExternalLink { return linksFromIDMap(ids) }

// 内部幂等键的分层：格式（validateExternalLinks，离线）与"谁能声明新键"
// （guardImportKey，看写入来源）分开，这里锁后者。
func TestGuardImportKey(t *testing.T) {
	key := linksOf(map[string]string{"metafusion_import": "bangumi:subject:123"})
	for _, c := range []struct {
		name    string
		next    []ExternalLink
		prev    []ExternalLink
		wantErr bool
	}{
		{"不带内部键", linksOf(map[string]string{"bangumi": "123"}), nil, false},
		{"新声明内部键", key, nil, true},
		{"改值", linksOf(map[string]string{"metafusion_import": "bangumi:subject:124"}), key, true},
		{"保留旧键后追加新键", append(append([]ExternalLink{}, key...), linkFromID("metafusion_import", "bangumi:subject:124")), key, true},
		{"同值写回（PUT 全量替换）", key, key, false},
		{"空值不算声明", []ExternalLink{{Kind: "metafusion_import", URL: "mf://external/metafusion_import/x", ExternalID: "  "}}, nil, false},
		{"删除内部键", nil, key, false},
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
			ExternalLinks: linksOf(ids), Translations: map[string]Translation{"en": {Title: "import key"}}}
	}
	forged := map[string]string{"metafusion_import": "bangumi:subject:633836"}
	if _, err := s.Save(ctx, Edit{Entity: entity(forged), EditNote: "n", Sources: sources}, u); err == nil {
		t.Fatal("手工载荷新声明内部幂等键被放行")
	}
	created, err := s.Save(ctx, Edit{Entity: entity(nil), EditNote: "n", Sources: sources}, u)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟退役导入器留下的真实存量标识；当前服务不得新建它。
	created.ExternalLinks = linksOf(forged)
	if _, err := s.DB.ExecContext(ctx, `UPDATE catalog.entities SET document=jsonb_set(document, '{external_links}', $1::jsonb) WHERE id=$2`, encode(created.ExternalLinks), created.ID); err != nil {
		t.Fatal(err)
	}
	created.Title = "导入键归属用例（改）"
	updated, err := s.Save(ctx, Edit{Entity: created, ExpectedVersion: created.Version, EditNote: "n", Sources: sources}, u)
	if err != nil {
		t.Fatalf("保留既有内部键的常规编辑被拒：%v", err)
	}
	updated.ExternalLinks = linksOf(map[string]string{"metafusion_import": "bangumi:subject:633837"})
	if _, err := s.Save(ctx, Edit{Entity: updated, ExpectedVersion: updated.Version, EditNote: "n", Sources: sources}, u); err == nil {
		t.Fatal("手工改写内部幂等键被放行")
	}
}
