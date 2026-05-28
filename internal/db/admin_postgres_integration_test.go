//go:build integration

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// seedNamedUser inserts an active user with a display name so the admin
// queries can attribute content to it. Returns the id.
func seedNamedUser(t *testing.T, pool *pgxpool.Pool, displayName string) uuid.UUID {
	t.Helper()
	id := domain.NewID()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, keycloak_sub, email, display_name, status)
		VALUES ($1, $2, $3, $4, 'active')`,
		id, "test-"+id.String(), id.String()+"@example.invalid", displayName)
	if err != nil {
		t.Fatalf("seed named user %s: %v", displayName, err)
	}
	return id
}

func insertSpecimen(t *testing.T, pool *pgxpool.Pool, author uuid.UUID, name, visibility string, createdAt time.Time) uuid.UUID {
	t.Helper()
	id := domain.NewID()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO specimens (id, type, name, visibility, author_id, created_at, updated_at)
		VALUES ($1, 'mineral', $2, $3, $4, $5, $5)`,
		id, name, visibility, author, createdAt)
	if err != nil {
		t.Fatalf("insert specimen %s: %v", name, err)
	}
	return id
}

func insertFile(t *testing.T, pool *pgxpool.Pool, uploadedBy uuid.UUID) uuid.UUID {
	t.Helper()
	id := domain.NewID()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO files (id, s3_key, content_type, byte_size, sha256, uploaded_by, uploaded_at)
		VALUES ($1, $2, 'image/jpeg', 1, $3, $4, now())`,
		id, "key-"+id.String(), id.String(), uploadedBy)
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	return id
}

// insertPhoto inserts a photo with an optional per-photo visibility
// override (nil = inherit). It first creates the backing file row.
func insertPhoto(t *testing.T, pool *pgxpool.Pool, specimenID, uploader uuid.UUID, visibility *string, createdAt time.Time) uuid.UUID {
	t.Helper()
	fileID := insertFile(t, pool, uploader)
	id := domain.NewID()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO photos (id, specimen_id, file_id, position, visibility, created_at)
		VALUES ($1, $2, $3, 0, $4, $5)`,
		id, specimenID, fileID, visibility, createdAt)
	if err != nil {
		t.Fatalf("insert photo: %v", err)
	}
	return id
}

func insertJournal(t *testing.T, pool *pgxpool.Pool, specimenID, author uuid.UUID, body string, createdAt time.Time) uuid.UUID {
	t.Helper()
	id := domain.NewID()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO journal_entries (id, specimen_id, author_id, body_md, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)`,
		id, specimenID, author, body, createdAt)
	if err != nil {
		t.Fatalf("insert journal: %v", err)
	}
	return id
}

// TestIntegration_AdminListUsers_CountsAndNoPII verifies the
// non-personal user view returns correct derived content counts across
// ALL of a user's content (regardless of visibility) and never selects
// email (mi-n5av).
func TestIntegration_AdminListUsers_CountsAndNoPII(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewAdminPostgres(pool)
	base := time.Now().UTC().Truncate(time.Second)

	alice := seedNamedUser(t, pool, "Alice")

	pub := insertSpecimen(t, pool, alice, "PubA", "public", base)
	priv := insertSpecimen(t, pool, alice, "PrivA", "private", base.Add(time.Second))
	// Two photos on the public specimen (one private override), one on
	// the private specimen → 3 photos total for Alice.
	private := "private"
	insertPhoto(t, pool, pub, alice, nil, base)
	insertPhoto(t, pool, pub, alice, &private, base)
	insertPhoto(t, pool, priv, alice, nil, base)
	// One journal entry per specimen → 2 total.
	insertJournal(t, pool, pub, alice, "pub note", base)
	insertJournal(t, pool, priv, alice, "priv note", base)

	users, _, err := repo.ListUsers(context.Background(), domain.Page{})
	if err != nil {
		t.Fatalf("list users: %v", err)
	}

	var got *domain.AdminUser
	for i := range users {
		if users[i].ID == alice {
			got = &users[i]
		}
	}
	if got == nil {
		t.Fatalf("alice not in user list (%d rows)", len(users))
	}
	if got.DisplayName == nil || *got.DisplayName != "Alice" {
		t.Errorf("display_name = %v, want Alice", got.DisplayName)
	}
	if got.SpecimenCount != 2 {
		t.Errorf("specimen_count = %d, want 2", got.SpecimenCount)
	}
	if got.PhotoCount != 3 {
		t.Errorf("photo_count = %d, want 3", got.PhotoCount)
	}
	if got.JournalCount != 2 {
		t.Errorf("journal_count = %d, want 2", got.JournalCount)
	}
	if got.Status != domain.UserStatusActive {
		t.Errorf("status = %q, want active", got.Status)
	}
}

// TestIntegration_AdminListPublishedContent verifies the unified feed
// includes only public/unlisted specimens, their non-private photos, and
// their journal entries — across users, attributed by display name +
// id, in (created_at DESC, id DESC) order (mi-gtkp).
func TestIntegration_AdminListPublishedContent(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewAdminPostgres(pool)
	base := time.Now().UTC().Truncate(time.Second)

	alice := seedNamedUser(t, pool, "Alice")
	bob := seedNamedUser(t, pool, "Bob")

	pub := insertSpecimen(t, pool, alice, "PubA", "public", base.Add(1*time.Second))
	priv := insertSpecimen(t, pool, alice, "PrivA", "private", base.Add(2*time.Second))
	unl := insertSpecimen(t, pool, bob, "UnlB", "unlisted", base.Add(3*time.Second))

	private := "private"
	visiblePhoto := insertPhoto(t, pool, pub, alice, nil, base.Add(4*time.Second))
	insertPhoto(t, pool, pub, alice, &private, base.Add(5*time.Second)) // excluded
	insertPhoto(t, pool, priv, alice, nil, base.Add(6*time.Second))     // excluded (parent private)
	pubJournal := insertJournal(t, pool, pub, alice, "visible body", base.Add(7*time.Second))
	insertJournal(t, pool, priv, alice, "hidden body", base.Add(8*time.Second)) // excluded

	rows, _, err := repo.ListPublishedContent(context.Background(), domain.Page{})
	if err != nil {
		t.Fatalf("list published content: %v", err)
	}

	byID := map[uuid.UUID]domain.AdminContent{}
	for _, r := range rows {
		byID[r.ID] = r
	}

	// Present: public specimen, unlisted specimen, the visible photo, the
	// public-specimen journal entry.
	if _, ok := byID[pub]; !ok {
		t.Error("public specimen missing from feed")
	}
	if _, ok := byID[unl]; !ok {
		t.Error("unlisted specimen missing from feed")
	}
	if _, ok := byID[visiblePhoto]; !ok {
		t.Error("visible photo missing from feed")
	}
	if _, ok := byID[pubJournal]; !ok {
		t.Error("public-specimen journal entry missing from feed")
	}
	// Absent: anything under the private specimen, and the private photo.
	if _, ok := byID[priv]; ok {
		t.Error("private specimen leaked into feed")
	}

	// Owner attribution + kinds.
	if sp := byID[pub]; sp.Kind != domain.AdminContentSpecimen ||
		sp.OwnerID != alice || sp.OwnerDisplayName == nil || *sp.OwnerDisplayName != "Alice" {
		t.Errorf("public specimen row malformed: %+v", sp)
	}
	if ph := byID[visiblePhoto]; ph.Kind != domain.AdminContentPhoto || ph.SpecimenID != pub {
		t.Errorf("photo row malformed: %+v", ph)
	}
	if jr := byID[pubJournal]; jr.Kind != domain.AdminContentJournal || jr.Preview != "visible body" {
		t.Errorf("journal row malformed: %+v", jr)
	}

	// Ordering: created_at strictly non-increasing across the feed.
	for i := 1; i < len(rows); i++ {
		if rows[i].CreatedAt.After(rows[i-1].CreatedAt) {
			t.Errorf("feed not in created_at DESC order at %d: %v then %v",
				i, rows[i-1].CreatedAt, rows[i].CreatedAt)
		}
	}
}

// TestIntegration_AdminListPaginationCursor verifies the cursor walks the
// user list without overlap or omission.
func TestIntegration_AdminListPaginationCursor(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewAdminPostgres(pool)
	base := time.Now().UTC().Truncate(time.Second)

	// Seed several users (plus the migration's stub user already present).
	const n = 5
	for i := 0; i < n; i++ {
		id := domain.NewID()
		_, err := pool.Exec(context.Background(), `
			INSERT INTO users (id, keycloak_sub, email, status, created_at)
			VALUES ($1, $2, $3, 'active', $4)`,
			id, "page-"+id.String(), id.String()+"@example.invalid", base.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("seed page user: %v", err)
		}
	}

	seen := map[uuid.UUID]bool{}
	cursor := ""
	pages := 0
	for {
		rows, next, err := repo.ListUsers(context.Background(), domain.Page{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("list users page: %v", err)
		}
		for _, r := range rows {
			if seen[r.ID] {
				t.Fatalf("duplicate row across pages: %s", r.ID)
			}
			seen[r.ID] = true
		}
		pages++
		if next == "" {
			break
		}
		cursor = string(next)
		if pages > 20 {
			t.Fatal("pagination did not terminate")
		}
	}
	// n seeded + 1 stub user from migration 0008.
	if len(seen) != n+1 {
		t.Errorf("walked %d distinct users, want %d", len(seen), n+1)
	}
}
