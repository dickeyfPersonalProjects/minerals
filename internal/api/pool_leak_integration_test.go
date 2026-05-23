//go:build integration

package api_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/api"
	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/auth/bff"
	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// leakBurst is how many requests each leak-detection subtest fires.
// A per-request connection leak makes pgxpool's AcquiredConns climb
// monotonically and never recover; 200 is comfortably more than the
// pool's MaxConns so a leak of even one conn-per-request would pin the
// pool at its ceiling well before the burst ends.
const leakBurst = 200

// TestIntegration_Pool_NoConnLeak is the regression guard for the
// mi-hkh6 P0 incident (DB connection leak drained the pool → /readyz
// 503 → pods NotReady). It drives a burst of requests through the
// real per-request DB paths and asserts the app pool's in-use
// connection count returns to baseline afterward.
//
// The mechanism it catches: any handler/middleware/repo that acquires
// a pool connection (a pgx.Rows left unclosed, a tx never
// committed/rolled back, a pool.Acquire without Release) and fails to
// return it makes AcquiredConns() ratchet up. After the burst the pool
// must be idle again; if it is not, a connection escaped.
//
// Two paths are exercised:
//   - the BFF session middleware alone (the most-traversed new V2 code,
//     run on EVERY authenticated request), per the bead's required
//     "N requests through the session-middleware path" test, and
//   - the full authenticated read chain (session MW → authz Enforce →
//     shares lookup → per-field redaction → specimen list), which is
//     the path the SpecimenCard fan-out actually amplifies.
func TestIntegration_Pool_NoConnLeak(t *testing.T) {
	pool := scopedDB(t)
	users := db.NewUserPostgres(pool)
	sessions := bff.NewPostgresResolver(pool)

	ctx := context.Background()
	owner := mustActiveUser(t, ctx, users, "leak-owner-sub", "leak-owner@example.invalid")
	cookie := mustSessionCookie(t, ctx, sessions, owner)

	t.Run("session-middleware", func(t *testing.T) {
		mw := bff.SessionMiddleware(bff.MiddlewareDeps{
			Sessions: sessions,
			OAuth:    &stubOAuthClient{},
			Users:    users,
			CookieConfig: bff.CookieConfig{
				Path: "/", SameSite: http.SameSiteLaxMode, MaxAge: time.Hour,
			},
			IdleTimeout: 24 * time.Hour,
		})
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		srv := httptest.NewServer(mw(next))
		t.Cleanup(srv.Close)

		assertNoLeak(t, pool, func() {
			driveBurst(t, srv.URL+"/", cookie)
		})
	})

	t.Run("full-read-chain", func(t *testing.T) {
		specimens := db.NewSpecimenPostgres(pool)
		other := mustActiveUser(t, ctx, users, "leak-other-sub", "leak-other@example.invalid")

		// A spread of visibilities so redaction + the shared-instance
		// DB lookup in authz.Enforce both run on the list path.
		seedLeakSpecimen(t, ctx, specimens, owner.ID, "own-private", domain.VisibilityPrivate)
		seedLeakSpecimen(t, ctx, specimens, other.ID, "other-public", domain.VisibilityPublic)
		sharedID := seedLeakSpecimen(t, ctx, specimens, other.ID, "other-shared", domain.VisibilityPrivate)
		if _, err := pool.Exec(ctx,
			`INSERT INTO shares (id, resource_type, resource_id, shared_by, shared_with, created_at)
			 VALUES ($1, 'specimens', $2, $3, $4, now())`,
			uuid.New(), sharedID, other.ID, owner.ID); err != nil {
			t.Fatalf("seed share: %v", err)
		}

		enforcer, err := authz.NewEnforcer(nil, db.NewSharesLookup(pool))
		if err != nil {
			t.Fatalf("new enforcer: %v", err)
		}
		if err := authz.SeedDefaultPolicies(enforcer); err != nil {
			t.Fatalf("seed policies: %v", err)
		}

		sessionMW := bff.SessionMiddleware(bff.MiddlewareDeps{
			Sessions: sessions,
			OAuth:    &stubOAuthClient{},
			Users:    users,
			CookieConfig: bff.CookieConfig{
				Path: "/", SameSite: http.SameSiteLaxMode, MaxAge: time.Hour,
			},
			IdleTimeout: 24 * time.Hour,
		})
		h := api.New(api.Deps{
			Specimens: specimens,
			Enforcer:  enforcer,
			Users:     users,
			Verifier:  rejectingVerifier{},
			SessionMW: sessionMW,
			CSRFMW:    bff.CSRFMiddleware,
		})
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)

		assertNoLeak(t, pool, func() {
			driveBurst(t, srv.URL+"/api/v1/specimens", cookie)
		})
	})
}

// assertNoLeak records the pool's in-use connection count, runs fn,
// then (after a short settle for async release) asserts the count did
// not climb. A one-conn tolerance absorbs an in-flight idle/health
// connection without masking a real per-request leak.
func assertNoLeak(t *testing.T, pool *pgxpool.Pool, fn func()) {
	t.Helper()
	baseline := pool.Stat().AcquiredConns()

	fn()

	// pgxpool releases a connection slightly after the request goroutine
	// returns; give async release a beat before sampling.
	time.Sleep(300 * time.Millisecond)
	after := pool.Stat().AcquiredConns()
	if after > baseline+1 {
		t.Errorf("connection leak: AcquiredConns climbed from %d to %d over %d requests "+
			"(in-use connections did not return to the pool)", baseline, after, leakBurst)
	}
}

// driveBurst fires leakBurst sequential authenticated GETs at url and
// fails on the first non-200.
func driveBurst(t *testing.T, url string, cookie *http.Cookie) {
	t.Helper()
	client := &http.Client{}
	for i := 0; i < leakBurst; i++ {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("req %d: build: %v", i, err)
		}
		req.AddCookie(cookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("req %d: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("req %d: status %d, want 200", i, resp.StatusCode)
		}
	}
}

// mustActiveUser resolve-or-creates a user and marks it active so the
// requireCompleteProfile gate lets authenticated reads through.
func mustActiveUser(t *testing.T, ctx context.Context, users *db.UserPostgres, sub, email string) domain.User {
	t.Helper()
	u, err := api.ResolveOrCreateUser(ctx, users, auth.User{Sub: sub, Email: email})
	if err != nil {
		t.Fatalf("resolve user %q: %v", sub, err)
	}
	if u.Status == domain.UserStatusPending {
		now := time.Now().UTC().Truncate(time.Microsecond)
		if err := users.MarkActive(ctx, nil, u.ID, "leak-test", now); err != nil {
			t.Fatalf("mark active %q: %v", sub, err)
		}
		u.Status = domain.UserStatusActive
	}
	return u
}

// mustSessionCookie creates a live session for u and returns the
// session cookie the middleware expects (base64url of the 32-byte id).
func mustSessionCookie(t *testing.T, ctx context.Context, sessions *bff.PostgresResolver, u domain.User) *http.Cookie {
	t.Helper()
	sess, err := sessions.Create(ctx, bff.CreateParams{
		UserSub:               u.KeycloakSub,
		UserID:                u.ID,
		AccessToken:           "at",
		RefreshToken:          "rt",
		IDToken:               "it",
		AccessTokenExpiresAt:  time.Now().Add(time.Hour),
		RefreshTokenExpiresAt: time.Now().Add(24 * time.Hour),
		AbsoluteExpiresAt:     time.Now().Add(7 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return &http.Cookie{
		Name:  bff.SessionCookieName,
		Value: base64.RawURLEncoding.EncodeToString(sess.ID[:]),
	}
}

// seedSpecimen inserts a minimal specimen owned by authorID with the
// given visibility and returns its id. Author is taken from the
// request context (SpecimenPostgres.Create reads auth.FromContext).
func seedLeakSpecimen(t *testing.T, ctx context.Context, specimens *db.SpecimenPostgres, authorID uuid.UUID, name string, vis domain.Visibility) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	authCtx := auth.WithUser(ctx, auth.User{ID: authorID})
	if err := specimens.Create(authCtx, nil, domain.Specimen{
		ID:         id,
		Type:       domain.SpecimenMineral,
		Name:       name,
		Visibility: vis,
		TypeData:   []byte(`{}`),
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("seed specimen %q: %v", name, err)
	}
	return id
}
