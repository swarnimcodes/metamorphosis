package gym

import "github.com/smarniw/metamorphosis/internal/db"

// Role is what a user may do to the shared gym directory. Roles are granted by hand in the database.
type Role = db.UserRole

const (
	RoleUser      = db.UserRoleUser
	RoleModerator = db.UserRoleModerator
	RoleAdmin     = db.UserRoleAdmin
)

// Abilities are what one role may do to one gym. This is the only place the rules are written down; the
// service enforces them and the API reports them so the app never has to repeat them.
//
//	                          user  moderator  admin
//	add a gym                  ✓       ✓         ✓      (no gym yet, so not an ability here)
//	edit an unverified gym     ✓       ✓         ✓
//	edit a verified gym        –       ✓         ✓
//	verify / unverify          –       ✓         ✓
//	revert someone's edit      –       ✓         ✓
//	merge a duplicate          –       ✓         ✓
//	delete                     –       –         ✓
type Abilities struct {
	Edit, Verify, Revert, Merge, Delete bool
}

// AbilitiesFor is what role may do to a gym that is (or is not) verified.
func AbilitiesFor(role Role, verified bool) Abilities {
	staff := role == RoleModerator || role == RoleAdmin
	return Abilities{
		Edit:   staff || !verified,
		Verify: staff,
		Revert: staff,
		Merge:  staff,
		Delete: role == RoleAdmin,
	}
}

// Staff reports whether the role moderates.
func Staff(role Role) bool { return role == RoleModerator || role == RoleAdmin }
