//go:build integration

package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// scopedDB / seedUser live in collector_postgres_integration_test.go.

// Coverage notes (mi-y72 / mi-fo8 #1):
//   - GetBySub returns field_defaults = nil when the column is SQL NULL.
//   - Create round-trips a populated FieldDefaults via JSON.
//   - Create round-trips a sparse FieldDefaults (single key set,
//     others nil) — proves omitempty fires on the wire and absent
//     keys read back as nil pointers, not zero-string Visibilities.
//   - UpdateFieldDefaults persists a new value, clears back to NULL
//     when called with nil, and surfaces ErrUserNotFound for unknown
//     ids without touching other rows.

func mkUser(t *testing.T, sub string) domain.User {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	display := "user-" + sub
	return domain.User{
		ID:          domain.NewID(),
		KeycloakSub: sub,
		Email:       sub + "@example.invalid",
		DisplayName: &display,
		Status:      domain.UserStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func TestIntegration_User_FieldDefaultsNullByDefault(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewUserPostgres(pool)
	ctx := context.Background()

	u := mkUser(t, "fd-null-"+uuid.NewString())
	if err := repo.Create(ctx, nil, u); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetBySub(ctx, u.KeycloakSub)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.FieldDefaults != nil {
		t.Errorf("field_defaults: want nil, got %+v", *got.FieldDefaults)
	}
}

func TestIntegration_User_FieldDefaultsRoundtripFull(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewUserPostgres(pool)
	ctx := context.Background()

	price := domain.VisibilityPrivate
	acq := domain.VisibilityUnlisted
	img := domain.VisibilityPublic
	fd := &domain.FieldDefaults{
		Price:        &price,
		AcquiredFrom: &acq,
		Images:       &img,
	}
	u := mkUser(t, "fd-full-"+uuid.NewString())
	u.FieldDefaults = fd
	if err := repo.Create(ctx, nil, u); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetBySub(ctx, u.KeycloakSub)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.FieldDefaults == nil {
		t.Fatalf("field_defaults: want non-nil, got nil")
	}
	if got.FieldDefaults.Price == nil || *got.FieldDefaults.Price != price {
		t.Errorf("price: got %v, want %v", got.FieldDefaults.Price, price)
	}
	if got.FieldDefaults.AcquiredFrom == nil || *got.FieldDefaults.AcquiredFrom != acq {
		t.Errorf("acquired_from: got %v, want %v", got.FieldDefaults.AcquiredFrom, acq)
	}
	if got.FieldDefaults.Images == nil || *got.FieldDefaults.Images != img {
		t.Errorf("images: got %v, want %v", got.FieldDefaults.Images, img)
	}
}

func TestIntegration_User_FieldDefaultsRoundtripSparse(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewUserPostgres(pool)
	ctx := context.Background()

	// Only Price set; AcquiredFrom and Images remain unset.
	price := domain.VisibilityUnlisted
	fd := &domain.FieldDefaults{Price: &price}

	u := mkUser(t, "fd-sparse-"+uuid.NewString())
	u.FieldDefaults = fd
	if err := repo.Create(ctx, nil, u); err != nil {
		t.Fatalf("create: %v", err)
	}

	// On-the-wire JSON must omit absent keys (sparse encoding per
	// CONTRACT §13 / mi-fo8). Belt-and-suspenders SQL probe — the
	// repo's read-side ignores extra keys, but the redaction work
	// (mi-9ww) does care.
	var rawHasAcq, rawHasImg bool
	if err := pool.QueryRow(context.Background(),
		`SELECT field_defaults ? 'acquired_from', field_defaults ? 'images'
		 FROM users WHERE id = $1`, u.ID,
	).Scan(&rawHasAcq, &rawHasImg); err != nil {
		t.Fatalf("probe keys: %v", err)
	}
	if rawHasAcq || rawHasImg {
		t.Errorf("sparse encoding violated: acquired_from=%v images=%v (want both false)", rawHasAcq, rawHasImg)
	}

	got, err := repo.GetBySub(ctx, u.KeycloakSub)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.FieldDefaults == nil {
		t.Fatalf("field_defaults: want non-nil, got nil")
	}
	if got.FieldDefaults.Price == nil || *got.FieldDefaults.Price != price {
		t.Errorf("price: got %v, want %v", got.FieldDefaults.Price, price)
	}
	if got.FieldDefaults.AcquiredFrom != nil {
		t.Errorf("acquired_from: want nil, got %v", *got.FieldDefaults.AcquiredFrom)
	}
	if got.FieldDefaults.Images != nil {
		t.Errorf("images: want nil, got %v", *got.FieldDefaults.Images)
	}
}

func TestIntegration_User_UpdateFieldDefaultsSetAndClear(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewUserPostgres(pool)
	ctx := context.Background()

	u := mkUser(t, "fd-upd-"+uuid.NewString())
	if err := repo.Create(ctx, nil, u); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Set.
	img := domain.VisibilityPublic
	fd := &domain.FieldDefaults{Images: &img}
	bumped := u.UpdatedAt.Add(time.Second)
	if err := repo.UpdateFieldDefaults(ctx, nil, u.ID, fd, bumped); err != nil {
		t.Fatalf("update set: %v", err)
	}
	got, err := repo.GetBySub(ctx, u.KeycloakSub)
	if err != nil {
		t.Fatalf("get after set: %v", err)
	}
	if got.FieldDefaults == nil || got.FieldDefaults.Images == nil || *got.FieldDefaults.Images != img {
		t.Errorf("after set: %+v", got.FieldDefaults)
	}
	if !got.UpdatedAt.Equal(bumped) {
		t.Errorf("updated_at: got %v, want %v", got.UpdatedAt, bumped)
	}

	// Clear to NULL.
	bumped2 := bumped.Add(time.Second)
	if err := repo.UpdateFieldDefaults(ctx, nil, u.ID, nil, bumped2); err != nil {
		t.Fatalf("update clear: %v", err)
	}
	got, err = repo.GetBySub(ctx, u.KeycloakSub)
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if got.FieldDefaults != nil {
		t.Errorf("after clear: want nil, got %+v", *got.FieldDefaults)
	}
	if !got.UpdatedAt.Equal(bumped2) {
		t.Errorf("updated_at: got %v, want %v", got.UpdatedAt, bumped2)
	}
}

func TestIntegration_User_UpdateFieldDefaultsUnknownID(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewUserPostgres(pool)

	err := repo.UpdateFieldDefaults(context.Background(), nil, uuid.New(), nil, time.Now().UTC())
	if !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("unknown id: want ErrUserNotFound, got %v", err)
	}
}

// --- default_specimen_visibility (mi-q2d8) ---
//
// Parallel coverage to FieldDefaults: NULL by default, round-trips via
// Create, and UpdateDefaultSpecimenVisibility sets/clears the column
// and surfaces ErrUserNotFound for unknown ids.

func TestIntegration_User_DefaultSpecimenVisibilityNullByDefault(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewUserPostgres(pool)
	ctx := context.Background()

	u := mkUser(t, "dsv-null-"+uuid.NewString())
	if err := repo.Create(ctx, nil, u); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetBySub(ctx, u.KeycloakSub)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DefaultSpecimenVisibility != nil {
		t.Errorf("default_specimen_visibility: want nil, got %v", *got.DefaultSpecimenVisibility)
	}
}

func TestIntegration_User_DefaultSpecimenVisibilityRoundtripViaCreate(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewUserPostgres(pool)
	ctx := context.Background()

	vis := domain.VisibilityPublic
	u := mkUser(t, "dsv-create-"+uuid.NewString())
	u.DefaultSpecimenVisibility = &vis
	if err := repo.Create(ctx, nil, u); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetBySub(ctx, u.KeycloakSub)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DefaultSpecimenVisibility == nil || *got.DefaultSpecimenVisibility != vis {
		t.Errorf("default_specimen_visibility: got %v, want %v", got.DefaultSpecimenVisibility, vis)
	}
}

func TestIntegration_User_UpdateDefaultSpecimenVisibilitySetAndClear(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewUserPostgres(pool)
	ctx := context.Background()

	u := mkUser(t, "dsv-update-"+uuid.NewString())
	if err := repo.Create(ctx, nil, u); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Set to a value.
	vis := domain.VisibilityUnlisted
	bumped := u.UpdatedAt.Add(time.Second)
	if err := repo.UpdateDefaultSpecimenVisibility(ctx, nil, u.ID, &vis, bumped); err != nil {
		t.Fatalf("update set: %v", err)
	}
	got, err := repo.GetBySub(ctx, u.KeycloakSub)
	if err != nil {
		t.Fatalf("get after set: %v", err)
	}
	if got.DefaultSpecimenVisibility == nil || *got.DefaultSpecimenVisibility != vis {
		t.Errorf("after set: %v", got.DefaultSpecimenVisibility)
	}
	if !got.UpdatedAt.Equal(bumped) {
		t.Errorf("updated_at: got %v, want %v", got.UpdatedAt, bumped)
	}

	// Clear to NULL.
	bumped2 := bumped.Add(time.Second)
	if err := repo.UpdateDefaultSpecimenVisibility(ctx, nil, u.ID, nil, bumped2); err != nil {
		t.Fatalf("update clear: %v", err)
	}
	got, err = repo.GetBySub(ctx, u.KeycloakSub)
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if got.DefaultSpecimenVisibility != nil {
		t.Errorf("after clear: want nil, got %v", *got.DefaultSpecimenVisibility)
	}
	if !got.UpdatedAt.Equal(bumped2) {
		t.Errorf("updated_at: got %v, want %v", got.UpdatedAt, bumped2)
	}
}

func TestIntegration_User_UpdateDefaultSpecimenVisibilityUnknownID(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewUserPostgres(pool)

	err := repo.UpdateDefaultSpecimenVisibility(context.Background(), nil, uuid.New(), nil, time.Now().UTC())
	if !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("unknown id: want ErrUserNotFound, got %v", err)
	}
}

// TestIntegration_User_SetStatus exercises the suspend/unsuspend status
// write (mi-3gxz), including that the 'suspended' value satisfies the
// migration-0019 CHECK constraint and round-trips back to 'active'.
func TestIntegration_User_SetStatus(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewUserPostgres(pool)
	ctx := context.Background()

	u := mkUser(t, "setstatus-"+uuid.NewString())
	if err := repo.Create(ctx, nil, u); err != nil {
		t.Fatalf("create: %v", err)
	}

	bumped := u.UpdatedAt.Add(time.Second)
	if err := repo.SetStatus(ctx, nil, u.ID, domain.UserStatusSuspended, bumped); err != nil {
		t.Fatalf("set suspended: %v", err)
	}
	got, err := repo.GetBySub(ctx, u.KeycloakSub)
	if err != nil {
		t.Fatalf("get after suspend: %v", err)
	}
	if got.Status != domain.UserStatusSuspended {
		t.Errorf("status: got %s, want suspended", got.Status)
	}
	if !got.UpdatedAt.Equal(bumped) {
		t.Errorf("updated_at: got %v, want %v", got.UpdatedAt, bumped)
	}

	bumped2 := bumped.Add(time.Second)
	if err := repo.SetStatus(ctx, nil, u.ID, domain.UserStatusActive, bumped2); err != nil {
		t.Fatalf("set active: %v", err)
	}
	got, err = repo.GetBySub(ctx, u.KeycloakSub)
	if err != nil {
		t.Fatalf("get after unsuspend: %v", err)
	}
	if got.Status != domain.UserStatusActive {
		t.Errorf("status: got %s, want active", got.Status)
	}
}

func TestIntegration_User_SetStatusUnknownID(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewUserPostgres(pool)

	err := repo.SetStatus(context.Background(), nil, uuid.New(), domain.UserStatusSuspended, time.Now().UTC())
	if !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("unknown id: want ErrUserNotFound, got %v", err)
	}
}
