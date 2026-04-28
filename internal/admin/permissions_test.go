package admin

import "testing"

func TestRolePermissions(t *testing.T) {
	tests := []struct {
		role       Role
		permission Permission
		want       bool
	}{
		{RoleViewer, PermissionViewAggregates, true},
		{RoleViewer, PermissionViewNormalizedTraces, false},
		{RoleAuditor, PermissionViewNormalizedTraces, true},
		{RoleAuditor, PermissionReview, true},
		{RoleAuditor, PermissionRawEvidence, false},
		{RoleRawAccess, PermissionRawEvidence, true},
		{RoleRawAccess, PermissionAPIKeyLookup, true},
		{RoleAdmin, PermissionManageUsers, true},
		{RoleAdmin, PermissionAPIKeyLookup, true},
	}

	for _, tt := range tests {
		if got := tt.role.Allows(tt.permission); got != tt.want {
			t.Fatalf("role %q Allows(%q) = %v, want %v", tt.role, tt.permission, got, tt.want)
		}
	}
}
