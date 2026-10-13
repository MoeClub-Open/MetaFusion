package catalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// 权限码是账号服务与目录侧的共享契约：改名等于改两边，这里把拼写钉死
// （对照 metafusion-auth/internal/store/access.go 的 PermissionCatalog 与 seed_groups.go）。
func TestCatalogPermissionCodesMatchAccountService(t *testing.T) {
	want := []string{"catalog.entity.edit", "catalog.relation.edit", "catalog.definitions.manage", "catalog.lifecycle.manage", "catalog.shelves.manage"}
	if len(catalogPermissionCodes) != len(want) {
		t.Fatalf("catalog permission code drift: %v", catalogPermissionCodes)
	}
	for _, code := range want {
		if !contains(catalogPermissionCodes, code) {
			t.Fatalf("missing catalog permission code %q", code)
		}
	}
}

// 令牌带 permissions 时以码为准：放行、拒绝、* 通配各一条。
func TestCanHonoursTokenPermissions(t *testing.T) {
	editor := User{Permissions: []string{PermissionEntityEdit, PermissionRelationEdit}}
	if !editor.Can(PermissionEntityEdit) {
		t.Fatal("granted permission code must pass")
	}
	if editor.Can(PermissionDefinitionsManage) || editor.Can(PermissionLifecycleManage) {
		t.Fatalf("ungranted permission code must be denied: %+v", editor)
	}
	wildcard := User{Permissions: []string{permissionWildcard}}
	for _, code := range catalogPermissionCodes {
		if !wildcard.Can(code) {
			t.Fatalf("wildcard must grant %q", code)
		}
	}
	// 其它子系统的码与本服务无关：没声明 catalog 码就不放行目录能力。
	foreign := User{Permissions: []string{"community.post.moderate"}}
	if foreign.Can(PermissionEntityEdit) {
		t.Fatal("foreign-service codes must not grant catalog capabilities")
	}
}

func TestCanDeniesEmptyPermissions(t *testing.T) {
	plain := User{}
	for _, code := range catalogPermissionCodes {
		if plain.Can(code) {
			t.Fatalf("empty permissions must deny %q", code)
		}
	}
	limited := User{Permissions: []string{PermissionEntityEdit}}
	if !limited.Can(PermissionEntityEdit) || limited.Can(PermissionLifecycleManage) {
		t.Fatalf("permission set must be authoritative: %+v", limited)
	}
}

// S01：空权限集合（空数组、显式 null、nil）一律不放行任何码——判定只看 permissions，
// 不存在角色兜底（见 permission.go 的 Can/HasPermission）。
func TestCanDeniesExplicitEmptyAdmin(t *testing.T) {
	for _, u := range []User{
		{Permissions: []string{}},
		{Permissions: []string{}},
		{},
	} {
		for _, code := range catalogPermissionCodes {
			if u.Can(code) {
				t.Fatalf("显式空权限不得回落 admin：%+v 不该放行 %s", u, code)
			}
		}
	}
}

// S01：第三方 OAuth 身份在治理码上直接拒绝，即使带 * 通配或显式持有该码，
// 即使令牌形态像老令牌（缺 permissions 键）——第三方标记优先于一切兜底。
func TestCanDeniesThirdPartyGovernance(t *testing.T) {
	for _, u := range []User{
		{Permissions: []string{permissionWildcard}, IsThirdParty: true},
		{Permissions: []string{PermissionEntityEdit}, IsThirdParty: true},
		{IsThirdParty: true},
	} {
		for _, code := range catalogPermissionCodes {
			if u.Can(code) {
				t.Fatalf("第三方不得放行治理码：%+v 不该放行 %s", u, code)
			}
		}
	}
}

func TestClaimsThirdPartyMapping(t *testing.T) {
	var c Claims
	if err := json.Unmarshal([]byte(`{"sub":"u-1","token_use":"session","permissions":[]}`), &c); err != nil {
		t.Fatalf("decode permissions: %v", err)
	}
	var tp Claims
	if err := json.Unmarshal([]byte(`{"sub":"u-1","token_use":"oauth","scope":"openid profile","client_id":"third-party-app"}`), &tp); err != nil {
		t.Fatal(err)
	}
	u := ClaimsToUser(&tp)
	if u == nil || !u.IsThirdParty {
		t.Fatalf("带 OAuth 标记的载荷应为第三方：%+v", u)
	}
	session := ClaimsToUser(&c)
	if session == nil || session.IsThirdParty {
		t.Fatalf("会话载荷不应标第三方：%+v", session)
	}
	if ClaimsToUser(nil) != nil {
		t.Fatal("nil 载荷应得 nil 用户")
	}
}

// 权限码放行与拒绝落到编辑判定上：同一实体，持码者与无码者结果不同，角色不参与。
func TestEditingDecidedByPermissionCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		u    User
		e    Entity
		want bool
	}{
		{"entity.edit maintains foreign published", User{ID: "self", Permissions: []string{PermissionEntityEdit}}, Entity{Status: "published", CreatedBy: "other"}, true},
		{"no code cannot maintain foreign published", User{ID: "self", Permissions: []string{"community.post.create"}}, Entity{Status: "published", CreatedBy: "other"}, false},
		{"no code may still edit own draft", User{ID: "self", Permissions: []string{"community.post.create"}}, Entity{Status: "draft", CreatedBy: "self"}, true},
		{"own published needs the code", User{ID: "self", Permissions: []string{"community.post.create"}}, Entity{Status: "published", CreatedBy: "self"}, false},
		{"entity.edit stays out of foreign drafts", User{ID: "self", Permissions: []string{PermissionEntityEdit}}, Entity{Status: "draft", CreatedBy: "other"}, false},
		{"lifecycle.manage reaches foreign drafts", User{ID: "self", Permissions: []string{PermissionLifecycleManage}}, Entity{Status: "draft", CreatedBy: "other"}, true},
		{"terminated status stays closed", User{ID: "self", Permissions: []string{permissionWildcard}}, Entity{Status: "merged", CreatedBy: "self"}, false},
	} {
		if got := canEditEntity(tc.u, tc.e); got != tc.want {
			t.Errorf("%s: canEditEntity=%v want=%v", tc.name, got, tc.want)
		}
		if got := canWriteRelation(tc.u, tc.e); got != tc.want {
			t.Errorf("%s: canWriteRelation=%v want=%v", tc.name, got, tc.want)
		}
	}
	// 关系目标端否决权：持实体编辑权可挂他人公开条目，无码者不可。
	published := Entity{Status: "published", CreatedBy: "other"}
	if !canAttachToTarget(User{ID: "self", Permissions: []string{PermissionEntityEdit}}, published) {
		t.Fatal("catalog.entity.edit must attach to published targets")
	}
	if canAttachToTarget(User{ID: "self", Permissions: []string{"community.post.create"}}, published) {
		t.Fatal("member without catalog code must not attach to foreign published targets")
	}
}

// 路由闸门：403 由权限码决定，401 才是未登录；老令牌按角色兜底仍能进。
func TestRequiredGateUsesPermissionCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name string
		u    *User
		want int
	}{
		{"anonymous", nil, http.StatusUnauthorized},
		{"ungranted code", &User{Permissions: []string{"community.post.create"}}, http.StatusForbidden},
		{"granted code", &User{Permissions: []string{PermissionDefinitionsManage}}, http.StatusOK},
		{"wildcard", &User{Permissions: []string{permissionWildcard}}, http.StatusOK},
		{"no permission", &User{}, http.StatusForbidden},
		{"explicit empty admin", &User{Permissions: []string{}}, http.StatusForbidden},
		{"third-party admin wildcard", &User{Permissions: []string{permissionWildcard}, IsThirdParty: true}, http.StatusForbidden},
		{"third-party explicit code", &User{Permissions: []string{PermissionDefinitionsManage}, IsThirdParty: true}, http.StatusForbidden},
	} {
		r := gin.New()
		r.GET("/x", func(c *gin.Context) {
			if tc.u != nil {
				c.Set("catalog_user", tc.u)
			}
		}, required(PermissionDefinitionsManage), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
		if w.Code != tc.want {
			t.Errorf("%s: status=%d want=%d", tc.name, w.Code, tc.want)
		}
	}
	// code 为空只要求登录：任意登录用户都能过（含第三方，自助草稿走所有权判定）。
	r := gin.New()
	r.GET("/x", func(c *gin.Context) { c.Set("catalog_user", &User{}) }, required(""), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("authenticated-only gate rejected a logged-in user: %d", w.Code)
	}
	r2 := gin.New()
	r2.GET("/x", func(c *gin.Context) {
		c.Set("catalog_user", &User{IsThirdParty: true})
	}, required(""), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w2 := httptest.NewRecorder()
	r2.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w2.Code != http.StatusOK {
		t.Fatalf("authenticated-only gate must stay open to third-party callers: %d", w2.Code)
	}
}

// L2：纯登录路由默认拒绝第三方写入（仅 openid/profile/email 授权无写能力）：
// 第三方建草稿（POST）403，会话仍 200；读（GET）仍对第三方开放。
func TestRequiredWriteDeniesThirdParty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	third := &User{ID: "third", IsThirdParty: true}
	session := &User{ID: "self"}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r := gin.New()
		r.Handle(method, "/x", func(c *gin.Context) {
			if c.GetHeader("X-Actor") == "third" {
				c.Set("catalog_user", third)
			} else {
				c.Set("catalog_user", session)
			}
		}, required(""), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/x", nil)
		req.Header.Set("X-Actor", "third")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "forbidden") {
			t.Errorf("%s 第三方写: status=%d body=%s, want 403 forbidden", method, w.Code, w.Body.String())
		}
		w2 := httptest.NewRecorder()
		r.ServeHTTP(w2, httptest.NewRequest(method, "/x", nil))
		if w2.Code != http.StatusOK {
			t.Errorf("%s 会话写: status=%d, want 200", method, w2.Code)
		}
	}
	// 读仍对第三方开放（个人偏好读、通知列表等自助读端）。
	r := gin.New()
	r.GET("/x", func(c *gin.Context) { c.Set("catalog_user", third) }, required(""), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("第三方读: status=%d, want 200", w.Code)
	}
}
