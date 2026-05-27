//go:build integration

package incidentregister_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/incidentregister"
)

// registerStore returns a Store backed by an isolated, throwaway schema.
// The register's real deployment uses a SEPARATE database
// (INCIDENT_REGISTER_DATABASE_URL); here a per-test schema gives the same
// isolation for the integration test without provisioning a second DB.
// Crucially the schema is bootstrapped via Store.EnsureSchema — NOT the
// app's migrations/ — exercising the package's own DDL ownership.
func registerStore(t *testing.T) *incidentregister.Store {
	store, _ := registerStoreWithPool(t)
	return store
}

// registerStoreWithPool is registerStore but also hands back the scoped
// pool, for tests that need raw SQL to simulate out-of-band tampering.
func registerStoreWithPool(t *testing.T) (*incidentregister.Store, *pgxpool.Pool) {
	t.Helper()
	rawDSN := os.Getenv("DATABASE_URL")
	if rawDSN == "" {
		t.Skip("DATABASE_URL not set; skipping incident-register integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	schema := "ireg_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]

	admin, err := pgxpool.New(ctx, rawDSN)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(admin.Close)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := admin.Exec(clean, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Logf("drop schema: %v", err)
		}
	})

	scoped, err := pgxpool.ParseConfig(rawDSN)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	scoped.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, scoped)
	if err != nil {
		t.Fatalf("scoped pool: %v", err)
	}
	t.Cleanup(pool.Close)

	store := incidentregister.NewStore(pool)
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	// EnsureSchema is idempotent — a second call on an existing table
	// must not error (it runs on every boot).
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("ensure schema (second call): %v", err)
	}
	return store, pool
}

func sampleIncident(by string) incidentregister.NewIncident {
	return incidentregister.NewIncident{
		PersonalInfoInvolved:      "email addresses and collection notes",
		Circumstances:             "a backup file was emailed to the wrong recipient",
		IncidentOccurredAt:        "2026-05-01",
		BecameAwareDate:           time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		PeopleAffected:            "approximately 12",
		RiskAssessment:            "No serious injury — recipient confirmed deletion",
		CAINotified:               false,
		CAINotifiedDetail:         "low risk; notification not required",
		IndividualsNotified:       true,
		IndividualsNotifiedDetail: "emailed affected users 2026-05-03",
		MeasuresTaken:             "added recipient confirmation step to the export flow",
		RecordedBy:                by,
	}
}

// TestStore_CreateAndRead exercises the full append → read-back path and
// the server-derived metadata (seq, recorded_at, retain_until = aware+5y).
func TestStore_CreateAndRead(t *testing.T) {
	store := registerStore(t)
	ctx := context.Background()

	e, err := store.Create(ctx, sampleIncident("operator-1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if e.Seq != 1 {
		t.Errorf("seq = %d, want 1 (first entry)", e.Seq)
	}
	if e.PrevHash != "GENESIS" {
		t.Errorf("prev_hash = %q, want GENESIS for first entry", e.PrevHash)
	}
	if e.EntryHash == "" {
		t.Error("entry_hash is empty")
	}
	wantRetain := time.Date(2031, 5, 2, 0, 0, 0, 0, time.UTC)
	if !e.RetainUntil.Equal(wantRetain) {
		t.Errorf("retain_until = %v, want %v (became_aware + 5y)", e.RetainUntil, wantRetain)
	}
	if e.RecordedAt.IsZero() {
		t.Error("recorded_at not set")
	}

	got, err := store.GetByID(ctx, e.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.ID != e.ID || got.EntryHash != e.EntryHash {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, e)
	}
	if got.MeasuresTaken != e.MeasuresTaken {
		t.Errorf("measures_taken round-trip: got %q", got.MeasuresTaken)
	}
}

// TestStore_GetByID_NotFound returns ErrNotFound for an unknown id.
func TestStore_GetByID_NotFound(t *testing.T) {
	store := registerStore(t)
	if _, err := store.GetByID(context.Background(), uuid.New()); err == nil {
		t.Fatal("get by id of unknown id: err = nil, want ErrNotFound")
	}
}

// TestStore_HashChainLinks confirms appended entries link into a chain
// (each prev_hash = the prior entry_hash) and that Verify/Export pass on
// the persisted register.
func TestStore_HashChainLinks(t *testing.T) {
	store := registerStore(t)
	ctx := context.Background()

	e1, err := store.Create(ctx, sampleIncident("op-1"))
	if err != nil {
		t.Fatalf("create 1: %v", err)
	}
	e2, err := store.Create(ctx, sampleIncident("op-2"))
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	e3, err := store.Create(ctx, sampleIncident("op-3"))
	if err != nil {
		t.Fatalf("create 3: %v", err)
	}

	if e2.Seq != 2 || e3.Seq != 3 {
		t.Errorf("seqs = %d,%d,%d; want 1,2,3", e1.Seq, e2.Seq, e3.Seq)
	}
	if e2.PrevHash != e1.EntryHash {
		t.Errorf("e2.prev_hash = %q, want e1.entry_hash %q", e2.PrevHash, e1.EntryHash)
	}
	if e3.PrevHash != e2.EntryHash {
		t.Errorf("e3.prev_hash = %q, want e2.entry_hash %q", e3.PrevHash, e2.EntryHash)
	}

	res, err := store.Verify(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.OK || res.Count != 3 {
		t.Errorf("verify = %+v, want OK with Count 3", res)
	}

	exp, err := store.Export(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(exp.Incidents) != 3 || !exp.Integrity.OK {
		t.Errorf("export = %d incidents, integrity OK=%v; want 3 + OK", len(exp.Incidents), exp.Integrity.OK)
	}
}

// TestStore_VerifyDetectsTampering writes a chain, then mutates a row
// directly in the database (the kind of out-of-band edit the hash chain
// exists to catch) and confirms Verify reports the break.
func TestStore_VerifyDetectsTampering(t *testing.T) {
	store, pool := registerStoreWithPool(t)
	ctx := context.Background()

	if _, err := store.Create(ctx, sampleIncident("op-1")); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	e2, err := store.Create(ctx, sampleIncident("op-2"))
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}

	// Reach behind the append-only API and mutate a stored field. This is
	// only possible with raw SQL — the Store offers no Update — which is
	// exactly the scenario Verify must detect.
	if _, err := pool.Exec(ctx,
		"UPDATE confidentiality_incidents SET risk_assessment = $1 WHERE id = $2",
		"falsified", e2.ID); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	res, err := store.Verify(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.OK {
		t.Fatal("verify OK = true after tampering, want false")
	}
	if res.BrokenAtSeq != 2 {
		t.Errorf("broken_at_seq = %d, want 2", res.BrokenAtSeq)
	}
}
