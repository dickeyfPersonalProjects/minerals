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
