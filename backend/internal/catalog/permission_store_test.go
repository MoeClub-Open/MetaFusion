package catalog

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/testutil"
)

// 端到端复核本次修复的原始问题：后台把「目录编辑」组分配给用户后，目录侧必须按
// 令牌里的权限码放行；组码与角色都不参与判定（见 permission.go）。
func TestPermissionCodesAuthorizeCatalogWrites(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: testutil.Database(t)}
	if err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sources := fixtureSources()
	// 权限集合按账号服务的组展开，角色一律是 member：授权只看码。
	editor := User{ID: uuid.NewString(), Username: "catalog-editor", Groups: []string{"catalog_editor"}, Permissions: []string{PermissionEntityEdit, PermissionRelationEdit, PermissionImportSubmit}}
	admin := User{ID: uuid.NewString(), Username: "catalog-admin", Groups: []string{"catalog_admin"}, Permissions: []string{PermissionEntityEdit, PermissionRelationEdit, PermissionDefinitionsManage, PermissionLifecycleManage, PermissionImportSubmit, PermissionShelvesManage}}
	member := User{ID: uuid.NewString(), Username: "plain-member", Groups: []string{"member"}, Permissions: []string{"community.post.create"}}
	mk := func(title string) Entity {
		return Entity{Kind: "work", Title: title, OriginalLanguage: "ja", Translations: map[string]Translation{"ja": {Title: title}}}
	}

	// 目录编辑组可直接发布（旧 editor 语义）；无目录码的成员仍走审核制。
	pub := mk("permission-editor-published")
	pub.Status = "published"
	if _, err := s.Save(ctx, Edit{Entity: pub, EditNote: "n", Sources: sources}, member); err == nil {
		t.Fatal("member without catalog.entity.edit must not publish")
	}
	saved, err := s.Save(ctx, Edit{Entity: pub, EditNote: "n", Sources: sources}, editor)
	if err != nil {
		t.Fatalf("catalog.entity.edit holder must publish: %v", err)
	}

	// 目录编辑组可协作维护他人的已发布条目，版本递增。
	saved.Title = "permission-editor-published-v2"
	updated, err := s.Save(ctx, Edit{Entity: saved, ExpectedVersion: saved.Version, EditNote: "n", Sources: sources}, editor)
	if err != nil {
		t.Fatalf("shared edit of a published entity must pass: %v", err)
	}
	updated.Title = "hijacked"
	if _, err := s.Save(ctx, Edit{Entity: updated, ExpectedVersion: updated.Version, EditNote: "n", Sources: sources}, member); err == nil {
		t.Fatal("member must not edit another user's published entity")
	}

	// 生命周期（删除/合并）只看 catalog.lifecycle.manage：目录编辑组被拒，目录管理员放行。
	if _, err := s.Lifecycle(ctx, updated.ID, LifecycleEdit{ExpectedVersion: updated.Version, EditNote: "n", Sources: sources}, editor); err == nil {
		t.Fatal("catalog_editor must not run lifecycle")
	}
	if _, err := s.Lifecycle(ctx, updated.ID, LifecycleEdit{ExpectedVersion: updated.Version, EditNote: "n", Sources: sources}, admin); err != nil {
		t.Fatalf("catalog.lifecycle.manage holder must run lifecycle: %v", err)
	}

	// 动态定义的起草只看 catalog.definitions.manage。
	defs, err := s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveDefinitions(ctx, defs.Document, defs.ETag, editor, "n", sources); err == nil {
		t.Fatal("catalog_editor must not draft definitions")
	}
	if _, err := s.SaveDefinitions(ctx, defs.Document, defs.ETag, admin, "n", sources); err != nil {
		t.Fatalf("catalog.definitions.manage holder must draft definitions: %v", err)
	}
}
