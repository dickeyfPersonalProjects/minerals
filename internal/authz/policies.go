package authz

// DefaultPolicies are the role-permission rules from CONTRACT §13 v2.
// Each row is (role, obj_type, obj_instance, act_set).
//
// Production deployments load policies from the database via the
// Casbin Postgres adapter; this slice is the canonical source for
// seeding a fresh database and for in-memory test enforcers.
var DefaultPolicies = [][]string{
	// anonymous — public and unlisted reads only
	{"anonymous", "specimens", "public", "view"},
	{"anonymous", "specimens", "unlisted", "view"},
	{"anonymous", "photos", "public", "view"},
	{"anonymous", "photos", "unlisted", "view"},

	// user — every authenticated caller has this role
	{"user", "specimens", "own", "*"},
	{"user", "photos", "own", "*"},
	{"user", "journal", "own", "*"},
	{"user", "collectors", "own", "*"},
	{"user", "qr-sheets", "own", "*"},
	{"user", "specimens", "shared", "view"},
	{"user", "photos", "shared", "view"},

	// devops-viewer / devops-admin
	{"devops-viewer", "devops", "*", "view"},
	{"devops-admin", "devops", "*", "view,edit"},

	// admin — superset
	{"admin", "*", "*", "*"},
	{"admin", "users", "*", "*"},
}

// DefaultGroupings encode role inheritance per CONTRACT §13 v2:
// devops-admin inherits devops-viewer via Casbin g(), not via
// Keycloak composite roles.
var DefaultGroupings = [][]string{
	{"devops-admin", "devops-viewer"},
}
