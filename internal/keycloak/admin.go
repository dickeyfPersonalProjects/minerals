// Package keycloak holds the minimal Keycloak admin-REST client the
// app needs for GDPR account erasure (mi-nwg5): deleting the
// identity-provider user that backs a deleted application account.
//
// The backend is otherwise a pure OAuth resource server (it validates
// bearer tokens against the realm JWKS and never calls the admin API),
// so this is the only privileged Keycloak surface. It is wired only
// when admin client credentials are configured; otherwise account
// deletion uses the no-op deleter and leaves the IdP user in place
// (see domain.IdentityDeleter).
package keycloak

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2/clientcredentials"
)

// AdminConfig configures the admin client. The service-account behind
// ClientID/ClientSecret must hold the realm-management `manage-users`
// role for the DELETE to succeed.
type AdminConfig struct {
	// BaseURL is the Keycloak server root, e.g. https://kc.example.com
	// (NO /realms/... suffix). Both the token endpoint and the admin
	// API are derived from it.
	BaseURL string
	// Realm is the Keycloak realm the application's users live in,
	// e.g. "minerals".
	Realm string
	// ClientID / ClientSecret authenticate the admin service account
	// via the OAuth client-credentials grant.
	ClientID     string
	ClientSecret string
}

// AdminClient is a domain.IdentityDeleter backed by the Keycloak admin
// REST API. The embedded *http.Client (from clientcredentials) fetches
// and transparently refreshes the service-account access token.
type AdminClient struct {
	httpClient *http.Client
	usersURL   string // {BaseURL}/admin/realms/{realm}/users
	realmURL   string // {BaseURL}/admin/realms/{realm}
	realm      string // the realm name, echoed in update bodies
}

// NewAdminClient builds an AdminClient from cfg. It validates that all
// four fields are present (callers gate on this — an unconfigured admin
// surface wires the no-op deleter instead). Token fetching is lazy
// (first DeleteIdentity call), so construction does not require
// Keycloak to be reachable.
func NewAdminClient(ctx context.Context, cfg AdminConfig) (*AdminClient, error) {
	if cfg.BaseURL == "" || cfg.Realm == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("keycloak: admin config requires base URL, realm, client id, and client secret")
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	realm := url.PathEscape(cfg.Realm)

	ccfg := &clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     base + "/realms/" + realm + "/protocol/openid-connect/token",
	}
	httpClient := ccfg.Client(ctx)
	httpClient.Timeout = 10 * time.Second

	return &AdminClient{
		httpClient: httpClient,
		usersURL:   base + "/admin/realms/" + realm + "/users",
		realmURL:   base + "/admin/realms/" + realm,
		realm:      cfg.Realm,
	}, nil
}

// DeleteIdentity deletes the Keycloak user whose id equals sub (the
// `sub` claim is the admin-API user id in Keycloak). A 404 is treated
// as success — the user is already gone, which is the idempotent
// outcome erasure wants. Any other non-2xx is an error the caller logs
// best-effort.
func (c *AdminClient) DeleteIdentity(ctx context.Context, sub string) error {
	if sub == "" {
		return errors.New("keycloak: empty sub")
	}
	endpoint := c.usersURL + "/" + url.PathEscape(sub)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("keycloak: build delete request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("keycloak: delete user: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil // already absent — idempotent success
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	default:
		return fmt.Errorf("keycloak: delete user: unexpected status %d", resp.StatusCode)
	}
}

// SetRegistrationAllowed flips the realm's `registrationAllowed` flag
// via the admin REST API (PUT /admin/realms/{realm}) so the IdP's
// self-signup gate stays consistent with the application-level toggle
// (mi-pkn2). The service account behind ClientID/ClientSecret must hold
// the realm-management `manage-realm` role for the update to succeed.
//
// The body is a partial RealmRepresentation — Keycloak merges the two
// fields onto the existing realm config, so unrelated realm settings are
// untouched. (`realm` is included because some Keycloak versions reject a
// realm-update body that omits it.)
func (c *AdminClient) SetRegistrationAllowed(ctx context.Context, enabled bool) error {
	payload, err := json.Marshal(struct {
		Realm               string `json:"realm"`
		RegistrationAllowed bool   `json:"registrationAllowed"`
	}{Realm: c.realm, RegistrationAllowed: enabled})
	if err != nil {
		return fmt.Errorf("keycloak: marshal realm update: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.realmURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("keycloak: build realm update request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("keycloak: update realm: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("keycloak: update realm: unexpected status %d", resp.StatusCode)
}

// NoopDeleter is the domain.IdentityDeleter used when admin credentials
// are not configured. DeleteIdentity is a no-op: the application row
// and sessions are already gone, and the orphaned IdP user can only log
// back in to receive a fresh pending row (equivalent to re-registration).
type NoopDeleter struct{}

// DeleteIdentity does nothing and returns nil.
func (NoopDeleter) DeleteIdentity(context.Context, string) error { return nil }
