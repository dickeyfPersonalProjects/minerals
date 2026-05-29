package keycloak

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// newKeycloakStub returns an httptest server that emulates the two
// Keycloak endpoints the admin client touches: the client-credentials
// token endpoint and the admin users-delete endpoint. deleteStatus is
// the status the DELETE returns; the captured delete path is recorded.
func newKeycloakStub(t *testing.T, realm string, deleteStatus int) (*httptest.Server, *atomic.Pointer[string]) {
	t.Helper()
	var deletedPath atomic.Pointer[string]
	tokenPath := "/realms/" + realm + "/protocol/openid-connect/token"
	usersPrefix := "/admin/realms/" + realm + "/users/"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == tokenPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"stub-token","token_type":"Bearer","expires_in":300}`))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, usersPrefix):
			if got := r.Header.Get("Authorization"); got != "Bearer stub-token" {
				t.Errorf("admin DELETE missing bearer token; got %q", got)
			}
			p := r.URL.Path
			deletedPath.Store(&p)
			w.WriteHeader(deleteStatus)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &deletedPath
}

func newTestAdmin(t *testing.T, baseURL, realm string) *AdminClient {
	t.Helper()
	c, err := NewAdminClient(context.Background(), AdminConfig{
		BaseURL:      baseURL,
		Realm:        realm,
		ClientID:     "admin-cli",
		ClientSecret: "secret",
	})
	if err != nil {
		t.Fatalf("NewAdminClient: %v", err)
	}
	return c
}

func TestNewAdminClient_RequiresAllFields(t *testing.T) {
	t.Parallel()
	cases := []AdminConfig{
		{Realm: "r", ClientID: "c", ClientSecret: "s"},   // no base url
		{BaseURL: "u", ClientID: "c", ClientSecret: "s"}, // no realm
		{BaseURL: "u", Realm: "r", ClientSecret: "s"},    // no client id
		{BaseURL: "u", Realm: "r", ClientID: "c"},        // no secret
	}
	for i, cfg := range cases {
		if _, err := NewAdminClient(context.Background(), cfg); err == nil {
			t.Errorf("case %d: expected error for incomplete config", i)
		}
	}
}

func TestDeleteIdentity_Success(t *testing.T) {
	t.Parallel()
	const realm = "minerals"
	srv, deleted := newKeycloakStub(t, realm, http.StatusNoContent)
	admin := newTestAdmin(t, srv.URL, realm)

	if err := admin.DeleteIdentity(context.Background(), "sub-123"); err != nil {
		t.Fatalf("DeleteIdentity: %v", err)
	}
	want := "/admin/realms/" + realm + "/users/sub-123"
	if got := deleted.Load(); got == nil || *got != want {
		t.Errorf("deleted path = %v, want %s", got, want)
	}
}

func TestDeleteIdentity_NotFoundIsIdempotentSuccess(t *testing.T) {
	t.Parallel()
	const realm = "minerals"
	srv, _ := newKeycloakStub(t, realm, http.StatusNotFound)
	admin := newTestAdmin(t, srv.URL, realm)

	if err := admin.DeleteIdentity(context.Background(), "already-gone"); err != nil {
		t.Errorf("404 should be treated as success, got %v", err)
	}
}

func TestDeleteIdentity_ServerErrorSurfaces(t *testing.T) {
	t.Parallel()
	const realm = "minerals"
	srv, _ := newKeycloakStub(t, realm, http.StatusInternalServerError)
	admin := newTestAdmin(t, srv.URL, realm)

	if err := admin.DeleteIdentity(context.Background(), "sub-x"); err == nil {
		t.Error("expected error on 500 from admin API")
	}
}

func TestDeleteIdentity_EmptySubRejected(t *testing.T) {
	t.Parallel()
	admin := newTestAdmin(t, "http://example.invalid", "minerals")
	if err := admin.DeleteIdentity(context.Background(), ""); err == nil {
		t.Error("expected error for empty sub")
	}
}

func TestNoopDeleter(t *testing.T) {
	t.Parallel()
	if err := (NoopDeleter{}).DeleteIdentity(context.Background(), "anything"); err != nil {
		t.Errorf("NoopDeleter must never error, got %v", err)
	}
}

// newUserUpdateStub emulates the token endpoint plus the admin
// PUT-user endpoint SetIdentityEnabled targets. It records the captured
// PUT path and raw body, and returns putStatus for the PUT.
func newUserUpdateStub(t *testing.T, realm string, putStatus int) (*httptest.Server, *atomic.Pointer[string], *atomic.Pointer[string]) {
	t.Helper()
	var putPath, putBody atomic.Pointer[string]
	tokenPath := "/realms/" + realm + "/protocol/openid-connect/token"
	usersPrefix := "/admin/realms/" + realm + "/users/"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == tokenPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"stub-token","token_type":"Bearer","expires_in":300}`))
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, usersPrefix):
			if got := r.Header.Get("Authorization"); got != "Bearer stub-token" {
				t.Errorf("admin PUT missing bearer token; got %q", got)
			}
			buf := new(strings.Builder)
			_, _ = io.Copy(buf, r.Body)
			p, b := r.URL.Path, buf.String()
			putPath.Store(&p)
			putBody.Store(&b)
			w.WriteHeader(putStatus)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &putPath, &putBody
}

func TestSetIdentityEnabled_DisablePutsEnabledFalse(t *testing.T) {
	t.Parallel()
	const realm = "minerals"
	srv, putPath, putBody := newUserUpdateStub(t, realm, http.StatusNoContent)
	admin := newTestAdmin(t, srv.URL, realm)

	if err := admin.SetIdentityEnabled(context.Background(), "sub-123", false); err != nil {
		t.Fatalf("SetIdentityEnabled: %v", err)
	}
	wantPath := "/admin/realms/" + realm + "/users/sub-123"
	if got := putPath.Load(); got == nil || *got != wantPath {
		t.Errorf("put path = %v, want %s", got, wantPath)
	}
	if got := putBody.Load(); got == nil || !strings.Contains(*got, `"enabled":false`) {
		t.Errorf("put body = %v, want it to contain \"enabled\":false", got)
	}
}

func TestSetIdentityEnabled_EnablePutsEnabledTrue(t *testing.T) {
	t.Parallel()
	const realm = "minerals"
	srv, _, putBody := newUserUpdateStub(t, realm, http.StatusNoContent)
	admin := newTestAdmin(t, srv.URL, realm)

	if err := admin.SetIdentityEnabled(context.Background(), "sub-123", true); err != nil {
		t.Fatalf("SetIdentityEnabled: %v", err)
	}
	if got := putBody.Load(); got == nil || !strings.Contains(*got, `"enabled":true`) {
		t.Errorf("put body = %v, want it to contain \"enabled\":true", got)
	}
}

func TestSetIdentityEnabled_ServerErrorSurfaces(t *testing.T) {
	t.Parallel()
	const realm = "minerals"
	srv, _, _ := newUserUpdateStub(t, realm, http.StatusNotFound)
	admin := newTestAdmin(t, srv.URL, realm)

	if err := admin.SetIdentityEnabled(context.Background(), "sub-x", false); err == nil {
		t.Error("expected error when the admin API returns 404 (target not found)")
	}
}

func TestSetIdentityEnabled_EmptySubRejected(t *testing.T) {
	t.Parallel()
	admin := newTestAdmin(t, "http://example.invalid", "minerals")
	if err := admin.SetIdentityEnabled(context.Background(), "", false); err == nil {
		t.Error("expected error for empty sub")
	}
}
