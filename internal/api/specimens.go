// Specimens HTTP surface (mi-quf / B-2). Implements the five §10
// CRUD endpoints on top of domain.SpecimenRepo:
//
//	GET    /api/v1/specimens
//	POST   /api/v1/specimens
//	GET    /api/v1/specimens/{id}
//	PATCH  /api/v1/specimens/{id}
//	DELETE /api/v1/specimens/{id}
//
// type_data polymorphism (per CONTRACT.md §11): the wire field
// `type_data` is JSON; its shape is selected by the sibling `type`
// field (mineral|rock|meteorite|fossil). The OpenAPI spec advertises
// this as an `anyOf` of the four concrete schemas — see
// SpecimenTypeData's Schema method for the rationale (oneOf was
// rejected because every type_data field is optional in v1, so `{}`
// matches all four).
// Discrimination at the parent level (the `type` enum) is documented
// in the schema description rather than encoded as an OpenAPI
// `discriminator` mapping, because the discriminator field lives one
// level up from `type_data` and OpenAPI's discriminator keyword
// requires a property within each member of the union.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/mindat"
)

// SpecimenTypeData is the wire shape of `type_data`. Internally it
// carries raw JSON bytes; on the way in the service unmarshals into
// the typed struct selected by the parent `type` field, validates
// it, and stores the validated bytes.
type SpecimenTypeData []byte

// MarshalJSON emits the stored bytes verbatim (or `null` when empty).
func (t SpecimenTypeData) MarshalJSON() ([]byte, error) {
	if len(t) == 0 {
		return []byte("null"), nil
	}
	// Defensive copy so the receiver can't mutate caller's bytes.
	out := make([]byte, len(t))
	copy(out, t)
	return out, nil
}

// UnmarshalJSON captures the raw bytes for later type-specific
// dispatch. Validation against MineralData/RockData/MeteoriteData/
// FossilData happens in the service layer once the parent `type` is
// known.
func (t *SpecimenTypeData) UnmarshalJSON(b []byte) error {
	*t = make([]byte, len(b))
	copy(*t, b)
	return nil
}

// Schema renders the OpenAPI 3.1 schema for the `type_data` field as
// an `anyOf` of the four concrete shapes. The discriminator (`type`)
// lives one level up in the parent body, so OpenAPI's
// `discriminator` keyword (which requires a property within each
// member of the union) doesn't fit; `anyOf` is the closest semantic
// match: type_data is valid against any of MineralData/RockData/
// MeteoriteData/FossilData, with the parent `type` field determining
// which one the server enforces. (Alternative considered: `oneOf`,
// rejected because the empty object `{}` is a valid value of all
// four — every field is optional in v1 — which makes oneOf fail
// validation on common requests.)
func (SpecimenTypeData) Schema(r huma.Registry) *huma.Schema {
	return &huma.Schema{
		AnyOf: []*huma.Schema{
			r.Schema(reflect.TypeOf(domain.MineralData{}), true, "MineralData"),
			r.Schema(reflect.TypeOf(domain.RockData{}), true, "RockData"),
			r.Schema(reflect.TypeOf(domain.MeteoriteData{}), true, "MeteoriteData"),
			r.Schema(reflect.TypeOf(domain.FossilData{}), true, "FossilData"),
		},
		Description: "Type-specific fields. Shape is selected by the sibling `type` " +
			"field: `mineral` -> MineralData, `rock` -> RockData, `meteorite` -> " +
			"MeteoriteData, `fossil` -> FossilData. The server validates the body " +
			"against the matching struct and silently strips fields that don't " +
			"belong. PATCH semantics merge top-level keys; an explicit JSON `null` " +
			"clears a field.",
	}
}

// SpecimenView is the wire shape of a specimen resource. Field names
// are snake_case and match column names; the frontend client is
// regenerated from this type's OpenAPI schema.
type SpecimenView struct {
	ID            uuid.UUID           `json:"id" doc:"UUIDv7 primary key."`
	Type          domain.SpecimenType `json:"type" enum:"mineral,rock,meteorite,fossil" doc:"Specimen kind discriminator (immutable after creation)."`
	CatalogNumber *string             `json:"catalog_number" doc:"Optional human catalog number; unique across all specimens when set."`
	Name          string              `json:"name" doc:"Display name."`
	Description   string              `json:"description" doc:"Markdown description; defaults to empty string."`
	Visibility    domain.Visibility   `json:"visibility" enum:"private,unlisted,public" doc:"Sharing visibility."`
	AuthorID      uuid.UUID           `json:"author_id" doc:"UUID of the user who created the row (CONTRACT.md §13)."`
	AcquiredAt    *time.Time          `json:"acquired_at" doc:"Acquisition date (RFC 3339, time component ignored)."`
	AcquiredFrom  *string             `json:"acquired_from" doc:"Where the specimen was acquired (free text)."`
	PriceCents    *int64              `json:"price_cents" doc:"Acquisition price in cents."`
	SourceNotes   *string             `json:"source_notes" doc:"Free-form provenance notes."`
	LocalityText  *string             `json:"locality_text" doc:"Free-form locality (primary display)."`
	Locality      *domain.Locality    `json:"locality" doc:"Optional structured locality."`
	MassG         *float64            `json:"mass_g" doc:"Mass in grams."`
	Dimensions    *domain.Dimensions  `json:"dimensions" doc:"Optional structured dimensions."`
	TypeData      SpecimenTypeData    `json:"type_data" doc:"Type-specific fields; shape governed by the parent type field."`
	MainImageID   *uuid.UUID          `json:"main_image_id" nullable:"true" doc:"File id of the photo designated as this specimen's main image (mi-m8q). Null means fall back to the first photo by position."`
	CreatedAt     time.Time           `json:"created_at" doc:"RFC 3339 creation timestamp."`
	UpdatedAt     time.Time           `json:"updated_at" doc:"RFC 3339 last-update timestamp."`
}

func toSpecimenView(s domain.Specimen) SpecimenView {
	return SpecimenView{
		ID:            s.ID,
		Type:          s.Type,
		CatalogNumber: s.CatalogNumber,
		Name:          s.Name,
		Description:   s.Description,
		Visibility:    s.Visibility,
		AuthorID:      s.AuthorID,
		AcquiredAt:    s.AcquiredAt,
		AcquiredFrom:  s.AcquiredFrom,
		PriceCents:    s.PriceCents,
		SourceNotes:   s.SourceNotes,
		LocalityText:  s.LocalityText,
		Locality:      s.Locality,
		MassG:         s.MassG,
		Dimensions:    s.Dimensions,
		TypeData:      SpecimenTypeData(s.TypeData),
		MainImageID:   s.MainImageID,
		CreatedAt:     s.CreatedAt,
		UpdatedAt:     s.UpdatedAt,
	}
}

// listSpecimensInput is the typed query-string surface for
// GET /api/v1/specimens.
type listSpecimensInput struct {
	Limit            int    `query:"limit" minimum:"1" maximum:"200" doc:"Page size (1-200; defaults to 50, values above 200 silently clamped)."`
	Cursor           string `query:"cursor" doc:"Opaque pagination cursor returned by the previous page (CONTRACT.md §10.3)."`
	Type             string `query:"type" enum:"mineral,rock,meteorite,fossil" doc:"Filter by specimen type."`
	Visibility       string `query:"visibility" enum:"private,unlisted,public" doc:"Filter by visibility."`
	HasCatalogNumber string `query:"has_catalog_number" enum:"true,false" doc:"true returns rows with a catalog_number set; false returns rows without. Omit to disable the filter."`
	AcquiredAfter    string `query:"acquired_after" doc:"Inclusive lower bound on acquired_at (YYYY-MM-DD)."`
	AcquiredBefore   string `query:"acquired_before" doc:"Inclusive upper bound on acquired_at (YYYY-MM-DD)."`
	CollectorID      string `query:"collector_id" doc:"Filter by collector: returns specimens that have the given collector anywhere in their chain (mi-zv3 / C-3)."`
	Q                string `query:"q" doc:"Full-text search; when present, ordering switches to ts_rank DESC and any cursor previously issued under default ordering becomes invalid."`
}

type listSpecimensOutput struct {
	Body specimenListBody
}

type specimenListBody struct {
	Items      []SpecimenView `json:"items" doc:"Page of specimens."`
	NextCursor *string        `json:"next_cursor" doc:"Cursor for the next page; null at end of results."`
}

type getSpecimenInput struct {
	ID string `path:"id" doc:"Specimen UUID."`
}

type specimenResponseOutput struct {
	Body SpecimenView
}

type createSpecimenInput struct {
	Body createSpecimenBody
}

type createSpecimenBody struct {
	Type          domain.SpecimenType `json:"type" enum:"mineral,rock,meteorite,fossil" doc:"Specimen kind. Immutable after creation."`
	CatalogNumber *string             `json:"catalog_number,omitempty" doc:"Optional unique catalog number."`
	Name          string              `json:"name" minLength:"1" maxLength:"500" doc:"Display name."`
	Description   string              `json:"description,omitempty" doc:"Markdown description; defaults to empty."`
	Visibility    domain.Visibility   `json:"visibility,omitempty" enum:"private,unlisted,public" doc:"Sharing visibility; defaults to private."`
	AcquiredAt    *time.Time          `json:"acquired_at,omitempty" doc:"Acquisition date."`
	AcquiredFrom  *string             `json:"acquired_from,omitempty" doc:"Where the specimen was acquired."`
	PriceCents    *int64              `json:"price_cents,omitempty" doc:"Acquisition price in cents."`
	SourceNotes   *string             `json:"source_notes,omitempty" doc:"Free-form provenance notes."`
	LocalityText  *string             `json:"locality_text,omitempty" doc:"Free-form locality."`
	Locality      *domain.Locality    `json:"locality,omitempty" doc:"Structured locality."`
	MassG         *float64            `json:"mass_g,omitempty" doc:"Mass in grams."`
	Dimensions    *domain.Dimensions  `json:"dimensions,omitempty" doc:"Structured dimensions."`
	TypeData      SpecimenTypeData    `json:"type_data,omitempty" doc:"Type-specific fields; shape selected by the type field."`
}

type createSpecimenOutput struct {
	Location string `header:"Location" doc:"URL of the newly created specimen."`
	Body     SpecimenView
}

type patchSpecimenInput struct {
	ID   string `path:"id" doc:"Specimen UUID."`
	Body patchSpecimenBody
}

// patchSpecimenBody uses pointers so a missing field stays untouched
// and `null` clears nullable fields. `type` is included so a client
// that submits the existing type round-trips successfully; a `type`
// that differs from the stored value is rejected with 409.
type patchSpecimenBody struct {
	Type          *domain.SpecimenType `json:"type,omitempty" enum:"mineral,rock,meteorite,fossil" doc:"Sending a value other than the stored type is rejected with 409 (immutable per design §2)."`
	CatalogNumber *string              `json:"catalog_number,omitempty" doc:"Omit to leave unchanged; pass null to clear."`
	Name          *string              `json:"name,omitempty" minLength:"1" maxLength:"500" doc:"Omit to leave unchanged."`
	Description   *string              `json:"description,omitempty" doc:"Omit to leave unchanged."`
	Visibility    *domain.Visibility   `json:"visibility,omitempty" enum:"private,unlisted,public" doc:"Omit to leave unchanged."`
	AcquiredAt    *time.Time           `json:"acquired_at,omitempty" doc:"Omit to leave unchanged."`
	AcquiredFrom  *string              `json:"acquired_from,omitempty" doc:"Omit to leave unchanged."`
	PriceCents    *int64               `json:"price_cents,omitempty" doc:"Omit to leave unchanged."`
	SourceNotes   *string              `json:"source_notes,omitempty" doc:"Omit to leave unchanged."`
	LocalityText  *string              `json:"locality_text,omitempty" doc:"Omit to leave unchanged."`
	Locality      *domain.Locality     `json:"locality,omitempty" doc:"Omit to leave unchanged."`
	MassG         *float64             `json:"mass_g,omitempty" doc:"Omit to leave unchanged."`
	Dimensions    *domain.Dimensions   `json:"dimensions,omitempty" doc:"Omit to leave unchanged."`
	TypeData      *SpecimenTypeData    `json:"type_data,omitempty" doc:"Top-level merge: present keys overwrite, explicit null clears, omitted keys preserved."`
	MainImageID   *uuid.UUID           `json:"main_image_id,omitempty" doc:"File id of the photo to designate as the specimen's main image (mi-m8q). Must be the file_id of an existing photo on this specimen, or the request is rejected with 422. To revert to the first-by-position fallback, delete the underlying photo — ON DELETE SET NULL handles the cleanup."`
}

type deleteSpecimenInput struct {
	ID string `path:"id" doc:"Specimen UUID."`
}

type deleteSpecimenOutput struct{}

// SpecimenService wires huma operations against a domain.SpecimenRepo.
type SpecimenService struct {
	repo  domain.SpecimenRepo
	authz authzGuard
}

func registerSpecimenOperations(api huma.API, authMW authMiddlewares, guard authzGuard, repo domain.SpecimenRepo) {
	if repo == nil {
		return
	}
	s := &SpecimenService{repo: repo, authz: guard}
	mws := authMW.Protected()
	optionalMWs := authMW.Optional()

	huma.Register(api, huma.Operation{
		OperationID: "list-specimens",
		Method:      http.MethodGet,
		Path:        "/api/v1/specimens",
		Summary:     "List specimens",
		Description: "Cursor-paginated list of specimens. Default ordering is `created_at DESC, id DESC`. When `?q=` is present, ordering switches to `ts_rank DESC, created_at DESC, id DESC` and a cursor previously issued under default ordering is rejected (clients discard cursors when filters or `q` change). " +
			"`?collector_id=` filters to specimens whose chain contains the given collector (mi-zv3). " +
			"Anonymous callers see public specimens only; the DB scope filter does the rest (CONTRACT.md §13 v2).",
		Tags:        []string{"specimens"},
		Errors:      []int{http.StatusBadRequest},
		Middlewares: optionalMWs,
	}, s.list)

	huma.Register(api, huma.Operation{
		OperationID:   "create-specimen",
		Method:        http.MethodPost,
		Path:          "/api/v1/specimens",
		Summary:       "Create a specimen",
		Description:   "Creates a new specimen. Returns 409 when `catalog_number` is non-unique. The `type` field is immutable after creation (see PATCH).",
		Tags:          []string{"specimens"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusConflict, http.StatusUnprocessableEntity},
		Middlewares:   mws,
	}, s.create)

	huma.Register(api, huma.Operation{
		OperationID: "get-specimen",
		Method:      http.MethodGet,
		Path:        "/api/v1/specimens/{id}",
		Summary:     "Get a specimen by id",
		Description: "Returns 404 (not 403/401) when the caller cannot see the specimen — anonymous and unauthorized callers receive the same response as for a non-existent row (CONTRACT.md §13 v2).",
		Tags:        []string{"specimens"},
		Errors:      []int{http.StatusBadRequest, http.StatusNotFound},
		Middlewares: optionalMWs,
	}, s.get)

	huma.Register(api, huma.Operation{
		OperationID: "patch-specimen",
		Method:      http.MethodPatch,
		Path:        "/api/v1/specimens/{id}",
		Summary:     "Update a specimen",
		Description: "Partial update; omitted fields keep previous values. `type_data` is merged at the top level. Sending a `type` that differs from the stored value is rejected with 409 (specimen reclassification means delete + recreate per design §2).",
		Tags:        []string{"specimens"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
		Middlewares: mws,
	}, s.patch)

	huma.Register(api, huma.Operation{
		OperationID:   "delete-specimen",
		Method:        http.MethodDelete,
		Path:          "/api/v1/specimens/{id}",
		Summary:       "Delete a specimen",
		Description:   "Deletes the specimen. Cascades to specimen_collectors. Returns 409 when the specimen still has photos or journal entries (those cascades are deferred to B-3 and beyond).",
		Tags:          []string{"specimens"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict},
		Middlewares:   mws,
	}, s.delete)
}

func (s *SpecimenService) list(ctx context.Context, in *listSpecimensInput) (*listSpecimensOutput, error) {
	filter := domain.SpecimenFilter{
		Query: strings.TrimSpace(in.Q),
	}
	switch in.HasCatalogNumber {
	case "true":
		yes := true
		filter.HasCatalogNumber = &yes
	case "false":
		no := false
		filter.HasCatalogNumber = &no
	case "":
		// filter disabled
	default:
		return nil, newAPIError(http.StatusBadRequest, "invalid_has_catalog_number",
			"has_catalog_number must be true or false",
			map[string]any{"field": "has_catalog_number"})
	}
	if in.Type != "" {
		t := domain.SpecimenType(in.Type)
		filter.Type = &t
	}
	if in.Visibility != "" {
		v := domain.Visibility(in.Visibility)
		filter.Visibility = &v
	}
	if in.CollectorID != "" {
		cid, err := uuid.Parse(in.CollectorID)
		if err != nil {
			return nil, newAPIError(http.StatusBadRequest, "invalid_collector_id",
				"collector_id must be a UUID",
				map[string]any{"field": "collector_id"})
		}
		filter.CollectorID = &cid
	}
	if in.AcquiredAfter != "" {
		t, err := time.Parse("2006-01-02", in.AcquiredAfter)
		if err != nil {
			return nil, newAPIError(http.StatusBadRequest, "invalid_date",
				"acquired_after must be YYYY-MM-DD",
				map[string]any{"field": "acquired_after"})
		}
		filter.AcquiredAfter = &t
	}
	if in.AcquiredBefore != "" {
		t, err := time.Parse("2006-01-02", in.AcquiredBefore)
		if err != nil {
			return nil, newAPIError(http.StatusBadRequest, "invalid_date",
				"acquired_before must be YYYY-MM-DD",
				map[string]any{"field": "acquired_before"})
		}
		filter.AcquiredBefore = &t
	}
	if filter.AcquiredAfter != nil && filter.AcquiredBefore != nil &&
		filter.AcquiredAfter.After(*filter.AcquiredBefore) {
		return nil, newAPIError(http.StatusUnprocessableEntity, "invalid_date_range",
			"acquired_after must be on or before acquired_before", nil)
	}

	page := domain.Page{Limit: in.Limit, Cursor: in.Cursor}
	rows, cursor, err := s.repo.List(ctx, filter, page)
	if err != nil {
		return nil, mapListError(err)
	}
	items := make([]SpecimenView, 0, len(rows))
	for _, r := range rows {
		items = append(items, toSpecimenView(r))
	}
	body := specimenListBody{Items: items}
	if cursor != "" {
		c := string(cursor)
		body.NextCursor = &c
	}
	return &listSpecimensOutput{Body: body}, nil
}

func (s *SpecimenService) get(ctx context.Context, in *getSpecimenInput) (*specimenResponseOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}
	sp, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, mapSpecimenError(err)
	}
	if err := s.authz.checkView(ctx, specimenResource(sp),
		"specimen_not_found", "no such specimen"); err != nil {
		return nil, err
	}
	return &specimenResponseOutput{Body: toSpecimenView(sp)}, nil
}

func (s *SpecimenService) create(ctx context.Context, in *createSpecimenInput) (*createSpecimenOutput, error) {
	b := in.Body
	name := strings.TrimSpace(b.Name)
	if name == "" {
		return nil, newAPIError(http.StatusBadRequest, "invalid_name",
			"name is required", nil)
	}
	if !validSpecimenType(b.Type) {
		return nil, newAPIError(http.StatusBadRequest, "invalid_type",
			"type must be one of mineral|rock|meteorite|fossil",
			map[string]any{"field": "type"})
	}
	visibility := b.Visibility
	if visibility == "" {
		visibility = domain.VisibilityPrivate
	}
	if !validVisibility(visibility) {
		return nil, newAPIError(http.StatusBadRequest, "invalid_visibility",
			"visibility must be one of private|unlisted|public",
			map[string]any{"field": "visibility"})
	}

	typeDataBytes, err := validateAndCanonicalizeTypeData(b.Type, []byte(b.TypeData))
	if err != nil {
		return nil, err
	}

	authorID := auth.FromContext(ctx).ID
	if err := s.authz.check(ctx, newSpecimenResource(authorID, visibility), actCreate); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	sp := domain.Specimen{
		ID:            domain.NewID(),
		Type:          b.Type,
		CatalogNumber: b.CatalogNumber,
		Name:          name,
		Description:   b.Description,
		Visibility:    visibility,
		AuthorID:      authorID,
		AcquiredAt:    b.AcquiredAt,
		AcquiredFrom:  b.AcquiredFrom,
		PriceCents:    b.PriceCents,
		SourceNotes:   b.SourceNotes,
		LocalityText:  b.LocalityText,
		Locality:      b.Locality,
		MassG:         b.MassG,
		Dimensions:    b.Dimensions,
		TypeData:      typeDataBytes,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.repo.Create(ctx, nil, sp); err != nil {
		return nil, mapSpecimenError(err)
	}
	return &createSpecimenOutput{
		Location: "/api/v1/specimens/" + sp.ID.String(),
		Body:     toSpecimenView(sp),
	}, nil
}

func (s *SpecimenService) patch(ctx context.Context, in *patchSpecimenInput) (*specimenResponseOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}

	current, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, mapSpecimenError(err)
	}
	if err := s.authz.check(ctx, specimenResource(current), actEdit); err != nil {
		return nil, err
	}

	b := in.Body
	if b.Type != nil && *b.Type != current.Type {
		return nil, newAPIError(http.StatusConflict, "specimen_type_immutable",
			"specimen type cannot be changed; reclassification means delete + recreate",
			map[string]any{"field": "type", "stored": string(current.Type)})
	}
	if b.Name != nil {
		trimmed := strings.TrimSpace(*b.Name)
		if trimmed == "" {
			return nil, newAPIError(http.StatusBadRequest, "invalid_name",
				"name must be non-empty when provided", nil)
		}
		current.Name = trimmed
	}
	if b.Description != nil {
		current.Description = *b.Description
	}
	if b.Visibility != nil {
		if !validVisibility(*b.Visibility) {
			return nil, newAPIError(http.StatusBadRequest, "invalid_visibility",
				"visibility must be one of private|unlisted|public",
				map[string]any{"field": "visibility"})
		}
		current.Visibility = *b.Visibility
	}
	if b.CatalogNumber != nil {
		current.CatalogNumber = b.CatalogNumber
	}
	if b.AcquiredAt != nil {
		current.AcquiredAt = b.AcquiredAt
	}
	if b.AcquiredFrom != nil {
		current.AcquiredFrom = b.AcquiredFrom
	}
	if b.PriceCents != nil {
		current.PriceCents = b.PriceCents
	}
	if b.SourceNotes != nil {
		current.SourceNotes = b.SourceNotes
	}
	if b.LocalityText != nil {
		current.LocalityText = b.LocalityText
	}
	if b.Locality != nil {
		current.Locality = b.Locality
	}
	if b.MassG != nil {
		current.MassG = b.MassG
	}
	if b.Dimensions != nil {
		current.Dimensions = b.Dimensions
	}
	if b.MainImageID != nil {
		ok, hpErr := s.repo.HasPhotoWithFile(ctx, current.ID, *b.MainImageID)
		if hpErr != nil {
			return nil, mapSpecimenError(hpErr)
		}
		if !ok {
			return nil, newAPIError(http.StatusUnprocessableEntity, "invalid_main_image_id",
				"main_image_id must reference a photo on this specimen",
				map[string]any{"field": "main_image_id"})
		}
		current.MainImageID = b.MainImageID
	}

	if b.TypeData != nil {
		merged, err := mergeTypeData(current.TypeData, []byte(*b.TypeData))
		if err != nil {
			return nil, newAPIError(http.StatusBadRequest, "invalid_type_data",
				"type_data is not a JSON object",
				map[string]any{"field": "type_data"})
		}
		// Re-validate against the stored type's struct.
		canonical, vErr := validateAndCanonicalizeTypeData(current.Type, merged)
		if vErr != nil {
			return nil, vErr
		}
		current.TypeData = canonical
	}

	current.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, nil, current); err != nil {
		return nil, mapSpecimenError(err)
	}
	return &specimenResponseOutput{Body: toSpecimenView(current)}, nil
}

func (s *SpecimenService) delete(ctx context.Context, in *deleteSpecimenInput) (*deleteSpecimenOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}
	current, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, mapSpecimenError(err)
	}
	if err := s.authz.check(ctx, specimenResource(current), actDelete); err != nil {
		return nil, err
	}
	if err := s.repo.Delete(ctx, nil, id); err != nil {
		return nil, mapSpecimenError(err)
	}
	return &deleteSpecimenOutput{}, nil
}

// validateAndCanonicalizeTypeData parses raw JSON bytes through the
// typed struct selected by t, runs the struct's Validate(), and
// returns canonical JSON bytes (re-marshalled from the struct so any
// unknown keys are stripped). Empty/null input yields the canonical
// empty object.
func validateAndCanonicalizeTypeData(t domain.SpecimenType, raw []byte) ([]byte, error) {
	rawTrimmed := bytesTrimSpace(raw)
	if len(rawTrimmed) == 0 || string(rawTrimmed) == "null" {
		return []byte(`{}`), nil
	}
	switch t {
	case domain.SpecimenMineral:
		var d domain.MineralData
		if err := json.Unmarshal(rawTrimmed, &d); err != nil {
			return nil, newAPIError(http.StatusBadRequest, "invalid_type_data",
				"type_data does not match MineralData shape",
				map[string]any{"field": "type_data", "type": "mineral"})
		}
		// Defensive normalization: a user pasting HTML-flavored markup
		// gets cleaned the same way Mindat-sourced values do, so the
		// column is uniformly Unicode at rest (mi-c8v).
		if d.ChemicalFormula != nil {
			normalized := mindat.NormalizeChemicalFormula(*d.ChemicalFormula)
			d.ChemicalFormula = &normalized
		}
		if err := d.Validate(); err != nil {
			return nil, newAPIError(http.StatusUnprocessableEntity, "invalid_type_data",
				err.Error(),
				map[string]any{"field": "type_data", "type": "mineral"})
		}
		return json.Marshal(d)
	case domain.SpecimenRock:
		var d domain.RockData
		if err := json.Unmarshal(rawTrimmed, &d); err != nil {
			return nil, newAPIError(http.StatusBadRequest, "invalid_type_data",
				"type_data does not match RockData shape",
				map[string]any{"field": "type_data", "type": "rock"})
		}
		if err := d.Validate(); err != nil {
			return nil, newAPIError(http.StatusUnprocessableEntity, "invalid_type_data",
				err.Error(),
				map[string]any{"field": "type_data", "type": "rock"})
		}
		return json.Marshal(d)
	case domain.SpecimenMeteorite:
		var d domain.MeteoriteData
		if err := json.Unmarshal(rawTrimmed, &d); err != nil {
			return nil, newAPIError(http.StatusBadRequest, "invalid_type_data",
				"type_data does not match MeteoriteData shape",
				map[string]any{"field": "type_data", "type": "meteorite"})
		}
		if err := d.Validate(); err != nil {
			return nil, newAPIError(http.StatusUnprocessableEntity, "invalid_type_data",
				err.Error(),
				map[string]any{"field": "type_data", "type": "meteorite"})
		}
		return json.Marshal(d)
	case domain.SpecimenFossil:
		var d domain.FossilData
		if err := json.Unmarshal(rawTrimmed, &d); err != nil {
			return nil, newAPIError(http.StatusBadRequest, "invalid_type_data",
				"type_data does not match FossilData shape",
				map[string]any{"field": "type_data", "type": "fossil"})
		}
		if err := d.Validate(); err != nil {
			return nil, newAPIError(http.StatusUnprocessableEntity, "invalid_type_data",
				err.Error(),
				map[string]any{"field": "type_data", "type": "fossil"})
		}
		return json.Marshal(d)
	}
	return nil, newAPIError(http.StatusBadRequest, "invalid_type",
		"type must be one of mineral|rock|meteorite|fossil",
		map[string]any{"field": "type"})
}

// mergeTypeData applies a top-level JSON merge patch (RFC 7396-ish):
// keys present in patch overwrite current; keys with `null` clear;
// keys absent in patch are preserved. Both inputs must be JSON
// objects (or empty/null).
func mergeTypeData(currentBytes, patchBytes []byte) ([]byte, error) {
	current := map[string]json.RawMessage{}
	currentTrimmed := bytesTrimSpace(currentBytes)
	if len(currentTrimmed) > 0 && string(currentTrimmed) != "null" {
		if err := json.Unmarshal(currentTrimmed, &current); err != nil {
			return nil, fmt.Errorf("current type_data: %w", err)
		}
	}
	patchTrimmed := bytesTrimSpace(patchBytes)
	if len(patchTrimmed) == 0 || string(patchTrimmed) == "null" {
		return json.Marshal(current)
	}
	patch := map[string]json.RawMessage{}
	if err := json.Unmarshal(patchTrimmed, &patch); err != nil {
		return nil, fmt.Errorf("patch type_data: %w", err)
	}
	for k, v := range patch {
		if string(bytesTrimSpace(v)) == "null" {
			delete(current, k)
		} else {
			current[k] = v
		}
	}
	return json.Marshal(current)
}

func bytesTrimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\n' || b[0] == '\r') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t' || b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func validSpecimenType(t domain.SpecimenType) bool {
	switch t {
	case domain.SpecimenMineral, domain.SpecimenRock, domain.SpecimenMeteorite, domain.SpecimenFossil:
		return true
	}
	return false
}

func validVisibility(v domain.Visibility) bool {
	switch v {
	case domain.VisibilityPrivate, domain.VisibilityUnlisted, domain.VisibilityPublic:
		return true
	}
	return false
}

// mapSpecimenError translates specimen repo sentinels into §10
// envelope errors.
func mapSpecimenError(err error) error {
	switch {
	case errors.Is(err, domain.ErrSpecimenNotFound):
		return newAPIError(http.StatusNotFound, "specimen_not_found",
			"no such specimen", nil)
	case errors.Is(err, domain.ErrSpecimenConflict):
		return newAPIError(http.StatusConflict, "specimen_conflict",
			"a specimen with that catalog_number already exists",
			map[string]any{"field": "catalog_number", "constraint": "unique"})
	case errors.Is(err, domain.ErrSpecimenReferenced):
		return newAPIError(http.StatusConflict, "specimen_referenced",
			"specimen still has photos or journal entries; delete those first",
			map[string]any{"constraint": "child_rows"})
	case errors.Is(err, domain.ErrSpecimenTypeImmutable):
		return newAPIError(http.StatusConflict, "specimen_type_immutable",
			"specimen type cannot be changed; reclassification means delete + recreate",
			map[string]any{"field": "type"})
	}
	return newAPIError(http.StatusInternalServerError, "internal_error",
		"internal server error", nil)
}
