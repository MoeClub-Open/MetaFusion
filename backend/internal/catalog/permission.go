package catalog

// 目录侧授权的集中判定：仅以权限码为准。
//
// 权限码由**各子系统自己声明**、由账号服务（metafusion-auth）装进权限组并随访问令牌下发
// （claims.permissions，admin 组带 * 通配）。码名必须与账号服务的权限清单逐字一致
// （metafusion-auth/internal/store/access.go 的 PermissionCatalog 与 seed_groups.go 播种的
// 系统组）：两边都只读令牌、谁也不查对方的库，唯一需要对齐的就是码的拼写。
// 组名只供展示与审计，不参与判定。

const (
	// PermissionEntityEdit 编辑目录实体：维护自己创建的条目，并协作维护公开条目。
	PermissionEntityEdit = "catalog.entity.edit"
	// Review applies a sealed commit; bypass permits the submitter's own commit to apply immediately.
	PermissionReviewManage = "catalog.review.manage"
	PermissionReviewBypass = "catalog.review.bypass"
	// PermissionRelationEdit 编辑实体关系：POST/PUT/DELETE /catalog/relations 的路由闸门，
	// 与实体编辑分开。两端细粒度判定（canWriteRelation/canAttachToTarget）仍按实体口径复核。
	PermissionRelationEdit = "catalog.relation.edit"
	// PermissionDefinitionsManage 管理动态定义：起草、影响面校验与发布。
	PermissionDefinitionsManage = "catalog.definitions.manage"
	// PermissionLifecycleManage 审核与生命周期：删除/合并，以及处置他人的未发布条目。
	PermissionLifecycleManage = "catalog.lifecycle.manage"
	// PermissionShelvesManage 管理货架：管理端点，以及货架求值时预览未发布条目。
	PermissionShelvesManage = "catalog.shelves.manage"

	// permissionWildcard 是账号服务给的「全部权限」码（admin 组）。
	permissionWildcard = "*"
)

// catalogPermissionCodes 是本服务声明的全部目录权限码，用于区分治理操作。
var catalogPermissionCodes = []string{
	PermissionEntityEdit,
	PermissionReviewManage,
	PermissionReviewBypass,
	PermissionRelationEdit,
	PermissionDefinitionsManage,
	PermissionLifecycleManage,
	PermissionShelvesManage,
}

// HasPermission 只看权限码；* 通配即全权，空集合不授予任何权限。
func (u User) HasPermission(code string) bool {
	return contains(u.Permissions, permissionWildcard) || contains(u.Permissions, code)
}

// Can 报告用户是否持有某权限码。第三方 OAuth 身份不能执行目录治理操作。
func (u User) Can(code string) bool {
	if u.IsThirdParty && isGovernanceCode(code) {
		return false
	}
	return u.HasPermission(code)
}

// CanBypassReview follows the account's current group permissions. PAT scopes
// still govern each operation; account eligibility never grants editing rights.
func (u User) CanBypassReview() bool {
	if u.ID == "" || u.IsThirdParty {
		return false
	}
	if u.FromPAT {
		return u.CatalogReviewExempt
	}
	return u.Can(PermissionReviewBypass)
}

// isGovernanceCode 报告是否为治理类（管理）权限码：目录侧全部自有码都是管理动作，
// 第三方令牌默认拒绝；跨服务的码不归本服务判定（HasPermission 只认 * 通配）。
func isGovernanceCode(code string) bool { return contains(catalogPermissionCodes, code) }
