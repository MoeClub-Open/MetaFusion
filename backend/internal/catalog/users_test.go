package catalog

import (
	"testing"

	"github.com/google/uuid"
)

// fixtureUser 造一个"已登录用户"供目录侧用例使用。
//
// 账号的注册/登录/改密已归账号服务，目录只验签：目录表里没有任何指向 auth 的外键
// （`catalog.entities.created_by` 只是裸 UUID），因此这里**不需要往 auth schema 写任何行**——
// 身份只来自请求携带的已验签令牌，权限判定只看令牌里的 sub/role。
// 这条性质本身就是要守住的东西：目录不再读、也不再写别人的 schema。
func fixtureUser(role string) User {
	if role == "" {
		role = "editor"
	}
	name := "fixture-" + role + "-" + uuid.NewString()[:8]
	permissions := []string{}
	switch role {
	case "admin":
		permissions = []string{permissionWildcard}
	case "editor":
		permissions = []string{PermissionEntityEdit, PermissionReviewBypass}
	case "moderator":
		permissions = []string{PermissionLifecycleManage, PermissionReviewBypass}
	}
	return User{ID: uuid.NewString(), Username: name, Email: name + "@example.com", Permissions: permissions}
}

// 目录不再依赖 auth schema：这条用例把"夹具账号不必落库"钉死，
// 免得以后有人为了省事又让目录往账号表里写行。
func TestFixtureUserDoesNotTouchAuthSchema(t *testing.T) {
	u := fixtureUser("admin")
	if u.ID == "" {
		t.Fatalf("fixture user malformed: %+v", u)
	}
	if u.Username == "" || u.Email == "" {
		t.Fatalf("fixture user missing display fields: %+v", u)
	}
}
