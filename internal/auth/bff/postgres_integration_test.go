//go:build integration

package bff_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth/bff"
)

// TestPostgresResolver_RoundTrip exercises the full Create →
// GetByID → UpdateTokens → Touch → Revoke lifecycle the design doc
// describes for the BFF session machinery. Every assertion is on
// fields the middleware (mi-ken4) reads, so a regression in any of
// these breaks production auth long before unit tests would catch
// it.
//
// Reuses the per-test schema scaffolding from
// cleanup_integration_test.go (setupPool) so the migration chain —
// including 0015_auth_sessions — applies in isolation per test.
func TestPostgresResolver_RoundTrip(t *testing.T) {
	pool := setupPool(t)
	r := bff.NewPostgresResolver(pool)
	ctx := context.Background()

	userID := uuid.New()
	t.Cleanup(func() {
		drop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(drop, `DELETE FROM auth.sessions WHERE user_id = $1`, userID)
	})

	now := time.Now().UTC().Truncate(time.Microsecond)
	params := bff.CreateParams{
		UserSub:               "kc|" + userID.String(),
		UserID:                userID,
		AccessToken:           "access-1",
		RefreshToken:          "refresh-1",
		IDToken:               "id-1",
		AccessTokenExpiresAt:  now.Add(5 * time.Minute),
		RefreshTokenExpiresAt: now.Add(30 * 24 * time.Hour),
		AbsoluteExpiresAt:     now.Add(7 * 24 * time.Hour),
		IP:                    netip.MustParseAddr("203.0.113.7"),
		UserAgent:             "Mozilla/5.0 (test)",
	}

	// --- Create ---------------------------------------------------
	created, err := r.Create(ctx, params)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == ([32]byte{}) {
		t.Errorf("Created.ID is zero — Create did not generate a session id")
	}
	if created.CSRFToken == ([32]byte{}) {
		t.Errorf("Created.CSRFToken is zero — Create did not generate a CSRF token")
	}
	if created.UserSub != params.UserSub || created.UserID != params.UserID {
		t.Errorf("user identity not persisted: got sub=%q user_id=%v",
			created.UserSub, created.UserID)
	}
	if created.AccessToken != "access-1" || created.RefreshToken != "refresh-1" ||
		created.IDToken != "id-1" {
		t.Errorf("tokens not persisted: %+v", created)
	}
	if !created.IP.IsValid() || created.IP.String() != "203.0.113.7" {
		t.Errorf("IP = %v, want 203.0.113.7", created.IP)
	}
	if created.UserAgent != "Mozilla/5.0 (test)" {
		t.Errorf("UserAgent = %q, want Mozilla/5.0 (test)", created.UserAgent)
	}
	if created.RevokedAt != nil {
		t.Errorf("RevokedAt = %v, want nil on Create", created.RevokedAt)
	}
	if created.CreatedAt.IsZero() || created.LastUsedAt.IsZero() {
		t.Errorf("CreatedAt/LastUsedAt zero; DB DEFAULTs not applied: %+v", created)
	}

	// --- GetByID --------------------------------------------------
	fetched, err := r.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if fetched.ID != created.ID {
		t.Errorf("GetByID id = %x, want %x", fetched.ID, created.ID)
	}
	if fetched.AccessToken != "access-1" {
		t.Errorf("GetByID access_token = %q, want access-1", fetched.AccessToken)
	}

	// --- UpdateTokens (refresh rotation) --------------------------
	newAccessExp := now.Add(10 * time.Minute)
	newRefreshExp := now.Add(31 * 24 * time.Hour)
	updated, err := r.UpdateTokens(ctx, created.ID, bff.TokenSet{
		AccessToken:           "access-2",
		RefreshToken:          "refresh-2-rotated",
		IDToken:               "id-2",
		AccessTokenExpiresAt:  newAccessExp,
		RefreshTokenExpiresAt: newRefreshExp,
	})
	if err != nil {
		t.Fatalf("UpdateTokens: %v", err)
	}
	if updated.AccessToken != "access-2" || updated.RefreshToken != "refresh-2-rotated" ||
		updated.IDToken != "id-2" {
		t.Errorf("UpdateTokens did not persist new tokens: %+v", updated)
	}
	if !updated.AccessTokenExpiresAt.Equal(newAccessExp) {
		t.Errorf("AccessTokenExpiresAt = %v, want %v", updated.AccessTokenExpiresAt, newAccessExp)
	}
	if !updated.RefreshTokenExpiresAt.Equal(newRefreshExp) {
		t.Errorf("RefreshTokenExpiresAt = %v, want %v", updated.RefreshTokenExpiresAt, newRefreshExp)
	}

	// --- Touch ----------------------------------------------------
	touchAt := now.Add(time.Hour).UTC().Truncate(time.Microsecond)
	if err := r.Touch(ctx, created.ID, touchAt); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	touched, err := r.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID after Touch: %v", err)
	}
	if !touched.LastUsedAt.Equal(touchAt) {
		t.Errorf("LastUsedAt after Touch = %v, want %v", touched.LastUsedAt, touchAt)
	}

	// --- Revoke (sets revoked_at; row remains visible) ------------
	if err := r.Revoke(ctx, created.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	revoked, err := r.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID after Revoke: %v (expected the row to remain readable; "+
			"middleware decides liveness, not the resolver)", err)
	}
	if revoked.RevokedAt == nil {
		t.Errorf("RevokedAt nil after Revoke; want non-nil")
	}
}

// TestPostgresResolver_GetByID_NotFound asserts the ErrSessionNotFound
// sentinel for a missing row. The middleware errors.Is against it to
// decide between "clear cookie and proceed anonymously" (sentinel)
// and "500" (anything else).
func TestPostgresResolver_GetByID_NotFound(t *testing.T) {
	pool := setupPool(t)
	r := bff.NewPostgresResolver(pool)

	var unknown [32]byte
	for i := range unknown {
		unknown[i] = byte(i + 1)
	}
	_, err := r.GetByID(context.Background(), unknown)
	if !errors.Is(err, bff.ErrSessionNotFound) {
		t.Errorf("GetByID(unknown) = %v, want ErrSessionNotFound", err)
	}
}

// TestPostgresResolver_RevokeAllForUser revokes two of a user's
// three sessions (the third is already revoked, so the helper must
// not re-touch it and reset its cleanup-window anchor).
func TestPostgresResolver_RevokeAllForUser(t *testing.T) {
	pool := setupPool(t)
	r := bff.NewPostgresResolver(pool)
	ctx := context.Background()

	userID := uuid.New()
	t.Cleanup(func() {
		drop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(drop, `DELETE FROM auth.sessions WHERE user_id = $1`, userID)
	})

	now := time.Now().UTC()
	baseParams := bff.CreateParams{
		UserSub:               "kc|" + userID.String(),
		UserID:                userID,
		AccessToken:           "a",
		RefreshToken:          "r",
		IDToken:               "i",
		AccessTokenExpiresAt:  now.Add(time.Hour),
		RefreshTokenExpiresAt: now.Add(24 * time.Hour),
		AbsoluteExpiresAt:     now.Add(7 * 24 * time.Hour),
	}
	s1, err := r.Create(ctx, baseParams)
	if err != nil {
		t.Fatalf("Create s1: %v", err)
	}
	s2, err := r.Create(ctx, baseParams)
	if err != nil {
		t.Fatalf("Create s2: %v", err)
	}
	s3, err := r.Create(ctx, baseParams)
	if err != nil {
		t.Fatalf("Create s3: %v", err)
	}
	// Pre-revoke s3 so we can later check that its revoked_at
	// wasn't bumped by RevokeAllForUser.
	if err := r.Revoke(ctx, s3.ID); err != nil {
		t.Fatalf("pre-revoke s3: %v", err)
	}
	preRevoked, _ := r.GetByID(ctx, s3.ID)
	if preRevoked.RevokedAt == nil {
		t.Fatalf("s3 not pre-revoked")
	}
	preRevokedAt := *preRevoked.RevokedAt

	// Sleep briefly so a re-revoke would land at a measurably
	// different revoked_at — without this, the assertion below
	// would be racy on fast machines.
	time.Sleep(20 * time.Millisecond)

	if err := r.RevokeAllForUser(ctx, userID); err != nil {
		t.Fatalf("RevokeAllForUser: %v", err)
	}

	g1, _ := r.GetByID(ctx, s1.ID)
	g2, _ := r.GetByID(ctx, s2.ID)
	g3, _ := r.GetByID(ctx, s3.ID)
	if g1.RevokedAt == nil {
		t.Errorf("s1 not revoked after RevokeAllForUser")
	}
	if g2.RevokedAt == nil {
		t.Errorf("s2 not revoked after RevokeAllForUser")
	}
	if g3.RevokedAt == nil || !g3.RevokedAt.Equal(preRevokedAt) {
		t.Errorf("s3 RevokedAt mutated: was %v, now %v (RevokeAllForUser must skip already-revoked rows)",
			preRevokedAt, g3.RevokedAt)
	}
}

// TestPostgresResolver_UpdateTokens_NotFound asserts the sentinel
// also surfaces from UpdateTokens — the middleware needs to handle
// "session disappeared between the lookup and the refresh" (rare,
// but possible with admin force-logout).
func TestPostgresResolver_UpdateTokens_NotFound(t *testing.T) {
	pool := setupPool(t)
	r := bff.NewPostgresResolver(pool)

	var unknown [32]byte
	unknown[0] = 0xFF
	_, err := r.UpdateTokens(context.Background(), unknown, bff.TokenSet{
		AccessToken:           "a",
		RefreshToken:          "r",
		IDToken:               "i",
		AccessTokenExpiresAt:  time.Now().Add(time.Hour),
		RefreshTokenExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if !errors.Is(err, bff.ErrSessionNotFound) {
		t.Errorf("UpdateTokens(unknown) = %v, want ErrSessionNotFound", err)
	}
}
