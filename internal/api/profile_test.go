package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// seedActiveProfile plants an active row for the stub identity with
// the supplied display_name and (optional) field_defaults. The stub
// auth path resolves to this row, so any handler call hits a
// realistic Active user state.
func seedActiveProfile(t *testing.T, repo *fakeUserRepo, name string, fd *domain.FieldDefaults) domain.User {
	t.Helper()
	dn := name
	u := domain.User{
		ID:            domain.NewID(),
		KeycloakSub:   auth.StubUserSub,
		Email:         auth.StubUser.Email,
		DisplayName:   &dn,
		Status:        domain.UserStatusActive,
		FieldDefaults: fd,
	}
	repo.seed(u)
	return u
}

// doProfileRequest sends a JSON request through the registered Huma
// handler and returns the recorder for the caller to inspect.
func doProfileRequest(t *testing.T, h http.Handler, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, "/api/v1/profile", rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	h.ServeHTTP(rec, req)
	return rec
}

func decodeProfile(t *testing.T, rec *httptest.ResponseRecorder) profileBody {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var out profileBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode profile body: %v (raw=%s)", err, rec.Body.String())
	}
	return out
}

func ptrVis(v domain.Visibility) *domain.Visibility { return &v }

func TestProfileGet_ReturnsBodyForActiveUser(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	priv := domain.VisibilityPrivate
	seedActiveProfile(t, repo, "Alice", &domain.FieldDefaults{Price: &priv})
	h := New(Deps{Users: repo})

	got := decodeProfile(t, doProfileRequest(t, h, http.MethodGet, ""))
	if got.DisplayName != "Alice" {
		t.Errorf("display_name = %q, want Alice", got.DisplayName)
	}
	if got.Pending {
		t.Errorf("pending = true, want false")
	}
	if got.FieldDefaults == nil || got.FieldDefaults.Price == nil ||
		*got.FieldDefaults.Price != domain.VisibilityPrivate {
		t.Errorf("field_defaults.price = %+v, want private", got.FieldDefaults)
	}
	if got.FieldDefaults.AcquiredFrom != nil || got.FieldDefaults.Images != nil {
		t.Errorf("unset keys leaked: %+v", got.FieldDefaults)
	}
}

func TestProfileGet_ReachableWhilePending(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	repo.seed(domain.User{
		ID:          domain.NewID(),
		KeycloakSub: auth.StubUserSub,
		Email:       auth.StubUser.Email,
		Status:      domain.UserStatusPending,
	})
	h := New(Deps{Users: repo})

	got := decodeProfile(t, doProfileRequest(t, h, http.MethodGet, ""))
	if !got.Pending {
		t.Errorf("pending = false, want true for unset profile")
	}
	if got.FieldDefaults != nil {
		t.Errorf("field_defaults = %+v, want nil for a fresh pending row", got.FieldDefaults)
	}
}

func TestProfilePatch_RoundTripThroughGet(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", nil)
	h := New(Deps{Users: repo})

	patched := decodeProfile(t, doProfileRequest(t, h, http.MethodPatch,
		`{"field_defaults":{"price":"public","images":"private"}}`))
	if patched.FieldDefaults == nil ||
		patched.FieldDefaults.Price == nil || *patched.FieldDefaults.Price != domain.VisibilityPublic ||
		patched.FieldDefaults.Images == nil || *patched.FieldDefaults.Images != domain.VisibilityPrivate {
		t.Fatalf("PATCH response field_defaults = %+v", patched.FieldDefaults)
	}

	got := decodeProfile(t, doProfileRequest(t, h, http.MethodGet, ""))
	if got.FieldDefaults == nil ||
		got.FieldDefaults.Price == nil || *got.FieldDefaults.Price != domain.VisibilityPublic ||
		got.FieldDefaults.Images == nil || *got.FieldDefaults.Images != domain.VisibilityPrivate {
		t.Errorf("GET after PATCH field_defaults = %+v", got.FieldDefaults)
	}
	if got.FieldDefaults.AcquiredFrom != nil {
		t.Errorf("acquired_from = %+v, want nil", got.FieldDefaults.AcquiredFrom)
	}
}

func TestProfilePatch_PartialUpdatePreservesAbsentKeys(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", &domain.FieldDefaults{
		Price:        ptrVis(domain.VisibilityPrivate),
		AcquiredFrom: ptrVis(domain.VisibilityUnlisted),
	})
	h := New(Deps{Users: repo})

	// Patch only `images`; price and acquired_from must be untouched.
	got := decodeProfile(t, doProfileRequest(t, h, http.MethodPatch,
		`{"field_defaults":{"images":"public"}}`))
	if got.FieldDefaults == nil ||
		got.FieldDefaults.Price == nil || *got.FieldDefaults.Price != domain.VisibilityPrivate ||
		got.FieldDefaults.AcquiredFrom == nil || *got.FieldDefaults.AcquiredFrom != domain.VisibilityUnlisted ||
		got.FieldDefaults.Images == nil || *got.FieldDefaults.Images != domain.VisibilityPublic {
		t.Fatalf("partial patch lost keys: %+v", got.FieldDefaults)
	}
}

func TestProfilePatch_NullPerKeyDeletes(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", &domain.FieldDefaults{
		Price:        ptrVis(domain.VisibilityPrivate),
		AcquiredFrom: ptrVis(domain.VisibilityUnlisted),
	})
	h := New(Deps{Users: repo})

	got := decodeProfile(t, doProfileRequest(t, h, http.MethodPatch,
		`{"field_defaults":{"price":null}}`))
	if got.FieldDefaults == nil {
		t.Fatalf("field_defaults = nil after partial delete; acquired_from should still be set")
	}
	if got.FieldDefaults.Price != nil {
		t.Errorf("price = %+v, want nil (deleted)", got.FieldDefaults.Price)
	}
	if got.FieldDefaults.AcquiredFrom == nil || *got.FieldDefaults.AcquiredFrom != domain.VisibilityUnlisted {
		t.Errorf("acquired_from = %+v, want unlisted (preserved)", got.FieldDefaults.AcquiredFrom)
	}
}

func TestProfilePatch_DeletingLastKeyCollapsesToNull(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", &domain.FieldDefaults{
		Price: ptrVis(domain.VisibilityPrivate),
	})
	h := New(Deps{Users: repo})

	got := decodeProfile(t, doProfileRequest(t, h, http.MethodPatch,
		`{"field_defaults":{"price":null}}`))
	if got.FieldDefaults != nil {
		t.Errorf("field_defaults = %+v, want nil after deleting the last key", got.FieldDefaults)
	}
	stored, err := repo.GetBySub(context.Background(), auth.StubUserSub)
	if err != nil {
		t.Fatalf("GetBySub: %v", err)
	}
	if stored.FieldDefaults != nil {
		t.Errorf("stored field_defaults = %+v, want nil (SQL NULL)", stored.FieldDefaults)
	}
}

func TestProfilePatch_EmptyObjectIsNoOp(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", &domain.FieldDefaults{
		Price: ptrVis(domain.VisibilityPrivate),
	})
	h := New(Deps{Users: repo})

	got := decodeProfile(t, doProfileRequest(t, h, http.MethodPatch,
		`{"field_defaults":{}}`))
	if got.FieldDefaults == nil || got.FieldDefaults.Price == nil ||
		*got.FieldDefaults.Price != domain.VisibilityPrivate {
		t.Errorf("empty-object patch mutated state: %+v", got.FieldDefaults)
	}
}

func TestProfilePatch_EmptyBodyIsNoOp(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", &domain.FieldDefaults{
		AcquiredFrom: ptrVis(domain.VisibilityUnlisted),
	})
	h := New(Deps{Users: repo})

	// No field_defaults key at all → preserve.
	got := decodeProfile(t, doProfileRequest(t, h, http.MethodPatch, `{}`))
	if got.FieldDefaults == nil || got.FieldDefaults.AcquiredFrom == nil ||
		*got.FieldDefaults.AcquiredFrom != domain.VisibilityUnlisted {
		t.Errorf("absent field_defaults mutated state: %+v", got.FieldDefaults)
	}
}

func TestProfilePatch_NullAtTopLevelRejected(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", nil)
	h := New(Deps{Users: repo})

	rec := doProfileRequest(t, h, http.MethodPatch, `{"field_defaults":null}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	got := decodeError(t, rec.Body)
	if got.Error.Code != "invalid_field_defaults" {
		t.Errorf("code = %q, want invalid_field_defaults", got.Error.Code)
	}
}

func TestProfilePatch_UnknownKeyRejected(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", nil)
	h := New(Deps{Users: repo})

	rec := doProfileRequest(t, h, http.MethodPatch,
		`{"field_defaults":{"nope":"public"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	got := decodeError(t, rec.Body)
	if got.Error.Code != "invalid_field_defaults" {
		t.Errorf("code = %q, want invalid_field_defaults", got.Error.Code)
	}
	if !strings.Contains(got.Error.Message, "nope") {
		t.Errorf("message %q does not name the offending key", got.Error.Message)
	}
}

func TestProfilePatch_InvalidVisibilityValueRejected(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", nil)
	h := New(Deps{Users: repo})

	rec := doProfileRequest(t, h, http.MethodPatch,
		`{"field_defaults":{"price":"world-readable"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	got := decodeError(t, rec.Body)
	if got.Error.Code != "invalid_field_defaults" {
		t.Errorf("code = %q, want invalid_field_defaults", got.Error.Code)
	}
}

// TestProfilePatch_NonObjectValueRejected covers a wrong-type value
// like a number where a Visibility string is required.
func TestProfilePatch_NonObjectValueRejected(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", nil)
	h := New(Deps{Users: repo})

	rec := doProfileRequest(t, h, http.MethodPatch,
		`{"field_defaults":{"price":42}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	got := decodeError(t, rec.Body)
	if got.Error.Code != "invalid_field_defaults" {
		t.Errorf("code = %q, want invalid_field_defaults", got.Error.Code)
	}
}

func TestProfilePatch_DisplayName_UpdatesAndTrims(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", nil)
	h := New(Deps{Users: repo})

	got := decodeProfile(t, doProfileRequest(t, h, http.MethodPatch,
		`{"display_name":"  Bob  "}`))
	if got.DisplayName != "Bob" {
		t.Errorf("display_name = %q, want %q (trimmed)", got.DisplayName, "Bob")
	}

	// GET reflects the persisted change.
	again := decodeProfile(t, doProfileRequest(t, h, http.MethodGet, ""))
	if again.DisplayName != "Bob" {
		t.Errorf("GET display_name = %q, want Bob", again.DisplayName)
	}
}

func TestProfilePatch_DisplayName_PreservesFieldDefaults(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", &domain.FieldDefaults{
		Price: ptrVis(domain.VisibilityPublic),
	})
	h := New(Deps{Users: repo})

	got := decodeProfile(t, doProfileRequest(t, h, http.MethodPatch,
		`{"display_name":"Bob"}`))
	if got.DisplayName != "Bob" {
		t.Errorf("display_name = %q, want Bob", got.DisplayName)
	}
	if got.FieldDefaults == nil || got.FieldDefaults.Price == nil ||
		*got.FieldDefaults.Price != domain.VisibilityPublic {
		t.Errorf("field_defaults lost: %+v", got.FieldDefaults)
	}
}

func TestProfilePatch_DisplayName_CombinedWithFieldDefaults(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", nil)
	h := New(Deps{Users: repo})

	got := decodeProfile(t, doProfileRequest(t, h, http.MethodPatch,
		`{"display_name":"Bob","field_defaults":{"price":"public"}}`))
	if got.DisplayName != "Bob" {
		t.Errorf("display_name = %q, want Bob", got.DisplayName)
	}
	if got.FieldDefaults == nil || got.FieldDefaults.Price == nil ||
		*got.FieldDefaults.Price != domain.VisibilityPublic {
		t.Errorf("field_defaults = %+v, want price=public", got.FieldDefaults)
	}
}

func TestProfilePatch_DisplayName_EmptyAfterTrimRejected(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", nil)
	h := New(Deps{Users: repo})

	rec := doProfileRequest(t, h, http.MethodPatch, `{"display_name":"   "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	got := decodeError(t, rec.Body)
	if got.Error.Code != "invalid_display_name" {
		t.Errorf("code = %q, want invalid_display_name", got.Error.Code)
	}
	// Existing display_name must not have been overwritten.
	again := decodeProfile(t, doProfileRequest(t, h, http.MethodGet, ""))
	if again.DisplayName != "Alice" {
		t.Errorf("display_name mutated to %q despite rejected PATCH", again.DisplayName)
	}
}

func TestProfilePatch_DisplayName_TooLongRejected(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", nil)
	h := New(Deps{Users: repo})

	tooLong := strings.Repeat("a", MaxDisplayNameLen+1)
	rec := doProfileRequest(t, h, http.MethodPatch,
		`{"display_name":"`+tooLong+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	got := decodeError(t, rec.Body)
	if got.Error.Code != "invalid_display_name" {
		t.Errorf("code = %q, want invalid_display_name", got.Error.Code)
	}
}

func TestProfilePatch_DisplayName_NullRejected(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", nil)
	h := New(Deps{Users: repo})

	// JSON null reaches the handler as a nil *string — same as absent.
	// The handler should NOT update the name, and absent + no other
	// keys means no-op. Verify the name is preserved.
	got := decodeProfile(t, doProfileRequest(t, h, http.MethodPatch, `{"display_name":null}`))
	if got.DisplayName != "Alice" {
		t.Errorf("display_name = %q, want Alice (null should be no-op)", got.DisplayName)
	}
}

// TestProfilePatch_NewFieldsRoundTrip exercises the two field_defaults
// keys added by mi-z3d0: acquired_at and catalog_number. The existing
// matrix above already covers price/acquired_from/images; this test
// pins the wiring for the new keys so the openapi schema, the merge
// path, and the GET projection all agree on their shape.
func TestProfilePatch_NewFieldsRoundTrip(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", nil)
	h := New(Deps{Users: repo})

	patched := decodeProfile(t, doProfileRequest(t, h, http.MethodPatch,
		`{"field_defaults":{"acquired_at":"unlisted","catalog_number":"private"}}`))
	if patched.FieldDefaults == nil ||
		patched.FieldDefaults.AcquiredAt == nil || *patched.FieldDefaults.AcquiredAt != domain.VisibilityUnlisted ||
		patched.FieldDefaults.CatalogNumber == nil || *patched.FieldDefaults.CatalogNumber != domain.VisibilityPrivate {
		t.Fatalf("PATCH new keys lost: %+v", patched.FieldDefaults)
	}

	got := decodeProfile(t, doProfileRequest(t, h, http.MethodGet, ""))
	if got.FieldDefaults == nil ||
		got.FieldDefaults.AcquiredAt == nil || *got.FieldDefaults.AcquiredAt != domain.VisibilityUnlisted ||
		got.FieldDefaults.CatalogNumber == nil || *got.FieldDefaults.CatalogNumber != domain.VisibilityPrivate {
		t.Errorf("GET after PATCH new keys = %+v", got.FieldDefaults)
	}
	// Other keys must remain nil — partial-key patch must not leak.
	if got.FieldDefaults.Price != nil || got.FieldDefaults.AcquiredFrom != nil || got.FieldDefaults.Images != nil {
		t.Errorf("absent keys leaked into the response: %+v", got.FieldDefaults)
	}
}

func TestProfilePatch_UnknownKeyMessageListsNewKeys(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", nil)
	h := New(Deps{Users: repo})

	rec := doProfileRequest(t, h, http.MethodPatch,
		`{"field_defaults":{"nope":"public"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	got := decodeError(t, rec.Body)
	for _, want := range []string{"acquired_at", "catalog_number"} {
		if !strings.Contains(got.Error.Message, want) {
			t.Errorf("error message %q does not list the new allowed key %q", got.Error.Message, want)
		}
	}
}

// TestProfileComplete_FirstLoginPersistsBySub drives the full
// verifier-backed middleware chain (humaAuth → resolveUser) for a
// brand-new, unseeded user: the resolver inserts a pending row keyed
// by the JWT sub with a freshly minted UUIDv7 — deliberately NOT
// equal to the Keycloak sub that UserFromClaims parses into u.ID.
// complete() must therefore resolve the canonical row by Sub and
// MarkActive it; keying off u.ID would only work by accident when the
// resolver happens to have overwritten it (mi-ml13). Acceptance:
// after setup the row carries the name and is active on the FIRST save.
func TestProfileComplete_FirstLoginPersistsBySub(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo() // unseeded — simulates first login
	h := New(Deps{Users: repo, Verifier: newFakeVerifier(), Collectors: newStubCollectorRepo()})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/profile",
		strings.NewReader(`{"display_name":"  Nick Fury  "}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer valid")
	h.ServeHTTP(rec, req)

	got := decodeProfile(t, rec)
	if got.DisplayName != "Nick Fury" {
		t.Errorf("response display_name = %q, want %q (trimmed)", got.DisplayName, "Nick Fury")
	}
	if got.Pending {
		t.Errorf("response pending = true, want false after setup")
	}

	// The canonical row (resolved by sub) must carry the name + active
	// status — this is the persistence the prod bug report says was lost.
	stored, err := repo.GetBySub(context.Background(), realAuthSub)
	if err != nil {
		t.Fatalf("GetBySub(realAuthSub): %v", err)
	}
	if stored.Status != domain.UserStatusActive {
		t.Errorf("stored status = %q, want active", stored.Status)
	}
	if stored.DisplayName == nil || *stored.DisplayName != "Nick Fury" {
		t.Errorf("stored display_name = %v, want Nick Fury", stored.DisplayName)
	}
	// The response id must be the canonical row id, not the JWT-derived
	// sub UUID, so a GET round-trips to the same row.
	if got.ID != stored.ID.String() {
		t.Errorf("response id = %q, want canonical row id %q", got.ID, stored.ID)
	}
}

func TestProfilePatch_RejectsPendingUser(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	repo.seed(domain.User{
		ID:          domain.NewID(),
		KeycloakSub: auth.StubUserSub,
		Email:       auth.StubUser.Email,
		Status:      domain.UserStatusPending,
	})
	h := New(Deps{Users: repo})

	rec := doProfileRequest(t, h, http.MethodPatch,
		`{"field_defaults":{"price":"public"}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	got := decodeError(t, rec.Body)
	if got.Error.Code != "profile_setup_required" {
		t.Errorf("code = %q, want profile_setup_required", got.Error.Code)
	}
}
