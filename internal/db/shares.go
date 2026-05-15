package db

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
)

// NewSharesLookup returns the authz.SharesLookup the Casbin enforcer
// uses to resolve the `:shared` instance qualifier (CONTRACT.md §13
// v2). It checks the shares table (migration 0010) for a row granting
// userID access to (resourceType, resourceID).
//
// The lookup is invoked from inside the Casbin matcher, which Casbin
// runs on context.Background() — the pool's own driver-level deadlines
// bound the point lookup, which is what the authz.SharesLookup doc
// note prescribes.
func NewSharesLookup(pool *pgxpool.Pool) authz.SharesLookup {
	return func(ctx context.Context, resourceType, resourceID, userID string) (bool, error) {
		rid, err := uuid.Parse(resourceID)
		if err != nil {
			return false, nil
		}
		uid, err := uuid.Parse(userID)
		if err != nil {
			return false, nil
		}
		const q = `SELECT EXISTS (
			SELECT 1 FROM shares
			 WHERE resource_type = $1 AND resource_id = $2 AND shared_with = $3
		)`
		var ok bool
		if err := pool.QueryRow(ctx, q, resourceType, rid, uid).Scan(&ok); err != nil {
			return false, fmt.Errorf("shares lookup: %w", err)
		}
		return ok, nil
	}
}

// isAdmin reports whether the caller carries the `admin` realm role,
// which CONTRACT.md §13 v2 grants `*:*:*` — every list query skips
// scoping for an admin.
func isAdmin(u auth.User) bool {
	return slices.Contains(u.Roles, "admin")
}

// isAnonymous reports whether the caller has no application identity.
// Per CONTRACT.md §13 v2 an anonymous caller sees only `public`
// resources and owns nothing.
func isAnonymous(u auth.User) bool {
	return u.ID == uuid.Nil
}

// ownerScope returns a WHERE fragment restricting `col` to rows the
// caller owns, and the args slice extended with any new parameter.
//
//   - admin            → "" (no restriction)
//   - authenticated    → "<col> = $N"  (own rows only)
//   - anonymous        → "false"       (owns nothing)
//
// Used for collectors and journal_entries, whose §13 v2 policies grant
// the `user` role `:own` access with no public or shared tier.
func ownerScope(u auth.User, col string, args []any) (string, []any) {
	if isAdmin(u) {
		return "", args
	}
	if isAnonymous(u) {
		return "false", args
	}
	args = append(args, u.ID)
	return fmt.Sprintf("%s = $%d", col, len(args)), args
}

// specimenListScope returns the §13 v2 list-visibility WHERE fragment
// for the specimens table (own + public + shared). `tbl` is the table
// name/alias whose author_id / visibility / id columns are filtered.
// `unlisted` is deliberately excluded — it is a discoverability
// control, not a security boundary, so it never appears in lists
// unless the caller owns or is shared the row.
//
//   - admin         → "" (no restriction)
//   - authenticated → own OR public OR shared
//   - anonymous     → public only
func specimenListScope(u auth.User, tbl string, args []any) (string, []any) {
	if isAdmin(u) {
		return "", args
	}
	if isAnonymous(u) {
		return tbl + ".visibility = 'public'", args
	}
	args = append(args, u.ID)
	n := len(args)
	return fmt.Sprintf(
		"(%[1]s.author_id = $%[2]d OR %[1]s.visibility = 'public' "+
			"OR %[1]s.id IN (SELECT resource_id FROM shares "+
			"WHERE resource_type = 'specimens' AND shared_with = $%[2]d))",
		tbl, n), args
}

// specimenAccessScope returns the §13 v2 point-access WHERE fragment
// for the specimens table. Unlike specimenListScope it admits
// `unlisted` rows — direct access to an unlisted resource is allowed
// to anyone (the URL is the access control). Photos inherit their
// parent specimen's access, so the photo list query joins through
// this fragment.
//
//   - admin         → "" (no restriction)
//   - authenticated → own OR public OR unlisted OR shared
//   - anonymous     → public OR unlisted
func specimenAccessScope(u auth.User, tbl string, args []any) (string, []any) {
	if isAdmin(u) {
		return "", args
	}
	if isAnonymous(u) {
		return tbl + ".visibility IN ('public', 'unlisted')", args
	}
	args = append(args, u.ID)
	n := len(args)
	return fmt.Sprintf(
		"(%[1]s.visibility IN ('public', 'unlisted') OR %[1]s.author_id = $%[2]d "+
			"OR %[1]s.id IN (SELECT resource_id FROM shares "+
			"WHERE resource_type = 'specimens' AND shared_with = $%[2]d))",
		tbl, n), args
}
