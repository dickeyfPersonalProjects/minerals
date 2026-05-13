package authz

import (
	"context"
	"fmt"

	"github.com/casbin/casbin/v2"
)

// Enforce evaluates whether u may perform act on obj against enf's
// policy set. The result is the OR over u.Roles — a single role
// granting the action is sufficient.
//
// Returns ErrUnknownAction if act is empty and ErrNoResource if
// obj is nil. Wraps any underlying Casbin / adapter error with
// the role being evaluated for traceability.
//
// The ctx argument is accepted for symmetry with other helpers and
// for future use (e.g. tracing); the current implementation does not
// thread it into the Casbin matcher — see SharesLookup's note.
func Enforce(ctx context.Context, enf *casbin.Enforcer, u User, obj *Resource, act string) (bool, error) {
	_ = ctx
	if act == "" {
		return false, ErrUnknownAction
	}
	if obj == nil {
		return false, ErrNoResource
	}
	for _, role := range u.Roles {
		ok, err := enf.Enforce(role, u.ID, obj, act)
		if err != nil {
			return false, fmt.Errorf("authz: enforce role %q: %w", role, err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}
