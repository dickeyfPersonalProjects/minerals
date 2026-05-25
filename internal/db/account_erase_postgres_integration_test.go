//go:build integration

package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// seedErasureFixture plants a full ownership graph for userID: two
// specimens (each with one photo+file), one journal entry on the first
// specimen with one file attachment, a collector linked to the first
// specimen, a QR sheet containing the first specimen, a user-authored
// mineral_species, and a share owned by the user. It returns the three
// file s3_keys so the test can assert FreedObjectKeys.
func seedErasureFixture(ctx context.Context, t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) []string {
	t.Helper()
	now := time.Now().UTC()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed exec: %v\nSQL: %s", err, sql)
		}
	}

	spec1, spec2 := domain.NewID(), domain.NewID()
	exec(`INSERT INTO specimens (id, type, name, author_id, type_data, created_at, updated_at)
	      VALUES ($1,'mineral','S1',$3,'{}'::jsonb,$2,$2), ($4,'mineral','S2',$3,'{}'::jsonb,$2,$2)`,
		spec1, now, userID, spec2)

	// One photo (file) per specimen + one journal attachment file = 3 files.
	file1, file2, file3 := domain.NewID(), domain.NewID(), domain.NewID()
	keys := []string{"files/" + file1.String(), "files/" + file2.String(), "files/" + file3.String()}
	insertFile := func(id uuid.UUID, key, ct string) {
		exec(`INSERT INTO files (id, s3_key, content_type, byte_size, sha256, uploaded_by, uploaded_at)
		      VALUES ($1,$2,$3,10,$4,$5,$6)`, id, key, ct, id.String(), userID, now)
	}
	insertFile(file1, keys[0], "image/jpeg")
	insertFile(file2, keys[1], "image/jpeg")
	insertFile(file3, keys[2], "image/png")

	photo1, photo2 := domain.NewID(), domain.NewID()
	exec(`INSERT INTO photos (id, specimen_id, file_id, position, created_at)
	      VALUES ($1,$2,$3,0,$4), ($5,$6,$7,0,$4)`,
		photo1, spec1, file1, now, photo2, spec2, file2)

	entry := domain.NewID()
	exec(`INSERT INTO journal_entries (id, specimen_id, author_id, body_md, created_at, updated_at)
	      VALUES ($1,$2,$3,'note',$4,$4)`, entry, spec1, userID, now)
	exec(`INSERT INTO journal_entry_files (entry_id, file_id, position, created_at)
	      VALUES ($1,$2,0,$3)`, entry, file3, now)

	collector := domain.NewID()
	exec(`INSERT INTO collectors (id, name, author_id, created_at, updated_at)
	      VALUES ($1,$2,$3,$4,$4)`, collector, "C-"+collector.String(), userID, now)
	exec(`INSERT INTO specimen_collectors (specimen_id, collector_id, position, created_at)
	      VALUES ($1,$2,0,$3)`, spec1, collector, now)

	sheet := domain.NewID()
	exec(`INSERT INTO qr_sheets (id, user_id, template, created_at, updated_at)
	      VALUES ($1,$2,'avery-5160',$3,$3)`, sheet, userID, now)
	exec(`INSERT INTO qr_sheet_specimens (id, sheet_id, specimen_id, position, added_at)
	      VALUES ($1,$2,$3,0,$4)`, domain.NewID(), sheet, spec1, now)

	species := domain.NewID()
	exec(`INSERT INTO mineral_species (id, name, source, data, author_id, created_at, updated_at)
	      VALUES ($1,$2,'user','{}'::jsonb,$3,$4,$4)`, species, "Sp-"+species.String(), userID, now)

	exec(`INSERT INTO shares (id, resource_type, resource_id, shared_by, shared_with, created_at)
	      VALUES ($1,'specimen',$2,$3,$4,$5)`, domain.NewID(), spec1, userID, auth.StubUser.ID, now)

	return keys
}

func countWhere(ctx context.Context, t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("count query: %v\nSQL: %s", err, sql)
	}
	return n
}

func TestIntegration_AccountErase_FullCascade(t *testing.T) {
	pool := scopedDB(t)
	ctx := context.Background()

	userID := domain.NewID()
	seedUser(t, pool, userID)
	keys := seedErasureFixture(ctx, t, pool, userID)

	eraser := db.NewAccountErasePostgres(pool)
	res, err := eraser.Erase(ctx, userID)
	if err != nil {
		t.Fatalf("Erase: %v", err)
	}

	// --- audit counts ------------------------------------------------
	if res.Specimens != 2 {
		t.Errorf("Specimens = %d, want 2", res.Specimens)
	}
	if res.Photos != 2 {
		t.Errorf("Photos = %d, want 2", res.Photos)
	}
	if res.JournalEntries != 1 {
		t.Errorf("JournalEntries = %d, want 1", res.JournalEntries)
	}
	if res.Collectors != 1 {
		t.Errorf("Collectors = %d, want 1", res.Collectors)
	}
	if res.Files != 3 {
		t.Errorf("Files = %d, want 3", res.Files)
	}
	if res.QRSheets != 1 {
		t.Errorf("QRSheets = %d, want 1", res.QRSheets)
	}
	if res.ReassignedSpecies != 1 {
		t.Errorf("ReassignedSpecies = %d, want 1", res.ReassignedSpecies)
	}
	if len(res.FreedObjectKeys) != 3 {
		t.Fatalf("FreedObjectKeys = %v, want 3 keys", res.FreedObjectKeys)
	}
	gotKeys := map[string]bool{}
	for _, k := range res.FreedObjectKeys {
		gotKeys[k] = true
	}
	for _, want := range keys {
		if !gotKeys[want] {
			t.Errorf("FreedObjectKeys missing %s (got %v)", want, res.FreedObjectKeys)
		}
	}

	// --- nothing owned by the user remains ---------------------------
	checks := []struct {
		label string
		sql   string
	}{
		{"users", `SELECT count(*) FROM users WHERE id = $1`},
		{"specimens", `SELECT count(*) FROM specimens WHERE author_id = $1`},
		{"photos", `SELECT count(*) FROM photos p JOIN specimens s ON s.id = p.specimen_id WHERE s.author_id = $1`},
		{"journal_entries", `SELECT count(*) FROM journal_entries WHERE author_id = $1`},
		{"files", `SELECT count(*) FROM files WHERE uploaded_by = $1`},
		{"collectors", `SELECT count(*) FROM collectors WHERE author_id = $1`},
		{"qr_sheets", `SELECT count(*) FROM qr_sheets WHERE user_id = $1`},
		{"shares", `SELECT count(*) FROM shares WHERE shared_by = $1 OR shared_with = $1`},
	}
	for _, c := range checks {
		if n := countWhere(ctx, t, pool, c.sql, userID); n != 0 {
			t.Errorf("%s: %d rows remain for erased user, want 0", c.label, n)
		}
	}

	// --- mineral_species reassigned to stub, not deleted -------------
	if n := countWhere(ctx, t, pool,
		`SELECT count(*) FROM mineral_species WHERE author_id = $1`, userID); n != 0 {
		t.Errorf("mineral_species still owned by erased user: %d", n)
	}
	if n := countWhere(ctx, t, pool,
		`SELECT count(*) FROM mineral_species WHERE author_id = $1`, auth.StubUser.ID); n != 1 {
		t.Errorf("mineral_species reassigned to stub = %d, want 1", n)
	}
}

func TestIntegration_AccountErase_StubUserRefused(t *testing.T) {
	pool := scopedDB(t)
	eraser := db.NewAccountErasePostgres(pool)
	_, err := eraser.Erase(context.Background(), auth.StubUser.ID)
	if !errors.Is(err, domain.ErrStubUserUndeletable) {
		t.Fatalf("Erase(stub) err = %v, want ErrStubUserUndeletable", err)
	}
	// The stub row must survive.
	if n := countWhere(context.Background(), t, pool,
		`SELECT count(*) FROM users WHERE id = $1`, auth.StubUser.ID); n != 1 {
		t.Errorf("stub user row count = %d, want 1 (must not be deleted)", n)
	}
}

func TestIntegration_AccountErase_UnknownUser(t *testing.T) {
	pool := scopedDB(t)
	eraser := db.NewAccountErasePostgres(pool)
	_, err := eraser.Erase(context.Background(), domain.NewID())
	if !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("Erase(unknown) err = %v, want ErrUserNotFound", err)
	}
}

func TestIntegration_AccountErase_EmptyAccount(t *testing.T) {
	pool := scopedDB(t)
	ctx := context.Background()
	userID := domain.NewID()
	seedUser(t, pool, userID)

	res, err := db.NewAccountErasePostgres(pool).Erase(ctx, userID)
	if err != nil {
		t.Fatalf("Erase(empty account): %v", err)
	}
	if len(res.FreedObjectKeys) != 0 || res.Specimens != 0 || res.Files != 0 {
		t.Errorf("empty account erasure reported work: %+v", res)
	}
	if n := countWhere(ctx, t, pool, `SELECT count(*) FROM users WHERE id = $1`, userID); n != 0 {
		t.Errorf("user row not deleted: %d", n)
	}
}
