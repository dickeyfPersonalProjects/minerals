package authz

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	"github.com/casbin/casbin/v2/persist"
)

// SharesLookup resolves the "shared" instance qualifier by checking
// the shares table for (resourceType, resourceID, userID). The
// shares table is introduced by mi-1mv; callers in environments
// without that migration MUST pass a no-op lookup (or nil, which
// the enforcer treats as a constant false).
//
// The context.Context argument is the lookup's own — Casbin does
// not thread the request context through the matcher, so the
// enforcer passes context.Background(). Implementations that need
// per-request scoping should capture it from the goroutine running
// Enforce (e.g. via a *pgxpool.Pool whose driver-level deadlines
// suffice for a fast point lookup).
type SharesLookup func(ctx context.Context, resourceType, resourceID, userID string) (bool, error)

// ErrUnknownAction is returned by Enforce when the requested action
// is empty.
var ErrUnknownAction = errors.New("authz: action is required")

// ErrNoResource is returned by Enforce when obj is nil.
var ErrNoResource = errors.New("authz: obj is required")

// NewEnforcer constructs a Casbin enforcer bound to the §13 v2 model.
// adapter is the policy-store backing (a Postgres adapter in prod,
// nil for an in-memory enforcer used in tests and seeding tools).
// shares resolves the "shared" instance qualifier; pass nil to make
// every "shared" check return false (useful before mi-1mv lands).
//
// The enforcer registers two custom matcher functions —
// actionMatch and instanceMatch — that the model file references.
// Callers MUST NOT swap the model out from under the enforcer.
func NewEnforcer(adapter persist.Adapter, shares SharesLookup) (*casbin.Enforcer, error) {
	m, err := model.NewModelFromString(Model)
	if err != nil {
		return nil, fmt.Errorf("authz: parse model: %w", err)
	}

	var enf *casbin.Enforcer
	if adapter == nil {
		enf, err = casbin.NewEnforcer(m)
	} else {
		enf, err = casbin.NewEnforcer(m, adapter)
	}
	if err != nil {
		return nil, fmt.Errorf("authz: new enforcer: %w", err)
	}

	enf.AddFunction("actionMatch", actionMatchFn)
	enf.AddFunction("instanceMatch", makeInstanceMatchFn(shares))
	return enf, nil
}

// SeedDefaultPolicies loads the CONTRACT §13 v2 default policies and
// role-inheritance groupings into enf. Idempotent — Casbin's
// AddPolicy / AddGroupingPolicy return (false, nil) for duplicates.
//
// Intended for bootstrapping a fresh database and for in-memory
// test enforcers. Production code that already has policies in the
// Postgres adapter MUST NOT call this — the policies are already
// loaded by the adapter.
func SeedDefaultPolicies(enf *casbin.Enforcer) error {
	for _, p := range DefaultPolicies {
		args := make([]any, len(p))
		for i, v := range p {
			args[i] = v
		}
		if _, err := enf.AddPolicy(args...); err != nil {
			return fmt.Errorf("authz: seed policy %v: %w", p, err)
		}
	}
	for _, g := range DefaultGroupings {
		args := make([]any, len(g))
		for i, v := range g {
			args[i] = v
		}
		if _, err := enf.AddGroupingPolicy(args...); err != nil {
			return fmt.Errorf("authz: seed grouping %v: %w", g, err)
		}
	}
	return nil
}

// actionMatchFn returns true iff the requested action is contained
// in the policy's action set. The policy set is "*" (all actions),
// a single action ("view"), or a comma-separated list ("view,edit").
func actionMatchFn(args ...any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("actionMatch: want 2 args, got %d", len(args))
	}
	reqAct, ok1 := args[0].(string)
	polAct, ok2 := args[1].(string)
	if !ok1 || !ok2 {
		return false, nil
	}
	if polAct == "*" {
		return true, nil
	}
	for _, a := range strings.Split(polAct, ",") {
		if strings.TrimSpace(a) == reqAct {
			return true, nil
		}
	}
	return false, nil
}

// makeInstanceMatchFn closes over the shares lookup so the matcher
// can resolve "shared" without the shares dependency leaking into
// the model file.
func makeInstanceMatchFn(shares SharesLookup) func(args ...any) (any, error) {
	return func(args ...any) (any, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("instanceMatch: want 3 args, got %d", len(args))
		}
		polInst, ok := args[0].(string)
		if !ok {
			return false, nil
		}
		subID, _ := args[1].(string)
		obj, ok := args[2].(*Resource)
		if !ok {
			return false, nil
		}
		switch polInst {
		case "*":
			return true, nil
		case "public":
			return obj.Visibility == "public", nil
		case "unlisted":
			return obj.Visibility == "unlisted", nil
		case "own":
			return subID != "" && obj.AuthorID == subID, nil
		case "shared":
			if shares == nil || subID == "" || obj.ID == "" {
				return false, nil
			}
			return shares(context.Background(), obj.Type, obj.ID, subID)
		default:
			// Treat any other literal as an exact resource-ID match.
			// CONTRACT §13 v2 allows a specific UUID as the instance.
			return obj.ID == polInst, nil
		}
	}
}
