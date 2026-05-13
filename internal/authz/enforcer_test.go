package authz

import (
	"context"
	"errors"
	"testing"
)

// newSeededEnforcer returns an in-memory enforcer pre-loaded with
// the §13 v2 default policies and an injectable shares lookup.
func newSeededEnforcer(t *testing.T, shares SharesLookup) *enforcerHarness {
	t.Helper()
	enf, err := NewEnforcer(nil, shares)
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	if err := SeedDefaultPolicies(enf); err != nil {
		t.Fatalf("SeedDefaultPolicies: %v", err)
	}
	return &enforcerHarness{t: t, enf: enf}
}

type enforcerHarness struct {
	t   *testing.T
	enf interface {
		Enforce(rvals ...interface{}) (bool, error)
	}
}

func (h *enforcerHarness) check(name string, u User, obj *Resource, act string, want bool) {
	h.t.Helper()
	h.t.Run(name, func(t *testing.T) {
		got, err := enforceWith(h.enf, u, obj, act)
		if err != nil {
			t.Fatalf("Enforce: %v", err)
		}
		if got != want {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// enforceWith mirrors Enforce but accepts the narrow interface so
// the harness can hold the concrete *casbin.Enforcer indirectly.
func enforceWith(e interface {
	Enforce(rvals ...interface{}) (bool, error)
}, u User, obj *Resource, act string) (bool, error) {
	for _, role := range u.Roles {
		ok, err := e.Enforce(role, u.ID, obj, act)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func TestEnforcer_AnonymousVisibility(t *testing.T) {
	h := newSeededEnforcer(t, nil)
	anon := User{ID: "", Roles: []string{"anonymous"}}

	h.check("public specimen view",
		anon, &Resource{Type: "specimens", ID: "s1", Visibility: "public"}, "view", true)
	h.check("unlisted specimen view",
		anon, &Resource{Type: "specimens", ID: "s1", Visibility: "unlisted"}, "view", true)
	h.check("private specimen view denied",
		anon, &Resource{Type: "specimens", ID: "s1", Visibility: "private"}, "view", false)
	h.check("public specimen edit denied",
		anon, &Resource{Type: "specimens", ID: "s1", Visibility: "public"}, "edit", false)
	h.check("journal view denied (no anonymous policy)",
		anon, &Resource{Type: "journal", ID: "j1", Visibility: "public"}, "view", false)
}

func TestEnforcer_UserOwnership(t *testing.T) {
	h := newSeededEnforcer(t, nil)
	owner := User{ID: "u1", Roles: []string{"user"}}
	intruder := User{ID: "u2", Roles: []string{"user"}}

	private := &Resource{Type: "specimens", ID: "s1", Visibility: "private", AuthorID: "u1"}
	h.check("owner views own private", owner, private, "view", true)
	h.check("owner edits own private", owner, private, "edit", true)
	h.check("owner deletes own private", owner, private, "delete", true)
	h.check("intruder views private denied", intruder, private, "view", false)
	h.check("intruder edits private denied", intruder, private, "edit", false)

	// Cross-type: user role grants journal:own but not journal:public.
	publicJournal := &Resource{Type: "journal", ID: "j1", Visibility: "public", AuthorID: "u2"}
	h.check("user denied public journal of another", owner, publicJournal, "view", false)
}

func TestEnforcer_UserShared(t *testing.T) {
	shareCalls := 0
	lookup := func(_ context.Context, resType, resID, userID string) (bool, error) {
		shareCalls++
		return resType == "specimens" && resID == "s1" && userID == "u2", nil
	}
	h := newSeededEnforcer(t, lookup)

	// u1 owns s1; u2 has it shared.
	bob := User{ID: "u2", Roles: []string{"user"}}
	shared := &Resource{Type: "specimens", ID: "s1", Visibility: "private", AuthorID: "u1"}

	h.check("shared view permitted", bob, shared, "view", true)
	h.check("shared edit denied (user:specimens:shared is view-only)",
		bob, shared, "edit", false)

	stranger := User{ID: "u3", Roles: []string{"user"}}
	h.check("non-shared user denied", stranger, shared, "view", false)

	if shareCalls == 0 {
		t.Errorf("expected the shares lookup to be invoked at least once")
	}
}

func TestEnforcer_SharedNilLookupReturnsFalse(t *testing.T) {
	h := newSeededEnforcer(t, nil) // shares lookup intentionally nil
	bob := User{ID: "u2", Roles: []string{"user"}}
	shared := &Resource{Type: "specimens", ID: "s1", Visibility: "private", AuthorID: "u1"}
	h.check("no lookup → shared check is false", bob, shared, "view", false)
}

func TestEnforcer_DevopsRoleInheritance(t *testing.T) {
	h := newSeededEnforcer(t, nil)
	devopsRsrc := &Resource{Type: "devops"}

	viewer := User{ID: "u1", Roles: []string{"devops-viewer"}}
	h.check("viewer can view devops", viewer, devopsRsrc, "view", true)
	h.check("viewer denied edit devops", viewer, devopsRsrc, "edit", false)

	op := User{ID: "u2", Roles: []string{"devops-admin"}}
	h.check("admin can view devops", op, devopsRsrc, "view", true)
	h.check("admin can edit devops", op, devopsRsrc, "edit", true)
}

func TestEnforcer_AdminSuperset(t *testing.T) {
	h := newSeededEnforcer(t, nil)
	admin := User{ID: "u1", Roles: []string{"admin"}}

	h.check("admin views anyone's private specimen",
		admin, &Resource{Type: "specimens", ID: "s1", Visibility: "private", AuthorID: "u9"}, "view", true)
	h.check("admin edits users resource",
		admin, &Resource{Type: "users", ID: "u9"}, "edit", true)
	h.check("admin deletes anything",
		admin, &Resource{Type: "journal", ID: "j1", Visibility: "private", AuthorID: "u9"}, "delete", true)
}

func TestEnforcer_MultipleRolesOR(t *testing.T) {
	// Keycloak attaches "user" plus any extras. A devops-viewer is
	// also a user — confirm Enforce takes the union.
	h := newSeededEnforcer(t, nil)
	caller := User{ID: "u1", Roles: []string{"user", "devops-viewer"}}

	h.check("user role covers own specimen",
		caller, &Resource{Type: "specimens", ID: "s1", Visibility: "private", AuthorID: "u1"}, "edit", true)
	h.check("devops-viewer role covers devops",
		caller, &Resource{Type: "devops"}, "view", true)
}

func TestEnforce_ErrorPaths(t *testing.T) {
	enf, err := NewEnforcer(nil, nil)
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}

	u := User{ID: "u1", Roles: []string{"user"}}
	obj := &Resource{Type: "specimens", ID: "s1"}

	if _, err := Enforce(context.Background(), enf, u, obj, ""); !errors.Is(err, ErrUnknownAction) {
		t.Errorf("empty action: want ErrUnknownAction, got %v", err)
	}
	if _, err := Enforce(context.Background(), enf, u, nil, "view"); !errors.Is(err, ErrNoResource) {
		t.Errorf("nil obj: want ErrNoResource, got %v", err)
	}
}

func TestActionMatchFn(t *testing.T) {
	cases := []struct {
		req, pol string
		want     bool
	}{
		{"view", "view", true},
		{"view", "*", true},
		{"view", "view,edit", true},
		{"edit", "view,edit", true},
		{"delete", "view,edit", false},
		{"view", "edit", false},
		{"view", "view, edit", true}, // tolerant of whitespace
	}
	for _, tc := range cases {
		got, err := actionMatchFn(tc.req, tc.pol)
		if err != nil {
			t.Fatalf("actionMatchFn(%q, %q): %v", tc.req, tc.pol, err)
		}
		if got.(bool) != tc.want {
			t.Errorf("actionMatchFn(%q, %q) = %v, want %v", tc.req, tc.pol, got, tc.want)
		}
	}
}

func TestInstanceMatchFn_UUIDLiteral(t *testing.T) {
	fn := makeInstanceMatchFn(nil)
	obj := &Resource{Type: "specimens", ID: "11111111-1111-1111-1111-111111111111"}
	got, err := fn("11111111-1111-1111-1111-111111111111", "u1", obj)
	if err != nil {
		t.Fatalf("instanceMatch: %v", err)
	}
	if !got.(bool) {
		t.Error("expected exact UUID match to be true")
	}
	got2, _ := fn("22222222-2222-2222-2222-222222222222", "u1", obj)
	if got2.(bool) {
		t.Error("expected non-matching UUID literal to be false")
	}
}
