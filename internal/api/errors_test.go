package api

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// Direct unit tests for the §10 error envelope classifiers (mi-824).
// These exercise the pure mapper helpers that the existing
// HTTP-level tests only cover indirectly via status assertions.

func asAPIError(t *testing.T, err error) *apiError {
	t.Helper()
	var ae *apiError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *apiError, got %T: %v", err, err)
	}
	return ae
}

func TestMapPhotoError(t *testing.T) {
	cases := []struct {
		name     string
		in       error
		wantCode string
		wantHTTP int
	}{
		{
			name:     "ErrPhotoNotFound maps to 404 photo_not_found",
			in:       domain.ErrPhotoNotFound,
			wantCode: "photo_not_found",
			wantHTTP: http.StatusNotFound,
		},
		{
			name:     "wrapped ErrPhotoNotFound still maps via errors.Is",
			in:       fmt.Errorf("repo lookup: %w", domain.ErrPhotoNotFound),
			wantCode: "photo_not_found",
			wantHTTP: http.StatusNotFound,
		},
		{
			name:     "unknown error falls back to opaque 500 internal_error",
			in:       errors.New("pgx: connection refused"),
			wantCode: "internal_error",
			wantHTTP: http.StatusInternalServerError,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ae := asAPIError(t, mapPhotoError(tc.in))
			if ae.Envelope.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", ae.Envelope.Code, tc.wantCode)
			}
			if ae.Status != tc.wantHTTP {
				t.Errorf("status = %d, want %d", ae.Status, tc.wantHTTP)
			}
			if ae.Envelope.Message == "" {
				t.Errorf("message must be non-empty (§10 envelope)")
			}
		})
	}
}

func TestMapFileError(t *testing.T) {
	t.Run("ErrFileNotFound maps to 404 file_not_found", func(t *testing.T) {
		ae := asAPIError(t, mapFileError(domain.ErrFileNotFound))
		if ae.Envelope.Code != "file_not_found" || ae.Status != http.StatusNotFound {
			t.Errorf("got %d/%s, want 404/file_not_found", ae.Status, ae.Envelope.Code)
		}
	})
	t.Run("unknown error falls back to 500 internal_error", func(t *testing.T) {
		ae := asAPIError(t, mapFileError(errors.New("io: broken pipe")))
		if ae.Envelope.Code != "internal_error" || ae.Status != http.StatusInternalServerError {
			t.Errorf("got %d/%s, want 500/internal_error", ae.Status, ae.Envelope.Code)
		}
	})
}

func TestMapListError(t *testing.T) {
	cases := []struct {
		name     string
		in       error
		wantCode string
		wantHTTP int
	}{
		{
			name:     "cursor-tagged error maps to 400 invalid_cursor",
			in:       errors.New("cursor: malformed base64"),
			wantCode: "invalid_cursor",
			wantHTTP: http.StatusBadRequest,
		},
		{
			name:     "embedded cursor: substring still triggers invalid_cursor",
			in:       fmt.Errorf("list page: cursor: bad token"),
			wantCode: "invalid_cursor",
			wantHTTP: http.StatusBadRequest,
		},
		{
			name:     "generic error falls back to 500 internal_error",
			in:       errors.New("pgx: deadline exceeded"),
			wantCode: "internal_error",
			wantHTTP: http.StatusInternalServerError,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ae := asAPIError(t, mapListError(tc.in))
			if ae.Envelope.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", ae.Envelope.Code, tc.wantCode)
			}
			if ae.Status != tc.wantHTTP {
				t.Errorf("status = %d, want %d", ae.Status, tc.wantHTTP)
			}
		})
	}
}

func TestMapPhotoListError(t *testing.T) {
	t.Run("cursor: substring maps to 400 invalid_cursor", func(t *testing.T) {
		ae := asAPIError(t, mapPhotoListError(errors.New("cursor: not base64")))
		if ae.Envelope.Code != "invalid_cursor" || ae.Status != http.StatusBadRequest {
			t.Errorf("got %d/%s, want 400/invalid_cursor", ae.Status, ae.Envelope.Code)
		}
	})
	t.Run("generic error falls back to 500 internal_error", func(t *testing.T) {
		ae := asAPIError(t, mapPhotoListError(errors.New("query timeout")))
		if ae.Envelope.Code != "internal_error" || ae.Status != http.StatusInternalServerError {
			t.Errorf("got %d/%s, want 500/internal_error", ae.Status, ae.Envelope.Code)
		}
	})
}

func TestCodeForStatus(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusBadRequest, "bad_request"},
		{http.StatusUnauthorized, "unauthorized"},
		{http.StatusForbidden, "forbidden"},
		{http.StatusNotFound, "not_found"},
		{http.StatusConflict, "conflict"},
		{http.StatusUnprocessableEntity, "unprocessable"},
		{http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{http.StatusRequestEntityTooLarge, "payload_too_large"},
		{http.StatusInternalServerError, "internal_error"},
		// 5xx that fall through the switch must collapse to internal_error
		// per the catch-all (huma_errors.go:107).
		{http.StatusBadGateway, "internal_error"},
		{http.StatusServiceUnavailable, "internal_error"},
		{http.StatusGatewayTimeout, "internal_error"},
		// Non-matching 4xx falls through to the HTTP-text fallback.
		{http.StatusTeapot, "i'm_a_teapot"},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			if got := codeForStatus(tc.status); got != tc.want {
				t.Errorf("codeForStatus(%d) = %q, want %q", tc.status, got, tc.want)
			}
		})
	}
}

func TestAPIErrorErrorMethod(t *testing.T) {
	// (e *apiError).Error() must return the envelope message verbatim
	// so wrapping/logging code sees the same human-readable text as the
	// JSON `error.message` field.
	ae := newAPIError(http.StatusNotFound, "photo_not_found", "no such photo", nil)
	if ae.Error() != "no such photo" {
		t.Errorf("Error() = %q, want %q", ae.Error(), "no such photo")
	}
	if ae.GetStatus() != http.StatusNotFound {
		t.Errorf("GetStatus() = %d, want %d", ae.GetStatus(), http.StatusNotFound)
	}
}

func TestIsPayloadTooLargeMessage(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		errs []error
		want bool
	}{
		{name: "msg substring", msg: "request body too large", want: true},
		{name: "wrapped err carries substring", msg: "validation",
			errs: []error{errors.New("multipart: request body too large")}, want: true},
		{name: "nil errs are skipped", msg: "validation",
			errs: []error{nil, errors.New("other failure")}, want: false},
		{name: "unrelated message", msg: "field required", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPayloadTooLargeMessage(tc.msg, tc.errs); got != tc.want {
				t.Errorf("isPayloadTooLargeMessage(%q, %v) = %v, want %v",
					tc.msg, tc.errs, got, tc.want)
			}
		})
	}
}

func TestCollectDetails(t *testing.T) {
	t.Run("nil errs yields nil details", func(t *testing.T) {
		if got := collectDetails(nil); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("all-nil errs yields nil details", func(t *testing.T) {
		if got := collectDetails([]error{nil, nil}); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("ErrorDetailer errs flatten to {errors:[...]}", func(t *testing.T) {
		got := collectDetails([]error{
			&huma.ErrorDetail{Message: "a"},
			nil,
			&huma.ErrorDetail{Message: "b"},
		})
		msgs, ok := got["errors"].([]string)
		if !ok {
			t.Fatalf("details[\"errors\"] type = %T, want []string", got["errors"])
		}
		if len(msgs) != 2 || msgs[0] != "a" || msgs[1] != "b" {
			t.Errorf("msgs = %v, want [a b]", msgs)
		}
	})
	t.Run("non-ErrorDetailer errs are dropped (mi-f5v3)", func(t *testing.T) {
		// Arbitrary errors may carry internal state (paths, SQL,
		// host names) and must never reach the client. Only huma's
		// structured validation details are whitelisted.
		if got := collectDetails([]error{
			errors.New("dial tcp 10.0.0.5:5432: connection refused"),
			fmt.Errorf("open /etc/secrets/key.pem: permission denied"),
		}); got != nil {
			t.Errorf("got %v, want nil (raw errors must not leak)", got)
		}
	})
	t.Run("mixed whitelisted + raw keeps only whitelisted", func(t *testing.T) {
		got := collectDetails([]error{
			&huma.ErrorDetail{Message: "field foo required"},
			errors.New("internal: dial tcp 10.0.0.5:5432"),
		})
		msgs, ok := got["errors"].([]string)
		if !ok || len(msgs) != 1 || msgs[0] != "field foo required" {
			t.Errorf("msgs = %#v, want [field foo required]", got["errors"])
		}
	})
}

func TestInstallEnvelopeErrors_EnvelopeShape(t *testing.T) {
	// installEnvelopeErrors uses sync.Once internally; calling it
	// here is safe (idempotent) and ensures huma.NewError is the
	// envelope-shaped variant regardless of test ordering.
	installEnvelopeErrors()

	t.Run("4xx wraps to envelope with mapped code", func(t *testing.T) {
		err := huma.NewError(http.StatusNotFound, "missing")
		ae := asAPIError(t, err)
		if ae.GetStatus() != http.StatusNotFound {
			t.Errorf("status = %d, want 404", ae.GetStatus())
		}
		if ae.Envelope.Code != "not_found" {
			t.Errorf("code = %q, want not_found", ae.Envelope.Code)
		}
		if ae.Envelope.Message != "missing" {
			t.Errorf("message = %q, want %q", ae.Envelope.Message, "missing")
		}
	})

	t.Run("5xx wraps to envelope with internal_error code", func(t *testing.T) {
		err := huma.NewError(http.StatusBadGateway, "upstream down")
		ae := asAPIError(t, err)
		if ae.GetStatus() != http.StatusBadGateway {
			t.Errorf("status = %d, want 502", ae.GetStatus())
		}
		if ae.Envelope.Code != "internal_error" {
			t.Errorf("code = %q, want internal_error", ae.Envelope.Code)
		}
	})

	t.Run("payload-too-large hint is remapped to 413 envelope", func(t *testing.T) {
		// huma surfaces MaxBytesReader bursts as 422 with the body-too-large
		// substring; the installer must hoist them to 413 per §12.
		err := huma.NewError(http.StatusUnprocessableEntity,
			"validation: request body too large")
		ae := asAPIError(t, err)
		if ae.GetStatus() != http.StatusRequestEntityTooLarge {
			t.Errorf("status = %d, want 413", ae.GetStatus())
		}
		if ae.Envelope.Code != "payload_too_large" {
			t.Errorf("code = %q, want payload_too_large", ae.Envelope.Code)
		}
	})

	t.Run("attached validation details flow into details.errors", func(t *testing.T) {
		err := huma.NewError(http.StatusBadRequest, "bad input",
			&huma.ErrorDetail{Message: "field foo required", Location: "body.foo"})
		ae := asAPIError(t, err)
		msgs, ok := ae.Envelope.Details["errors"].([]string)
		if !ok || len(msgs) != 1 || msgs[0] != "field foo required (body.foo: <nil>)" {
			t.Errorf("details = %#v, want errors=[field foo required (body.foo: <nil>)]", ae.Envelope.Details)
		}
	})

	t.Run("attached raw errs are not echoed (mi-f5v3)", func(t *testing.T) {
		err := huma.NewError(http.StatusBadGateway, "upstream down",
			errors.New("dial tcp 10.0.0.5:5432: connection refused"))
		ae := asAPIError(t, err)
		if _, present := ae.Envelope.Details["errors"]; present {
			t.Errorf("details = %#v, want no errors key (raw error must not leak)", ae.Envelope.Details)
		}
	})
}

// Sibling §10 classifiers — same pattern as mapPhotoError/mapListError but
// covering the rest of the resource subtrees so a regression that
// reshuffles `errors.Is` branches (e.g. swaps "conflict" with "not_found")
// is caught at the unit boundary. The fallback (unknown error → 500) is
// already exercised by mapPhotoError's table.
func TestSiblingDomainErrorMappers(t *testing.T) {
	type mapping struct {
		mapper   func(error) error
		domain   error
		wantCode string
		wantHTTP int
	}
	cases := []struct {
		name string
		m    mapping
	}{
		{"collectors not_found", mapping{mapDomainError, domain.ErrCollectorNotFound,
			"collector_not_found", http.StatusNotFound}},
		{"collectors conflict", mapping{mapDomainError, domain.ErrCollectorConflict,
			"collector_conflict", http.StatusConflict}},
		{"collectors referenced", mapping{mapDomainError, domain.ErrCollectorReferenced,
			"collector_referenced", http.StatusConflict}},
		{"collectors fallback", mapping{mapDomainError, errors.New("boom"),
			"internal_error", http.StatusInternalServerError}},

		{"qr_sheet not_found", mapping{mapQRSheetError, domain.ErrQRSheetNotFound,
			"qr_sheet_not_found", http.StatusNotFound}},
		{"qr_sheet conflict", mapping{mapQRSheetError, domain.ErrQRSheetConflict,
			"qr_sheet_conflict", http.StatusConflict}},
		{"qr_sheet specimen not_found", mapping{mapQRSheetError, domain.ErrQRSheetSpecimenNotFound,
			"qr_sheet_specimen_not_found", http.StatusNotFound}},
		{"qr_sheet specimen not_found (domain)", mapping{mapQRSheetError, domain.ErrSpecimenNotFound,
			"specimen_not_found", http.StatusNotFound}},
		{"qr_sheet fallback", mapping{mapQRSheetError, errors.New("boom"),
			"internal_error", http.StatusInternalServerError}},

		{"mineral_species not_found", mapping{mapMineralSpeciesError, domain.ErrMineralSpeciesNotFound,
			"mineral_species_not_found", http.StatusNotFound}},
		{"mineral_species conflict", mapping{mapMineralSpeciesError, domain.ErrMineralSpeciesConflict,
			"mineral_species_conflict", http.StatusConflict}},
		{"mineral_species fallback", mapping{mapMineralSpeciesError, errors.New("boom"),
			"internal_error", http.StatusInternalServerError}},

		{"journal not_found", mapping{mapJournalError, domain.ErrJournalEntryNotFound,
			"journal_entry_not_found", http.StatusNotFound}},
		{"journal conflict", mapping{mapJournalError, domain.ErrJournalEntryConflict,
			"journal_entry_referenced", http.StatusConflict}},
		{"journal fallback", mapping{mapJournalError, errors.New("boom"),
			"internal_error", http.StatusInternalServerError}},

		{"specimen_collectors specimen not_found", mapping{mapSpecimenCollectorError, domain.ErrSpecimenNotFound,
			"specimen_not_found", http.StatusNotFound}},
		{"specimen_collectors collector not_found", mapping{mapSpecimenCollectorError, domain.ErrCollectorNotFound,
			"collector_not_found", http.StatusNotFound}},
		{"specimen_collectors duplicate", mapping{mapSpecimenCollectorError, domain.ErrCollectorConflict,
			"duplicate_collector_id", http.StatusBadRequest}},
		{"specimen_collectors fallback", mapping{mapSpecimenCollectorError, errors.New("boom"),
			"internal_error", http.StatusInternalServerError}},

		{"specimens not_found", mapping{mapSpecimenError, domain.ErrSpecimenNotFound,
			"specimen_not_found", http.StatusNotFound}},
		{"specimens conflict", mapping{mapSpecimenError, domain.ErrSpecimenConflict,
			"specimen_conflict", http.StatusConflict}},
		{"specimens referenced", mapping{mapSpecimenError, domain.ErrSpecimenReferenced,
			"specimen_referenced", http.StatusConflict}},
		{"specimens type immutable", mapping{mapSpecimenError, domain.ErrSpecimenTypeImmutable,
			"specimen_type_immutable", http.StatusConflict}},
		{"specimens fallback", mapping{mapSpecimenError, errors.New("boom"),
			"internal_error", http.StatusInternalServerError}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ae := asAPIError(t, tc.m.mapper(tc.m.domain))
			if ae.Envelope.Code != tc.m.wantCode {
				t.Errorf("code = %q, want %q", ae.Envelope.Code, tc.m.wantCode)
			}
			if ae.Status != tc.m.wantHTTP {
				t.Errorf("status = %d, want %d", ae.Status, tc.m.wantHTTP)
			}
			if ae.Envelope.Message == "" {
				t.Errorf("message must be non-empty (§10 envelope)")
			}
		})
	}
}
