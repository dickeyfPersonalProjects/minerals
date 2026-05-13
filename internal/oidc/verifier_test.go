package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// signer wraps a freshly-generated RSA key with the matching JWKS
// payload the verifier will fetch.
type signer struct {
	key     *rsa.PrivateKey
	keyID   string
	signer  jose.Signer
	jwksRaw []byte
}

func newSigner(t *testing.T) *signer {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	const kid = "test-key-1"
	sig, err := jose.NewSigner(
		jose.SigningKey{
			Algorithm: jose.RS256,
			Key:       jose.JSONWebKey{Key: priv, KeyID: kid},
		},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		t.Fatalf("jose.NewSigner: %v", err)
	}

	jwks := struct {
		Keys []jose.JSONWebKey `json:"keys"`
	}{
		Keys: []jose.JSONWebKey{
			{Key: &priv.PublicKey, KeyID: kid, Algorithm: "RS256", Use: "sig"},
		},
	}
	raw, err := json.Marshal(jwks)
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}

	return &signer{key: priv, keyID: kid, signer: sig, jwksRaw: raw}
}

// claims is the on-wire JWT payload — issuer, audience, expiry plus
// the Keycloak-style realm_access.roles claim.
type tokenClaims struct {
	jwt.Claims
	Email       string `json:"email,omitempty"`
	RealmAccess struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

func (s *signer) issue(t *testing.T, c tokenClaims) string {
	t.Helper()
	tok, err := jwt.Signed(s.signer).Claims(c).Serialize()
	if err != nil {
		t.Fatalf("jwt.Signed: %v", err)
	}
	return tok
}

func startJWKSServer(t *testing.T, s *signer) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(s.jwksRaw)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestVerifier_HappyPath(t *testing.T) {
	s := newSigner(t)
	jwks := startJWKSServer(t, s)

	const issuer = "https://keycloak.example/realms/minerals"
	const clientID = "minerals-api"
	v, err := NewVerifier(context.Background(), Config{
		Issuer:   issuer,
		ClientID: clientID,
		JWKSURL:  jwks.URL,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	now := time.Now()
	c := tokenClaims{
		Claims: jwt.Claims{
			Issuer:   issuer,
			Subject:  "00000000-0000-0000-0000-000000000abc",
			Audience: jwt.Audience{clientID},
			Expiry:   jwt.NewNumericDate(now.Add(time.Minute)),
			IssuedAt: jwt.NewNumericDate(now),
		},
		Email: "fury@minerals.local",
	}
	c.RealmAccess.Roles = []string{"user", "devops-viewer"}

	token := s.issue(t, c)
	claims, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if claims.Subject != c.Subject {
		t.Errorf("Subject: got %q, want %q", claims.Subject, c.Subject)
	}
	if claims.Email != c.Email {
		t.Errorf("Email: got %q, want %q", claims.Email, c.Email)
	}
	if len(claims.Roles) != 2 || claims.Roles[0] != "user" || claims.Roles[1] != "devops-viewer" {
		t.Errorf("Roles: got %v, want [user devops-viewer]", claims.Roles)
	}
}

func TestVerifier_RejectsBadIssuer(t *testing.T) {
	s := newSigner(t)
	jwks := startJWKSServer(t, s)

	v, err := NewVerifier(context.Background(), Config{
		Issuer:   "https://keycloak.example/realms/minerals",
		ClientID: "minerals-api",
		JWKSURL:  jwks.URL,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	now := time.Now()
	c := tokenClaims{
		Claims: jwt.Claims{
			Issuer:   "https://attacker.example/realms/minerals",
			Subject:  "abc",
			Audience: jwt.Audience{"minerals-api"},
			Expiry:   jwt.NewNumericDate(now.Add(time.Minute)),
			IssuedAt: jwt.NewNumericDate(now),
		},
	}
	if _, err := v.Verify(context.Background(), s.issue(t, c)); err == nil {
		t.Fatal("expected error for mismatched issuer")
	}
}

func TestVerifier_RejectsBadAudience(t *testing.T) {
	s := newSigner(t)
	jwks := startJWKSServer(t, s)

	const issuer = "https://keycloak.example/realms/minerals"
	v, err := NewVerifier(context.Background(), Config{
		Issuer:   issuer,
		ClientID: "minerals-api",
		JWKSURL:  jwks.URL,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	now := time.Now()
	c := tokenClaims{
		Claims: jwt.Claims{
			Issuer:   issuer,
			Subject:  "abc",
			Audience: jwt.Audience{"some-other-client"},
			Expiry:   jwt.NewNumericDate(now.Add(time.Minute)),
			IssuedAt: jwt.NewNumericDate(now),
		},
	}
	if _, err := v.Verify(context.Background(), s.issue(t, c)); err == nil {
		t.Fatal("expected error for mismatched audience")
	}
}

func TestVerifier_RejectsExpired(t *testing.T) {
	s := newSigner(t)
	jwks := startJWKSServer(t, s)

	const issuer = "https://keycloak.example/realms/minerals"
	v, err := NewVerifier(context.Background(), Config{
		Issuer:   issuer,
		ClientID: "minerals-api",
		JWKSURL:  jwks.URL,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	past := time.Now().Add(-time.Hour)
	c := tokenClaims{
		Claims: jwt.Claims{
			Issuer:   issuer,
			Subject:  "abc",
			Audience: jwt.Audience{"minerals-api"},
			Expiry:   jwt.NewNumericDate(past),
			IssuedAt: jwt.NewNumericDate(past.Add(-time.Minute)),
		},
	}
	if _, err := v.Verify(context.Background(), s.issue(t, c)); err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestVerifier_RejectsForeignSignature(t *testing.T) {
	good := newSigner(t)
	bad := newSigner(t)
	jwks := startJWKSServer(t, good)

	const issuer = "https://keycloak.example/realms/minerals"
	v, err := NewVerifier(context.Background(), Config{
		Issuer:   issuer,
		ClientID: "minerals-api",
		JWKSURL:  jwks.URL,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	now := time.Now()
	c := tokenClaims{
		Claims: jwt.Claims{
			Issuer:   issuer,
			Subject:  "abc",
			Audience: jwt.Audience{"minerals-api"},
			Expiry:   jwt.NewNumericDate(now.Add(time.Minute)),
			IssuedAt: jwt.NewNumericDate(now),
		},
	}
	token := bad.issue(t, c) // signed by a key the JWKS doesn't publish
	if _, err := v.Verify(context.Background(), token); err == nil {
		t.Fatal("expected error for signature from unknown key")
	}
}

func TestVerifier_EmptyToken(t *testing.T) {
	s := newSigner(t)
	jwks := startJWKSServer(t, s)
	v, err := NewVerifier(context.Background(), Config{
		Issuer:   "https://keycloak.example/realms/minerals",
		ClientID: "minerals-api",
		JWKSURL:  jwks.URL,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if _, err := v.Verify(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestNewVerifier_ConfigValidation(t *testing.T) {
	ctx := context.Background()
	if _, err := NewVerifier(ctx, Config{ClientID: "x", JWKSURL: "http://x"}); err == nil {
		t.Error("missing Issuer: want error")
	}
	if _, err := NewVerifier(ctx, Config{Issuer: "https://x", JWKSURL: "http://x"}); err == nil {
		t.Error("missing ClientID without SkipClientIDCheck: want error")
	}
	if _, err := NewVerifier(ctx, Config{Issuer: "https://x", JWKSURL: "http://x", SkipClientIDCheck: true}); err != nil {
		t.Errorf("SkipClientIDCheck path: %v", err)
	}
}
