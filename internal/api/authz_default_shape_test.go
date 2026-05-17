package api

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
)

// keycloakDefaultClaimsSnapshot is a fixed relative path under the
// repo, intentionally a literal so gosec G304 does not flag a
// variable file inclusion at the os.ReadFile call below.
const keycloakDefaultClaimsSnapshot = "../oidc/testdata/keycloak-default-token-claims.json"

// TestAuthzUser_DefaultKeycloakShape pins authzUser's behavior against
// the committed Keycloak "default-shaped user" snapshot — the realm
// roles a freshly-created user receives from Keycloak with no fixture
// customisation (see internal/oidc/testdata/keycloak-default-token-claims.json
// and docs/design/08-keycloak-default-token-shape.md, mi-2xa4).
//
// Two assertions, hitting both layers of the defense-in-depth that
// mi-cl1 / mi-2eg6 / mi-rcox now form:
//
//  1. With the snapshot's full role set (which mi-rcox makes include
//     `user` via the realm default-roles composite) the backend
//     preserves it — no role is silently dropped en route to Casbin.
//
//  2. With the snapshot's role set MINUS `user` — i.e. simulating
//     a future Keycloak/TF regression of mi-rcox — the backend STILL
//     produces a `user` role on the output, because authzUser injects
//     it implicitly (mi-2eg6). This is the unit-level guarantee that
//     the backend half of the contract survives realm drift; the
//     integration smoke proves the realm half of the contract still
//     matches the snapshot.
func TestAuthzUser_DefaultKeycloakShape(t *testing.T) {
	raw, err := os.ReadFile(keycloakDefaultClaimsSnapshot)
	if err != nil {
		t.Fatalf("read snapshot %s: %v", keycloakDefaultClaimsSnapshot, err)
	}

	var snap struct {
		RealmAccess struct {
			Roles []string `json:"roles"`
		} `json:"realm_access"`
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("parse snapshot %s: %v", keycloakDefaultClaimsSnapshot, err)
	}
	if len(snap.RealmAccess.Roles) == 0 {
		t.Fatalf("snapshot has empty realm_access.roles — expected the default Keycloak realm role set")
	}

	id := uuid.Must(uuid.NewRandom())

	// 1. Full snapshot shape: every snapshot role survives, and `user`
	// is present (whether from the realm or the implicit fallback).
	full := authzUser(auth.User{ID: id, Roles: snap.RealmAccess.Roles})
	if !slices.Contains(full.Roles, "user") {
		t.Errorf("authzUser must yield `user` for the snapshot's full role set; got Roles=%v", full.Roles)
	}
	if full.ID != id.String() {
		t.Errorf("authzUser dropped the caller id: got %q want %q", full.ID, id.String())
	}
	for _, want := range snap.RealmAccess.Roles {
		if !slices.Contains(full.Roles, want) {
			t.Errorf("authzUser dropped a snapshot role %q from output; got Roles=%v", want, full.Roles)
		}
	}

	// 2. Snapshot minus `user`: the mi-2eg6 regression net. If a
	// future TF change drops `user` from the realm default-roles
	// composite (a regression of mi-rcox), authzUser MUST still
	// inject it so writes do not 403.
	withoutUser := make([]string, 0, len(snap.RealmAccess.Roles))
	for _, r := range snap.RealmAccess.Roles {
		if r != "user" {
			withoutUser = append(withoutUser, r)
		}
	}
	if len(withoutUser) == len(snap.RealmAccess.Roles) {
		t.Fatalf("snapshot did not contain `user` — adjust this test if the snapshot intentionally lacks it")
	}
	fallback := authzUser(auth.User{ID: id, Roles: withoutUser})
	if !slices.Contains(fallback.Roles, "user") {
		t.Errorf("authzUser must inject implicit `user` when realm roles omit it (mi-2eg6 regression); got Roles=%v", fallback.Roles)
	}
}
