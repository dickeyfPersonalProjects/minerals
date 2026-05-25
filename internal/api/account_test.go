package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// --- fakes -----------------------------------------------------------

type fakeEraser struct {
	res   domain.AccountErasure
	err   error
	calls int
	gotID uuid.UUID
}

func (f *fakeEraser) Erase(_ context.Context, id uuid.UUID) (domain.AccountErasure, error) {
	f.calls++
	f.gotID = id
	return f.res, f.err
}

type fakeObjStore struct {
	deleted []string
	err     error
}

func (f *fakeObjStore) Delete(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	return f.err
}

type fakeRevoker struct {
	calls int
	gotID uuid.UUID
	err   error
}

func (f *fakeRevoker) RevokeAllForUser(_ context.Context, id uuid.UUID) error {
	f.calls++
	f.gotID = id
	return f.err
}

type fakeIdentity struct {
	calls  int
	gotSub string
	err    error
}

func (f *fakeIdentity) DeleteIdentity(_ context.Context, sub string) error {
	f.calls++
	f.gotSub = sub
	return f.err
}

// doAccountDelete drives DELETE /api/v1/account through the registered
// Huma handler with the supplied JSON body.
func doAccountDelete(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/account", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

// newAccountHarness wires a New() handler whose stub-auth path resolves
// to an active user, plus the four account collaborators as fakes.
func newAccountHarness(t *testing.T, eraser domain.AccountEraser) (http.Handler, *fakeObjStore, *fakeRevoker, *fakeIdentity, domain.User) {
	t.Helper()
	repo := newFakeUserRepo()
	u := seedActiveProfile(t, repo, "Alice", nil)
	store := &fakeObjStore{}
	rev := &fakeRevoker{}
	id := &fakeIdentity{}
	h := New(Deps{
		Users: repo,
		Account: &AccountServiceDeps{
			Eraser:   eraser,
			Storage:  store,
			Sessions: rev,
			Identity: id,
		},
	})
	return h, store, rev, id, u
}

func decodeErrCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v (raw=%s)", err, rec.Body.String())
	}
	return env.Error.Code
}

// --- tests -----------------------------------------------------------

func TestAccountDelete_HappyPath_RunsFullCleanup(t *testing.T) {
	t.Parallel()
	eraser := &fakeEraser{res: domain.AccountErasure{
		FreedObjectKeys: []string{"files/a", "files/b"},
		Specimens:       3,
	}}
	h, store, rev, id, u := newAccountHarness(t, eraser)

	rec := doAccountDelete(t, h, `{"confirm":"DELETE"}`)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if eraser.calls != 1 {
		t.Fatalf("eraser calls = %d, want 1", eraser.calls)
	}
	if eraser.gotID != u.ID {
		t.Errorf("eraser id = %v, want resolved user id %v", eraser.gotID, u.ID)
	}
	if len(store.deleted) != 2 || store.deleted[0] != "files/a" || store.deleted[1] != "files/b" {
		t.Errorf("object purge = %v, want [files/a files/b]", store.deleted)
	}
	if rev.calls != 1 || rev.gotID != u.ID {
		t.Errorf("revoker calls=%d id=%v, want 1 / %v", rev.calls, rev.gotID, u.ID)
	}
	if id.calls != 1 || id.gotSub != u.KeycloakSub {
		t.Errorf("identity calls=%d sub=%q, want 1 / %q", id.calls, id.gotSub, u.KeycloakSub)
	}
}

func TestAccountDelete_WrongConfirmation_Rejected(t *testing.T) {
	t.Parallel()
	eraser := &fakeEraser{}
	h, _, rev, id, _ := newAccountHarness(t, eraser)

	// A present-but-wrong confirm phrase reaches the handler and is
	// rejected with our stable 400 code.
	for _, body := range []string{`{"confirm":"delete"}`, `{"confirm":""}`} {
		rec := doAccountDelete(t, h, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rec.Code)
		}
		if code := decodeErrCode(t, rec); code != "invalid_confirmation" {
			t.Errorf("body %s: code = %q, want invalid_confirmation", body, code)
		}
	}
	// A missing `confirm` key is caught by huma's required-field schema
	// validation (422) before the handler runs — still no side effects.
	if rec := doAccountDelete(t, h, `{}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty body: status = %d, want 422", rec.Code)
	}
	// No side effects on a rejected confirmation.
	if eraser.calls != 0 || rev.calls != 0 || id.calls != 0 {
		t.Errorf("rejected confirmation triggered side effects: eraser=%d rev=%d id=%d",
			eraser.calls, rev.calls, id.calls)
	}
}

func TestAccountDelete_NotFound_Maps404(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newAccountHarness(t, &fakeEraser{err: domain.ErrUserNotFound})
	rec := doAccountDelete(t, h, `{"confirm":"DELETE"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if code := decodeErrCode(t, rec); code != "user_not_found" {
		t.Errorf("code = %q, want user_not_found", code)
	}
}

func TestAccountDelete_StubUndeletable_Maps403(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newAccountHarness(t, &fakeEraser{err: domain.ErrStubUserUndeletable})
	rec := doAccountDelete(t, h, `{"confirm":"DELETE"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if code := decodeErrCode(t, rec); code != "account_undeletable" {
		t.Errorf("code = %q, want account_undeletable", code)
	}
}

// Cleanup steps are best-effort: a failure in object purge, session
// revoke, or identity delete must NOT fail the request — the DB
// erasure has already committed.
func TestAccountDelete_CleanupFailuresAreBestEffort(t *testing.T) {
	t.Parallel()
	eraser := &fakeEraser{res: domain.AccountErasure{FreedObjectKeys: []string{"files/a"}}}
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", nil)
	store := &fakeObjStore{err: context.DeadlineExceeded}
	rev := &fakeRevoker{err: context.DeadlineExceeded}
	id := &fakeIdentity{err: context.DeadlineExceeded}
	h := New(Deps{
		Users: repo,
		Account: &AccountServiceDeps{
			Eraser: eraser, Storage: store, Sessions: rev, Identity: id,
		},
	})

	rec := doAccountDelete(t, h, `{"confirm":"DELETE"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 despite cleanup failures; body=%s", rec.Code, rec.Body.String())
	}
	if eraser.calls != 1 || rev.calls != 1 || id.calls != 1 {
		t.Errorf("cleanup steps not all attempted: eraser=%d rev=%d id=%d", eraser.calls, rev.calls, id.calls)
	}
}

// With only the required Eraser wired (no Storage/Sessions/Identity),
// the endpoint still succeeds.
func TestAccountDelete_OnlyEraserWired_Succeeds(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", nil)
	eraser := &fakeEraser{res: domain.AccountErasure{FreedObjectKeys: []string{"files/a"}}}
	h := New(Deps{Users: repo, Account: &AccountServiceDeps{Eraser: eraser}})

	rec := doAccountDelete(t, h, `{"confirm":"DELETE"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if eraser.calls != 1 {
		t.Errorf("eraser calls = %d, want 1", eraser.calls)
	}
}

// A nil Account dep (or nil Eraser) leaves the route unregistered — the
// /api/v1/* catch-all returns the 404 envelope.
func TestAccountDelete_Unregistered_WhenNoEraser(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", nil)
	h := New(Deps{Users: repo})
	rec := doAccountDelete(t, h, `{"confirm":"DELETE"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route unregistered)", rec.Code)
	}
}
