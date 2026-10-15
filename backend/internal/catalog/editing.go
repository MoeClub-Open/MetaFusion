package catalog

import (
	"context"
	"fmt"
)

// 协作编辑由权限码判定；作者可提交自己条目的修改。免审与审核另行授权。

// ownedBy 要求 CreatedBy 非空：匿名（ID 为空）与历史脏行（created_by 为空）不得互相认领。
func ownedBy(e Entity, u User) bool { return e.CreatedBy != "" && e.CreatedBy == u.ID }

// canEditEntity 报告 u 能否改动实体 e。
func canEditEntity(u User, e Entity) bool {
	if e.State == "deleted" || e.State == "merged" {
		return false
	}
	if u.Can(PermissionLifecycleManage) {
		return true
	}
	if u.Can(PermissionEntityEdit) {
		return true
	}
	return ownedBy(e, u)
}

// Applied facts always have an active lifecycle and a reviewed marker.
func prepareEntityWriteState(old Entity, next *Entity, u User) error {
	if old.ID != "" && !canEditEntity(u, old) {
		return errForbidden
	}
	if next.State == "" {
		next.State = "active"
	}
	if next.State != "active" || next.RedirectID != "" {
		return fmt.Errorf("use_lifecycle_endpoint")
	}
	// Only applied facts are materialized. Pending commits never replace this head.
	next.ReviewStatus = "reviewed"
	return nil
}

// Applied facts resolve validated references against public visibility.
func entityWriteReference(ctx context.Context, q queryer, e Entity, u User) (func(string, []string) error, error) {
	reader := &u
	if e.State == "active" {
		reader = nil
	}
	return reference(ctx, q, reader), nil
}

func validateEntityStructuralReferences(e Entity, ref func(string, []string) error) error {
	for _, item := range []struct {
		id   string
		kind string
	}{
		{e.WorkID, "work"}, {e.ReleaseID, "release"}, {e.MediumID, "medium"},
		{e.ParentID, e.Kind}, {e.ContentUnitID, "content_unit"},
	} {
		if item.id != "" {
			if err := ref(item.id, []string{item.kind}); err != nil {
				return err
			}
		}
	}
	return nil
}
