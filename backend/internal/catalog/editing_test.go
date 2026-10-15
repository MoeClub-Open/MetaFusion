package catalog

import "testing"

func TestSharedEditingRoles(t *testing.T) {
	for _, tc := range []struct {
		role, status, owner string
		allowed             bool
	}{
		{"editor", "active", "other", true},
		{"editor", "active", "other", true},
		{"editor", "active", "other", true},
		{"editor", "active", "self", true},
		{"user", "active", "self", true},
		{"user", "active", "self", true},
		{"user", "active", "other", false},
		{"admin", "active", "other", true},
		{"editor", "merged", "self", false},
		{"admin", "deleted", "other", false},
	} {
		u := User{ID: "self"}
		switch tc.role {
		case "admin":
			u.Permissions = []string{permissionWildcard}
		case "editor":
			u.Permissions = []string{PermissionEntityEdit}
		}
		e := Entity{State: tc.status, CreatedBy: tc.owner}
		if canEditEntity(u, e) != tc.allowed || canWriteRelation(u, e) != tc.allowed {
			t.Errorf("role=%s status=%s owner=%s want=%v", tc.role, tc.status, tc.owner, tc.allowed)
		}
	}
	if canAttachToTarget(User{ID: "self"}, Entity{State: "active", CreatedBy: "other"}) {
		t.Fatal("ordinary user must not gain shared relationship editing")
	}
}
