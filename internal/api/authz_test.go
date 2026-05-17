package api

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
)

// Unit tests for authzUser (mi-2eg6). Every authenticated caller must
// receive the `user` role implicitly so that CONTRACT.md §13 v2 base
// policies match regardless of Keycloak realm-role configuration.
func TestAuthzUser(t *testing.T) {
	someID := uuid.Must(uuid.NewRandom())

	cases := []struct {
		name      string
		in        auth.User
		wantID    string
		wantRoles []string
	}{
		{
			name:      "anonymous: nil id and no roles → anonymous",
			in:        auth.User{},
			wantID:    "",
			wantRoles: []string{"anonymous"},
		},
		{
			name:      "authenticated with Keycloak-default roles gets user appended",
			in:        auth.User{ID: someID, Roles: []string{"offline_access", "uma_authorization"}},
			wantID:    someID.String(),
			wantRoles: []string{"offline_access", "uma_authorization", "user"},
		},
		{
			name:      "authenticated with user already present does not duplicate",
			in:        auth.User{ID: someID, Roles: []string{"user"}},
			wantID:    someID.String(),
			wantRoles: []string{"user"},
		},
		{
			name:      "authenticated with admin keeps admin and gains user",
			in:        auth.User{ID: someID, Roles: []string{"admin"}},
			wantID:    someID.String(),
			wantRoles: []string{"admin", "user"},
		},
		{
			name:      "authenticated with no roles gets user (no anonymous)",
			in:        auth.User{ID: someID},
			wantID:    someID.String(),
			wantRoles: []string{"user"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := authzUser(tc.in)
			if got.ID != tc.wantID {
				t.Errorf("ID = %q, want %q", got.ID, tc.wantID)
			}
			if !slices.Equal(got.Roles, tc.wantRoles) {
				t.Errorf("Roles = %v, want %v", got.Roles, tc.wantRoles)
			}
		})
	}
}

// Regression guard: authzUser must not mutate the caller's Roles
// slice — handlers reuse auth.FromContext(ctx) across the request and
// an appended `user` must not leak back to the auth.User in context.
func TestAuthzUser_DoesNotMutateInput(t *testing.T) {
	id := uuid.Must(uuid.NewRandom())
	original := []string{"offline_access"}
	in := auth.User{ID: id, Roles: original}

	_ = authzUser(in)

	if !slices.Equal(in.Roles, []string{"offline_access"}) {
		t.Errorf("input Roles mutated: got %v, want [offline_access]", in.Roles)
	}
}
