package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// MaxDisplayNameLen caps the display_name a user can submit during
// first-login setup. The schema column itself is `text` (unbounded);
// the cap exists at the API boundary to keep abusive inputs out of
// logs and toast messages.
const MaxDisplayNameLen = 80

// fieldDefaultsKeys enumerates the keys allowed in the field_defaults
// map (mi-fo8). Any other key in PATCH /api/v1/profile is a 400.
// Kept sorted so error messages list the allowed set deterministically.
var fieldDefaultsKeys = []string{"acquired_at", "acquired_from", "catalog_number", "images", "price"}

// profileSetupInput is the POST /api/v1/profile request body. v1
// collects only display_name; later beads may extend this surface.
type profileSetupInput struct {
	Body struct {
		DisplayName string `json:"display_name" doc:"Public display name; required, 1–80 characters, trimmed."`
	}
}

// profileBody is the post-setup user state returned by /api/v1/profile.
// `pending` is always false after a successful setup — the frontend
// reads it back to confirm the gate has lifted before navigating to
// the original destination. `field_defaults` carries the user's
// per-field default visibility map (mi-fo8); a null value here means
// the user has set no defaults and the resolution chain falls through
// to the system default for every field.
type profileBody struct {
	ID          string `json:"id" doc:"User row UUID."`
	Email       string `json:"email" doc:"Email from the JWT claim, persisted at first-login."`
	DisplayName string `json:"display_name" doc:"Display name as persisted."`
	Pending     bool   `json:"pending" doc:"Profile-setup-required flag; always false on a successful response."`
	// Roles are the caller's JWT realm roles, surfaced so the SPA can
	// gate role-specific UI (e.g. the admin/devops console nav link,
	// mi-agff) without a second round-trip. This is a UI hint only —
	// the authoritative gate is server-side per-endpoint Casbin
	// enforcement. Always a (possibly empty) array, never null. Does
	// not include the implicit base `user` role (that is a backend
	// authz detail, not a Keycloak realm role).
	Roles         []string           `json:"roles" doc:"Caller's Keycloak realm roles (UI-gating hint only; real authorization is enforced server-side per endpoint)."`
	FieldDefaults *fieldDefaultsView `json:"field_defaults" doc:"Per-field default visibility map (mi-fo8). Sparse — absent keys mean 'no user default; fall through to system default'. Null when the user has no defaults set at all."`
	// DefaultSpecimenVisibility is the per-user default whole-specimen
	// visibility the create form pre-fills with (mi-q2d8). Null when
	// the user has set no preference — the create form then falls back
	// to the system default (private). Distinct from FieldDefaults,
	// which governs per-field redaction within a specimen.
	DefaultSpecimenVisibility *domain.Visibility `json:"default_specimen_visibility" enum:"private,unlisted,public" doc:"Default whole-specimen visibility for newly-created specimens (mi-q2d8). Null means no preference; the create form falls back to the system default (private). Does not affect existing specimens."`
}

// fieldDefaultsView is the wire shape of users.field_defaults. The
// per-key enum tags propagate to the generated OpenAPI / schema.d.ts
// so frontend codegen receives a typed Visibility union for each
// known key. Mirrors domain.FieldDefaults; converted via
// toFieldDefaultsView.
type fieldDefaultsView struct {
	Price         *domain.Visibility `json:"price,omitempty" enum:"private,unlisted,public" doc:"Default visibility for the price field; absent means fall through to the system default."`
	AcquiredFrom  *domain.Visibility `json:"acquired_from,omitempty" enum:"private,unlisted,public" doc:"Default visibility for the acquired_from field; absent means fall through to the system default."`
	AcquiredAt    *domain.Visibility `json:"acquired_at,omitempty" enum:"private,unlisted,public" doc:"Default visibility for the acquired_at field; absent means fall through to the system default."`
	CatalogNumber *domain.Visibility `json:"catalog_number,omitempty" enum:"private,unlisted,public" doc:"Default visibility for the catalog_number field; absent means fall through to the system default."`
	Images        *domain.Visibility `json:"images,omitempty" enum:"private,unlisted,public" doc:"Default visibility for the images field; absent means fall through to the system default."`
}

// toFieldDefaultsView projects the persisted domain.FieldDefaults to
// the wire shape. Returns nil when the domain value is nil so the
// JSON response carries a literal `null` rather than `{}`.
func toFieldDefaultsView(fd *domain.FieldDefaults) *fieldDefaultsView {
	if fd == nil {
		return nil
	}
	return &fieldDefaultsView{
		Price:         fd.Price,
		AcquiredFrom:  fd.AcquiredFrom,
		AcquiredAt:    fd.AcquiredAt,
		CatalogNumber: fd.CatalogNumber,
		Images:        fd.Images,
	}
}

type profileOutput struct {
	Body profileBody
}

// FieldDefaultsPatch is the wire shape of the field_defaults value in
// a PATCH /api/v1/profile body. It carries the raw JSON bytes so the
// service layer can distinguish "key absent" (preserve) from "key set
// to null" (delete) per-key — a distinction the typed `*Visibility`
// pointers in domain.FieldDefaults can't preserve through encoding/json.
type FieldDefaultsPatch []byte

// UnmarshalJSON captures the raw bytes verbatim. Per-key parsing and
// validation happens in mergeFieldDefaults where absent / null / value
// must be disambiguated. encoding/json invokes UnmarshalJSON even for
// the JSON value `null`, so storing the raw bytes here lets the
// handler detect and reject a top-level `field_defaults: null`.
func (p *FieldDefaultsPatch) UnmarshalJSON(b []byte) error {
	*p = append((*p)[:0], b...)
	return nil
}

// MarshalJSON emits the stored bytes verbatim. A zero-value patch
// serializes as JSON `null`; only the handler reads this type and the
// wire response uses *domain.FieldDefaults instead, so this is mainly
// for symmetry / debug logging.
func (p FieldDefaultsPatch) MarshalJSON() ([]byte, error) {
	if len(p) == 0 {
		return []byte("null"), nil
	}
	out := make([]byte, len(p))
	copy(out, p)
	return out, nil
}

// Schema renders the OpenAPI 3.1 schema for field_defaults. The three
// allowed keys (price, acquired_from, images) are documented for
// codegen consumers but neither the closed key set nor the Visibility
// enum is enforced at the schema layer — the handler does both checks
// and surfaces 400 with a stable `invalid_field_defaults` code, which
// the huma validator would otherwise pre-empt with a generic 422.
// schema.d.ts consumers should read the response type
// (domain.FieldDefaults) for the canonical typed shape.
func (FieldDefaultsPatch) Schema(_ huma.Registry) *huma.Schema {
	return &huma.Schema{
		Type: "object",
		// AdditionalProperties is intentionally an open schema (any
		// value): this lets the handler return a 400 with the
		// `invalid_field_defaults` code on unknown keys or invalid
		// values instead of being short-circuited by huma's
		// 422-on-schema-violation behavior. Codegen consumers should
		// type the request manually using the FieldDefaultsView shape
		// returned by GET /api/v1/profile.
		AdditionalProperties: &huma.Schema{},
		Description: "Per-field default visibility map (mi-fo8). PATCH semantics: " +
			"keys present in the request replace the stored value; keys absent " +
			"are preserved; an explicit JSON `null` per key clears that entry " +
			"(returns to the system default). Allowed keys: `price`, " +
			"`acquired_from`, `acquired_at`, `catalog_number`, `images`. " +
			"Allowed values: `private`, `unlisted`, " +
			"`public` (CONTRACT.md §13). Sending `null` at this field's level " +
			"(i.e. `\"field_defaults\": null` in the request body) is rejected " +
			"with 400 — use omission to mean 'don't change'.",
	}
}

// visibilityPatch is the wire shape of the default_specimen_visibility
// value in a PATCH /api/v1/profile body (mi-q2d8). Like
// FieldDefaultsPatch it carries the raw JSON bytes so the handler can
// distinguish three cases a typed *Visibility pointer can't:
//   - key absent          → leave the stored value unchanged
//   - explicit JSON null  → clear (fall back to the system default)
//   - a Visibility string → set the new default
type visibilityPatch []byte

// UnmarshalJSON captures the raw bytes verbatim; the handler does the
// null / value disambiguation and validation. encoding/json invokes
// this even for the JSON value `null`, which is how an explicit
// null-to-clear is detected.
func (p *visibilityPatch) UnmarshalJSON(b []byte) error {
	*p = append((*p)[:0], b...)
	return nil
}

// MarshalJSON emits the stored bytes verbatim (JSON null when empty).
// Only the handler reads this type; the wire response uses the typed
// *domain.Visibility in profileBody instead. Present for symmetry.
func (p visibilityPatch) MarshalJSON() ([]byte, error) {
	if len(p) == 0 {
		return []byte("null"), nil
	}
	out := make([]byte, len(p))
	copy(out, p)
	return out, nil
}

// Schema renders an open string schema so the handler — not huma's
// validator — owns value validation and can return a stable 400 with
// the `invalid_default_specimen_visibility` code (an enum here would
// let huma pre-empt with a generic 422). Nullable so an explicit
// `null` (clear the preference) is accepted; omission means "leave
// unchanged". Codegen consumers should read the typed
// `default_specimen_visibility` on the GET response for the canonical
// Visibility union.
func (visibilityPatch) Schema(_ huma.Registry) *huma.Schema {
	return &huma.Schema{
		Type:     "string",
		Nullable: true,
		Description: "Default whole-specimen visibility for newly-created " +
			"specimens (mi-q2d8). PATCH semantics: a Visibility string " +
			"(`private`, `unlisted`, `public`) sets the default; an explicit " +
			"JSON `null` clears it (the create form then falls back to the " +
			"system default, `private`); omitting the key leaves the stored " +
			"value unchanged. Does not affect existing specimens.",
	}
}

// profilePatchInput is the PATCH /api/v1/profile request body.
// Supports partial update of display_name (mi-j3kn) and field_defaults
// (mi-fo8). Both keys are optional; absent means "leave unchanged".
type profilePatchInput struct {
	Body profilePatchBody
}

type profilePatchBody struct {
	// DisplayName is a pointer so the handler can distinguish absent
	// (preserve) from present (validate + write). An explicit empty
	// string (after trim) is a 400; a JSON `null` value is also a 400
	// — use omission to mean "don't change".
	DisplayName   *string            `json:"display_name,omitempty" doc:"Replacement display_name; trimmed, required non-empty, max 80 chars. Omit to leave unchanged."`
	FieldDefaults FieldDefaultsPatch `json:"field_defaults,omitempty" doc:"Per-field default visibility map; see FieldDefaultsPatch schema."`
	// DefaultSpecimenVisibility patches the create-form default
	// whole-specimen visibility (mi-q2d8). Omit to leave unchanged;
	// explicit null clears it; a Visibility string sets it.
	DefaultSpecimenVisibility visibilityPatch `json:"default_specimen_visibility,omitempty" doc:"Default whole-specimen visibility for new specimens; see visibilityPatch schema."`
}

// profileService wires the profile handlers against a UserRepo.
// Constructed in api.New when Users is non-nil.
type profileService struct {
	repo domain.UserRepo
}

func registerProfileOperations(api huma.API, mws authMiddlewares, repo domain.UserRepo) {
	if repo == nil {
		return
	}
	s := &profileService{repo: repo}

	huma.Register(api, huma.Operation{
		OperationID: "complete-profile",
		Method:      http.MethodPost,
		Path:        "/api/v1/profile",
		Summary:     "Complete first-login profile setup",
		Description: "Persists the caller's display_name and flips their account from " +
			"`pending` to `active`. After a successful call the first-login gate (mi-2hf) " +
			"no longer redirects this user away from protected endpoints. " +
			"This endpoint MUST be reachable while the caller is still pending — it is " +
			"the only protected route that bypasses the gate.",
		Tags:        []string{"profile"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Middlewares: mws.SetupAllowed(),
	}, s.complete)

	huma.Register(api, huma.Operation{
		OperationID: "get-profile",
		Method:      http.MethodGet,
		Path:        "/api/v1/profile",
		Summary:     "Get the caller's profile",
		Description: "Returns the caller's profile row, including the per-field " +
			"default visibility map (`field_defaults`, mi-fo8). Reachable while " +
			"the caller is still pending so the SPA's profile-setup page can " +
			"render the row's current state.",
		Tags:        []string{"profile"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
		Middlewares: mws.SetupAllowed(),
	}, s.get)

	huma.Register(api, huma.Operation{
		OperationID: "patch-profile",
		Method:      http.MethodPatch,
		Path:        "/api/v1/profile",
		Summary:     "Patch the caller's profile",
		Description: "Partial update of the caller's profile. Accepts " +
			"`display_name` (mi-j3kn), `field_defaults` (per-field default " +
			"visibility, mi-fo8), and `default_specimen_visibility` " +
			"(create-form whole-specimen default, mi-q2d8). Keys present in " +
			"the patch replace the stored value; keys absent are preserved. " +
			"For field_defaults, an explicit JSON `null` per inner key clears " +
			"that entry; sending `field_defaults: null` at the top level is " +
			"rejected. For default_specimen_visibility, an explicit `null` " +
			"clears the preference (create form falls back to the system " +
			"default); an invalid value is rejected with " +
			"`invalid_default_specimen_visibility`. A display_name that is " +
			"empty after trimming, longer than 80 chars, or `null` is rejected " +
			"with `invalid_display_name`. Unknown keys and invalid values are " +
			"rejected with 400.",
		Tags:        []string{"profile"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Middlewares: mws.Protected(),
	}, s.patch)
}

func (s *profileService) complete(
	ctx context.Context, in *profileSetupInput,
) (*profileOutput, error) {
	u := auth.FromContext(ctx)
	if u.Sub == "" {
		// Defensive — the auth middleware should already have
		// surfaced this as 401 before reaching the handler.
		return nil, newAPIError(http.StatusUnauthorized,
			"unauthorized", "authentication required", nil)
	}

	name := strings.TrimSpace(in.Body.DisplayName)
	if name == "" {
		return nil, newAPIError(http.StatusBadRequest,
			"invalid_display_name", "display_name is required",
			map[string]any{"field": "display_name"})
	}
	if len(name) > MaxDisplayNameLen {
		return nil, newAPIError(http.StatusBadRequest,
			"invalid_display_name",
			"display_name exceeds the maximum length",
			map[string]any{"field": "display_name", "max": MaxDisplayNameLen})
	}

	// Resolve the canonical row by Sub — the same key get()/patch()
	// use (mi-ml13). Keying MarkActive off u.ID instead would couple
	// this write to the resolveUser middleware having overwritten the
	// JWT-derived ID (UserFromClaims seeds it from the Keycloak sub,
	// not the application users.id); resolving by Sub here is
	// self-contained and matches the other two profile writers.
	full, err := s.repo.GetBySub(ctx, u.Sub)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return nil, newAPIError(http.StatusNotFound,
				"user_not_found", "user record disappeared", nil)
		}
		return nil, err
	}

	now := time.Now().UTC()
	if err := s.repo.MarkActive(ctx, nil, full.ID, name, now); err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return nil, newAPIError(http.StatusNotFound,
				"user_not_found", "user record disappeared", nil)
		}
		return nil, err
	}
	// MarkActive doesn't touch field_defaults; we don't re-read the
	// row here because a freshly-completed profile has no defaults set
	// (the column is null on insert) — the response's `field_defaults`
	// is null, matching what a GET would return.
	return &profileOutput{Body: profileBody{
		ID:          full.ID.String(),
		Email:       full.Email,
		DisplayName: name,
		Pending:     false,
		Roles:       rolesFromContext(ctx),
	}}, nil
}

// rolesFromContext returns the caller's JWT realm roles as a non-nil
// (possibly empty) slice for the profile body's UI-gating hint. The
// slice is copied so callers can't mutate the request-scoped
// auth.User. Note this is the raw JWT role set — it deliberately omits
// the implicit base `user` role that authzUser injects, since that is
// a backend authz detail, not a Keycloak realm role the SPA should see.
func rolesFromContext(ctx context.Context) []string {
	roles := auth.FromContext(ctx).Roles
	out := make([]string, len(roles))
	copy(out, roles)
	return out
}

func (s *profileService) get(ctx context.Context, _ *struct{}) (*profileOutput, error) {
	u := auth.FromContext(ctx)
	if u.Sub == "" {
		return nil, newAPIError(http.StatusUnauthorized,
			"unauthorized", "authentication required", nil)
	}
	full, err := s.repo.GetBySub(ctx, u.Sub)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return nil, newAPIError(http.StatusNotFound,
				"user_not_found", "user record disappeared", nil)
		}
		return nil, err
	}
	body := toProfileBody(full)
	body.Roles = rolesFromContext(ctx)
	return &profileOutput{Body: body}, nil
}

func (s *profileService) patch(ctx context.Context, in *profilePatchInput) (*profileOutput, error) {
	u := auth.FromContext(ctx)
	if u.Sub == "" {
		return nil, newAPIError(http.StatusUnauthorized,
			"unauthorized", "authentication required", nil)
	}
	full, err := s.repo.GetBySub(ctx, u.Sub)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return nil, newAPIError(http.StatusNotFound,
				"user_not_found", "user record disappeared", nil)
		}
		return nil, err
	}

	now := time.Now().UTC()

	if in.Body.DisplayName != nil {
		name := strings.TrimSpace(*in.Body.DisplayName)
		if name == "" {
			return nil, newAPIError(http.StatusBadRequest,
				"invalid_display_name", "display_name is required",
				map[string]any{"field": "display_name"})
		}
		if len(name) > MaxDisplayNameLen {
			return nil, newAPIError(http.StatusBadRequest,
				"invalid_display_name",
				"display_name exceeds the maximum length",
				map[string]any{"field": "display_name", "max": MaxDisplayNameLen})
		}
		if err := s.repo.UpdateDisplayName(ctx, nil, full.ID, name, now); err != nil {
			if errors.Is(err, domain.ErrUserNotFound) {
				return nil, newAPIError(http.StatusNotFound,
					"user_not_found", "user record disappeared", nil)
			}
			return nil, err
		}
		full.DisplayName = &name
		full.UpdatedAt = now
	}

	patchBytes := bytes.TrimSpace(in.Body.FieldDefaults)
	if len(patchBytes) != 0 {
		// `{"field_defaults": null}` reaches UnmarshalJSON as `null`
		// bytes; reject it so callers use omission to mean "don't change".
		if string(patchBytes) == "null" {
			return nil, newAPIError(http.StatusBadRequest,
				"invalid_field_defaults",
				"field_defaults: null is not allowed at the top level — omit the key to leave unchanged",
				map[string]any{"field": "field_defaults"})
		}
		merged, mErr := mergeFieldDefaults(full.FieldDefaults, patchBytes)
		if mErr != nil {
			return nil, mErr
		}
		if err := s.repo.UpdateFieldDefaults(ctx, nil, full.ID, merged, now); err != nil {
			if errors.Is(err, domain.ErrUserNotFound) {
				return nil, newAPIError(http.StatusNotFound,
					"user_not_found", "user record disappeared", nil)
			}
			return nil, err
		}
		full.FieldDefaults = merged
		full.UpdatedAt = now
	}

	visBytes := bytes.TrimSpace(in.Body.DefaultSpecimenVisibility)
	if len(visBytes) != 0 {
		// Tri-state: explicit `null` clears the preference; any other
		// value must parse as a valid Visibility string. Omission
		// (len == 0) never reaches here — it means "leave unchanged".
		var newVis *domain.Visibility
		if string(visBytes) != "null" {
			var v domain.Visibility
			if err := json.Unmarshal(visBytes, &v); err != nil {
				return nil, newAPIError(http.StatusBadRequest,
					"invalid_default_specimen_visibility",
					"default_specimen_visibility must be a Visibility string or null",
					map[string]any{"field": "default_specimen_visibility"})
			}
			if !isValidVisibility(v) {
				return nil, newAPIError(http.StatusBadRequest,
					"invalid_default_specimen_visibility",
					fmt.Sprintf("default_specimen_visibility: %q is not a valid Visibility; allowed values are %s",
						v, strings.Join(validVisibilityValues(), ", ")),
					map[string]any{"field": "default_specimen_visibility",
						"allowed": validVisibilityValues()})
			}
			newVis = &v
		}
		if err := s.repo.UpdateDefaultSpecimenVisibility(ctx, nil, full.ID, newVis, now); err != nil {
			if errors.Is(err, domain.ErrUserNotFound) {
				return nil, newAPIError(http.StatusNotFound,
					"user_not_found", "user record disappeared", nil)
			}
			return nil, err
		}
		full.DefaultSpecimenVisibility = newVis
		full.UpdatedAt = now
	}

	body := toProfileBody(full)
	body.Roles = rolesFromContext(ctx)
	return &profileOutput{Body: body}, nil
}

// toProfileBody is the canonical projection from the persisted user
// row to the profile wire envelope.
func toProfileBody(u domain.User) profileBody {
	displayName := ""
	if u.DisplayName != nil {
		displayName = *u.DisplayName
	}
	return profileBody{
		ID:                        u.ID.String(),
		Email:                     u.Email,
		DisplayName:               displayName,
		Pending:                   u.Status == domain.UserStatusPending,
		FieldDefaults:             toFieldDefaultsView(u.FieldDefaults),
		DefaultSpecimenVisibility: u.DefaultSpecimenVisibility,
	}
}

// mergeFieldDefaults applies a per-key patch over the current map:
// keys with a valid Visibility value replace the stored entry; keys
// with explicit `null` clear that entry; keys absent in the patch are
// preserved. Unknown keys and invalid values are rejected with 400.
// Returns nil when the resulting map has no entries, so the persisted
// column collapses to SQL NULL ("no user defaults").
func mergeFieldDefaults(current *domain.FieldDefaults, patchBytes []byte) (*domain.FieldDefaults, error) {
	patch := map[string]json.RawMessage{}
	if err := json.Unmarshal(patchBytes, &patch); err != nil {
		return nil, newAPIError(http.StatusBadRequest,
			"invalid_field_defaults",
			"field_defaults must be a JSON object",
			map[string]any{"field": "field_defaults"})
	}

	// Start from a copy of current so we can mutate per-key.
	merged := domain.FieldDefaults{}
	if current != nil {
		merged = *current
	}

	for key, rawVal := range patch {
		if !isAllowedFieldDefaultsKey(key) {
			return nil, newAPIError(http.StatusBadRequest,
				"invalid_field_defaults",
				fmt.Sprintf("field_defaults: unknown key %q; allowed keys are %s",
					key, strings.Join(fieldDefaultsKeys, ", ")),
				map[string]any{"field": "field_defaults", "key": key,
					"allowed": fieldDefaultsKeys})
		}
		trimmed := bytes.TrimSpace(rawVal)
		if string(trimmed) == "null" {
			setFieldDefaultsKey(&merged, key, nil)
			continue
		}
		var v domain.Visibility
		if err := json.Unmarshal(trimmed, &v); err != nil {
			return nil, newAPIError(http.StatusBadRequest,
				"invalid_field_defaults",
				fmt.Sprintf("field_defaults.%s: value must be a Visibility string", key),
				map[string]any{"field": "field_defaults", "key": key})
		}
		if !isValidVisibility(v) {
			return nil, newAPIError(http.StatusBadRequest,
				"invalid_field_defaults",
				fmt.Sprintf("field_defaults.%s: %q is not a valid Visibility; allowed values are %s",
					key, v, strings.Join(validVisibilityValues(), ", ")),
				map[string]any{"field": "field_defaults", "key": key,
					"allowed": validVisibilityValues()})
		}
		setFieldDefaultsKey(&merged, key, &v)
	}

	if merged == (domain.FieldDefaults{}) {
		return nil, nil
	}
	return &merged, nil
}

func isAllowedFieldDefaultsKey(k string) bool {
	for _, allowed := range fieldDefaultsKeys {
		if k == allowed {
			return true
		}
	}
	return false
}

// setFieldDefaultsKey writes v (possibly nil) into the slot named by
// key. Caller has already verified key is in fieldDefaultsKeys.
func setFieldDefaultsKey(fd *domain.FieldDefaults, key string, v *domain.Visibility) {
	switch key {
	case "price":
		fd.Price = v
	case "acquired_from":
		fd.AcquiredFrom = v
	case "acquired_at":
		fd.AcquiredAt = v
	case "catalog_number":
		fd.CatalogNumber = v
	case "images":
		fd.Images = v
	}
}

func isValidVisibility(v domain.Visibility) bool {
	switch v {
	case domain.VisibilityPrivate, domain.VisibilityUnlisted, domain.VisibilityPublic:
		return true
	}
	return false
}

// validVisibilityValues returns the allowed Visibility strings sorted
// alphabetically, for stable error messages.
func validVisibilityValues() []string {
	vs := []string{
		string(domain.VisibilityPrivate),
		string(domain.VisibilityUnlisted),
		string(domain.VisibilityPublic),
	}
	sort.Strings(vs)
	return vs
}
