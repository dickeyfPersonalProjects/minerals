package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/oidc"
)

// moderationTestServer wires api.New() with a real (in-memory) Casbin
// enforcer seeded with the §13 v2 defaults, a fake verifier mapping
// tokens to roles, a user repo, and a specimen repo pre-seeded with one
// public and one private specimen owned by a non-admin user. This is the
// unit-level harness for the report + takedown endpoints.
func moderationTestServer(t *testing.T) (http.Handler, *fakeSpecimenRepo, uuid.UUID, uuid.UUID) {
	t.Helper()

	enf, err := authz.NewEnforcer(nil, nil)
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}
	if err := authz.SeedDefaultPolicies(enf); err != nil {
		t.Fatalf("seed policies: %v", err)
	}

	const (
		ownerSub = "00000000-0000-0000-0000-0000000000b1"
		otherSub = "00000000-0000-0000-0000-0000000000b2"
		adminSub = "00000000-0000-0000-0000-0000000000b3"
	)
	verifier := fakeVerifier{tokens: map[string]*oidc.Claims{
		"owner-tok": {Subject: ownerSub, Email: "owner@minerals.local", Roles: []string{"user"}},
		"other-tok": {Subject: otherSub, Email: "other@minerals.local", Roles: []string{"user"}},
		"admin-tok": {Subject: adminSub, Email: "admin@minerals.local", Roles: []string{"admin"}},
	}}

	users := newFakeUserRepo()
	for _, sub := range []string{ownerSub, otherSub, adminSub} {
		users.seed(domain.User{
			ID:          uuid.MustParse(sub),
			KeycloakSub: sub,
			Email:       sub + "@minerals.local",
			Status:      domain.UserStatusActive,
		})
	}

	ownerID := uuid.MustParse(ownerSub)
	specimens := newFakeSpecimenRepo()
	publicID := uuid.New()
	privateID := uuid.New()
	specimens.rows[publicID] = domain.Specimen{
		ID: publicID, Type: domain.SpecimenMineral, Name: "Public Quartz",
		Visibility: domain.VisibilityPublic, AuthorID: ownerID,
	}
	specimens.rows[privateID] = domain.Specimen{
		ID: privateID, Type: domain.SpecimenMineral, Name: "Private Beryl",
		Visibility: domain.VisibilityPrivate, AuthorID: ownerID,
	}

	h := New(Deps{Specimens: specimens, Users: users, Verifier: verifier, Enforcer: enf})
	return h, specimens, publicID, privateID
}

func postJSON(t *testing.T, h http.Handler, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestReportSpecimen covers the public report affordance: an anonymous
// caller may report a public specimen (202), an invalid reason is
// rejected (400/422), and reporting a private specimen the caller can't
// see is indistinguishable from a missing one (404, never 403).
func TestReportSpecimen(t *testing.T) {
	t.Parallel()
	h, _, publicID, privateID := moderationTestServer(t)

	t.Run("anonymous reports public specimen -> 202", func(t *testing.T) {
		rec := postJSON(t, h, "/api/v1/specimens/"+publicID.String()+"/report", "",
			map[string]any{"reason": "abuse", "details": "offensive name"})
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
		}
		var ack reportAck
		if err := json.Unmarshal(rec.Body.Bytes(), &ack); err != nil {
			t.Fatalf("decode ack: %v; raw = %s", err, rec.Body.String())
		}
		if ack.ReportID == "" {
			t.Error("report_id is empty")
		}
	})

	t.Run("invalid reason -> 400", func(t *testing.T) {
		rec := postJSON(t, h, "/api/v1/specimens/"+publicID.String()+"/report", "",
			map[string]any{"reason": "bogus"})
		// Huma enum validation (422) or the handler's own check (400)
		// both reject it; anything in that band is a rejection.
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 400 or 422; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("report private specimen as non-owner -> 404 (no leak)", func(t *testing.T) {
		rec := postJSON(t, h, "/api/v1/specimens/"+privateID.String()+"/report", "other-tok",
			map[string]any{"reason": "abuse"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("report nonexistent specimen -> 404", func(t *testing.T) {
		rec := postJSON(t, h, "/api/v1/specimens/"+uuid.New().String()+"/report", "",
			map[string]any{"reason": "spam"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
		}
	})
}

// TestTakedownSpecimen covers the operator force-private action: an
// admin can take down another user's public specimen (200 + visibility
// flips to private), a plain non-owner user cannot (403), and the
// action is idempotent on an already-private specimen.
func TestTakedownSpecimen(t *testing.T) {
	t.Parallel()
	h, specimens, publicID, privateID := moderationTestServer(t)

	t.Run("admin takes down another user's public specimen -> 200 + private", func(t *testing.T) {
		rec := postJSON(t, h, "/api/v1/admin/specimens/"+publicID.String()+"/takedown", "admin-tok",
			map[string]any{"reason": "policy violation"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var view SpecimenView
		if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
			t.Fatalf("decode view: %v; raw = %s", err, rec.Body.String())
		}
		if view.Visibility != domain.VisibilityPrivate {
			t.Errorf("view visibility = %q, want private", view.Visibility)
		}
		got, _ := specimens.GetByID(t.Context(), publicID)
		if got.Visibility != domain.VisibilityPrivate {
			t.Errorf("persisted visibility = %q, want private", got.Visibility)
		}
	})

	t.Run("non-admin user cannot take down others' content -> 403", func(t *testing.T) {
		rec := postJSON(t, h, "/api/v1/admin/specimens/"+privateID.String()+"/takedown", "other-tok",
			map[string]any{})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("anonymous cannot take down -> 401", func(t *testing.T) {
		rec := postJSON(t, h, "/api/v1/admin/specimens/"+privateID.String()+"/takedown", "",
			map[string]any{})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("idempotent on already-private specimen -> 200", func(t *testing.T) {
		rec := postJSON(t, h, "/api/v1/admin/specimens/"+privateID.String()+"/takedown", "admin-tok",
			map[string]any{})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
	})
}
