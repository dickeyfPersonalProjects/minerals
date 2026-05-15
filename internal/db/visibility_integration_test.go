//go:build integration

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// Visibility-scoping integration suite (mi-aw3b / CONTRACT.md §13 v2
// layer 1). Exercises the DB-level list scoping every repo applies
// from auth.FromContext, plus the shares-table lookup the Casbin
// enforcer's `:shared` qualifier resolves through. scopedDB,
// authedCtx and seedUser live in collector_postgres_integration_test.go.

// ctxAs builds a request-scoped context carrying a caller with the
// given application id and realm roles — the integration-tier stand-in
// for a JWT-derived auth.User (the dev-seed.sh Keycloak users, mi-3rq).
func ctxAs(id uuid.UUID, roles ...string) context.Context {
	return auth.WithUser(context.Background(), auth.User{ID: id, Roles: roles})
}

func TestIntegration_Visibility_SpecimenListScope(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewSpecimenPostgres(pool)

	owner := domain.NewID()
	other := domain.NewID()
	admin := domain.NewID()
	seedUser(t, pool, owner)
	seedUser(t, pool, other)
	seedUser(t, pool, admin)

	ownerCtx := ctxAs(owner, "user")
	otherCtx := ctxAs(other, "user")
	adminCtx := ctxAs(admin, "user", "admin")
	anonCtx := ctxAs(uuid.Nil, "anonymous")

	mk := func(ctx context.Context, name string, vis domain.Visibility) uuid.UUID {
		t.Helper()
		s := mkSpecimen(domain.SpecimenMineral, name)
		s.Visibility = vis
		if err := repo.Create(ctx, nil, s); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return s.ID
	}

	pub := mk(ownerCtx, "owner-public", domain.VisibilityPublic)
	unl := mk(ownerCtx, "owner-unlisted", domain.VisibilityUnlisted)
	priv := mk(ownerCtx, "owner-private", domain.VisibilityPrivate)
	otherPriv := mk(otherCtx, "other-private", domain.VisibilityPrivate)

	// other shares its private specimen with owner.
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO shares (id, resource_type, resource_id, shared_by, shared_with)
		 VALUES ($1, 'specimens', $2, $3, $4)`,
		domain.NewID(), otherPriv, other, owner,
	); err != nil {
		t.Fatalf("share specimen: %v", err)
	}

	listIDs := func(ctx context.Context) map[uuid.UUID]bool {
		t.Helper()
		rows, _, err := repo.List(ctx, domain.SpecimenFilter{}, domain.Page{Limit: 100})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		set := make(map[uuid.UUID]bool, len(rows))
		for _, r := range rows {
			set[r.ID] = true
		}
		return set
	}

	cases := []struct {
		name    string
		ctx     context.Context
		want    []uuid.UUID
		notWant []uuid.UUID
	}{
		{
			// owner: own (any visibility) + public + shared.
			name: "owner", ctx: ownerCtx,
			want: []uuid.UUID{pub, unl, priv, otherPriv},
		},
		{
			// other: own + public. owner's unlisted and private are hidden.
			name: "other", ctx: otherCtx,
			want: []uuid.UUID{pub, otherPriv}, notWant: []uuid.UUID{unl, priv},
		},
		{
			// admin: everything.
			name: "admin", ctx: adminCtx,
			want: []uuid.UUID{pub, unl, priv, otherPriv},
		},
		{
			// anonymous: public only — unlisted is excluded from lists.
			name: "anonymous", ctx: anonCtx,
			want: []uuid.UUID{pub}, notWant: []uuid.UUID{unl, priv, otherPriv},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := listIDs(tc.ctx)
			for _, id := range tc.want {
				if !got[id] {
					t.Errorf("%s: expected to see specimen %s", tc.name, id)
				}
			}
			for _, id := range tc.notWant {
				if got[id] {
					t.Errorf("%s: must NOT see specimen %s", tc.name, id)
				}
			}
		})
	}
}

func TestIntegration_Visibility_CollectorListScope(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewCollectorPostgres(pool)

	owner := domain.NewID()
	other := domain.NewID()
	admin := domain.NewID()
	seedUser(t, pool, owner)
	seedUser(t, pool, other)
	seedUser(t, pool, admin)

	mk := func(ctx context.Context, name string) uuid.UUID {
		t.Helper()
		c := mkCollector(name, nil)
		if err := repo.Create(ctx, nil, c); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return c.ID
	}
	ownerC := mk(ctxAs(owner, "user"), "owner-collector")
	otherC := mk(ctxAs(other, "user"), "other-collector")

	listIDs := func(ctx context.Context) map[uuid.UUID]bool {
		t.Helper()
		rows, _, err := repo.List(ctx, domain.CollectorFilter{}, domain.Page{Limit: 100})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		set := make(map[uuid.UUID]bool, len(rows))
		for _, r := range rows {
			set[r.ID] = true
		}
		return set
	}

	// owner sees only their own collector.
	owned := listIDs(ctxAs(owner, "user"))
	if !owned[ownerC] || owned[otherC] {
		t.Errorf("owner: want only ownerC; got %v", owned)
	}
	// admin sees all collectors.
	all := listIDs(ctxAs(admin, "user", "admin"))
	if !all[ownerC] || !all[otherC] {
		t.Errorf("admin: want both collectors; got %v", all)
	}
	// anonymous sees no collectors (no `collectors` policy for anonymous).
	none := listIDs(ctxAs(uuid.Nil, "anonymous"))
	if len(none) != 0 {
		t.Errorf("anonymous: want zero collectors; got %v", none)
	}
}

func TestIntegration_Visibility_JournalListScope(t *testing.T) {
	pool := scopedDB(t)
	specimens := db.NewSpecimenPostgres(pool)
	journal := db.NewJournalEntryPostgres(pool)

	owner := domain.NewID()
	other := domain.NewID()
	admin := domain.NewID()
	seedUser(t, pool, owner)
	seedUser(t, pool, other)
	seedUser(t, pool, admin)
	ownerCtx := ctxAs(owner, "user")

	// A public specimen owned by `owner` with a journal entry owned by
	// `owner`. Journal entries are owner-only even on a public
	// specimen — there is no public or shared tier for journal.
	sp := mkSpecimen(domain.SpecimenMineral, "journal-host")
	sp.Visibility = domain.VisibilityPublic
	if err := specimens.Create(ownerCtx, nil, sp); err != nil {
		t.Fatalf("create specimen: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	entry := domain.JournalEntry{
		ID: domain.NewID(), SpecimenID: sp.ID, BodyMD: "field notes",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := journal.Create(ownerCtx, nil, entry); err != nil {
		t.Fatalf("create journal entry: %v", err)
	}

	count := func(ctx context.Context) int {
		t.Helper()
		rows, _, err := journal.ListBySpecimen(ctx, sp.ID, domain.Page{Limit: 100})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		return len(rows)
	}
	if got := count(ownerCtx); got != 1 {
		t.Errorf("owner: want 1 journal entry, got %d", got)
	}
	if got := count(ctxAs(other, "user")); got != 0 {
		t.Errorf("other: want 0 journal entries on a public specimen, got %d", got)
	}
	if got := count(ctxAs(admin, "user", "admin")); got != 1 {
		t.Errorf("admin: want 1 journal entry, got %d", got)
	}
	if got := count(ctxAs(uuid.Nil, "anonymous")); got != 0 {
		t.Errorf("anonymous: want 0 journal entries, got %d", got)
	}
}

func TestIntegration_Visibility_PhotoListScope(t *testing.T) {
	pool := scopedDB(t)
	specimens := db.NewSpecimenPostgres(pool)
	files := db.NewFilePostgres(pool)
	photos := db.NewPhotoPostgres(pool)

	owner := domain.NewID()
	other := domain.NewID()
	seedUser(t, pool, owner)
	seedUser(t, pool, other)
	ownerCtx := ctxAs(owner, "user")

	mkPhotoOn := func(name string, vis domain.Visibility) uuid.UUID {
		t.Helper()
		sp := mkSpecimen(domain.SpecimenMineral, name)
		sp.Visibility = vis
		if err := specimens.Create(ownerCtx, nil, sp); err != nil {
			t.Fatalf("create specimen %s: %v", name, err)
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		fileID := seedFile(t, ownerCtx, files, "image/jpeg", now)
		photo := domain.Photo{
			ID: domain.NewID(), SpecimenID: sp.ID, FileID: fileID,
			Kind: domain.PhotoKindVisible, Position: 1, CreatedAt: now,
		}
		if err := photos.Create(ownerCtx, nil, photo); err != nil {
			t.Fatalf("create photo on %s: %v", name, err)
		}
		return sp.ID
	}

	pubSpec := mkPhotoOn("photo-public", domain.VisibilityPublic)
	privSpec := mkPhotoOn("photo-private", domain.VisibilityPrivate)

	count := func(ctx context.Context, specimenID uuid.UUID) int {
		t.Helper()
		rows, _, err := photos.ListBySpecimen(ctx, specimenID, domain.Page{Limit: 100})
		if err != nil {
			t.Fatalf("list photos: %v", err)
		}
		return len(rows)
	}

	// Photos inherit the parent specimen's access. A public specimen's
	// photos are visible to anyone; a private specimen's photos only to
	// its owner.
	if got := count(ctxAs(other, "user"), pubSpec); got != 1 {
		t.Errorf("other on public specimen: want 1 photo, got %d", got)
	}
	if got := count(ctxAs(other, "user"), privSpec); got != 0 {
		t.Errorf("other on private specimen: want 0 photos, got %d", got)
	}
	if got := count(ctxAs(uuid.Nil, "anonymous"), pubSpec); got != 1 {
		t.Errorf("anonymous on public specimen: want 1 photo, got %d", got)
	}
	if got := count(ctxAs(uuid.Nil, "anonymous"), privSpec); got != 0 {
		t.Errorf("anonymous on private specimen: want 0 photos, got %d", got)
	}
	if got := count(ownerCtx, privSpec); got != 1 {
		t.Errorf("owner on own private specimen: want 1 photo, got %d", got)
	}
}

func TestIntegration_Visibility_SharesLookup(t *testing.T) {
	pool := scopedDB(t)
	lookup := db.NewSharesLookup(pool)

	owner := domain.NewID()
	grantee := domain.NewID()
	stranger := domain.NewID()
	seedUser(t, pool, owner)
	seedUser(t, pool, grantee)
	seedUser(t, pool, stranger)

	resourceID := domain.NewID()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO shares (id, resource_type, resource_id, shared_by, shared_with)
		 VALUES ($1, 'specimens', $2, $3, $4)`,
		domain.NewID(), resourceID, owner, grantee,
	); err != nil {
		t.Fatalf("insert share: %v", err)
	}

	ctx := context.Background()
	ok, err := lookup(ctx, "specimens", resourceID.String(), grantee.String())
	if err != nil || !ok {
		t.Errorf("grantee lookup: got (%v, %v), want (true, nil)", ok, err)
	}
	ok, err = lookup(ctx, "specimens", resourceID.String(), stranger.String())
	if err != nil || ok {
		t.Errorf("stranger lookup: got (%v, %v), want (false, nil)", ok, err)
	}
	// A malformed id is a clean miss, not an error.
	ok, err = lookup(ctx, "specimens", "not-a-uuid", grantee.String())
	if err != nil || ok {
		t.Errorf("malformed resource id: got (%v, %v), want (false, nil)", ok, err)
	}
}
