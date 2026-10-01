package auth

// Permission is a capability checked by the API.
type Permission string

// Permissions.
const (
	PermView      Permission = "view"      // read every page
	PermOperate   Permission = "operate"   // patrols, robot commands, anomaly validation, schedules
	PermConfigure Permission = "configure" // robots, rules, floor plans, routes
	PermAudit     Permission = "audit"     // audit log
	PermUsers     Permission = "users"     // user management
)

// Roles in increasing order of privilege.
var Roles = []string{"viewer", "operator", "supervisor", "admin"}

var rolePermissions = map[string][]Permission{
	"viewer":     {PermView},
	"operator":   {PermView, PermOperate},
	"supervisor": {PermView, PermOperate, PermConfigure, PermAudit},
	"admin":      {PermView, PermOperate, PermConfigure, PermAudit, PermUsers},
}

// Can reports whether a role has a permission.
func Can(role string, p Permission) bool {
	for _, have := range rolePermissions[role] {
		if have == p {
			return true
		}
	}
	return false
}

// PermissionsOf lists the permissions of a role.
func PermissionsOf(role string) []Permission {
	return append([]Permission(nil), rolePermissions[role]...)
}

// ValidRole reports whether role exists.
func ValidRole(role string) bool { _, ok := rolePermissions[role]; return ok }
