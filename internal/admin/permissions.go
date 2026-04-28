package admin

var rolePermissions = map[Role]map[Permission]bool{
	RoleViewer: {
		PermissionViewAggregates: true,
	},
	RoleAuditor: {
		PermissionViewAggregates:       true,
		PermissionViewNormalizedTraces: true,
		PermissionReview:               true,
	},
	RoleRawAccess: {
		PermissionViewAggregates:       true,
		PermissionViewNormalizedTraces: true,
		PermissionReview:               true,
		PermissionRawEvidence:          true,
		PermissionAPIKeyLookup:         true,
	},
	RoleAdmin: {
		PermissionViewAggregates:       true,
		PermissionViewNormalizedTraces: true,
		PermissionReview:               true,
		PermissionRawEvidence:          true,
		PermissionAPIKeyLookup:         true,
		PermissionManageUsers:          true,
	},
}

func (r Role) Allows(permission Permission) bool {
	return rolePermissions[r][permission]
}
