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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// dateLayout is the YYYY-MM-DD layout used for acquired_at on the
// wire (per CONTRACT §10 query params, design §2 column type).
const dateLayout = "2006-01-02"

// SpecimensDeps carries the dependencies the specimens handlers
// need: a repo and the underlying pool for transaction composition.
type SpecimensDeps struct {
	Repo *db.SpecimenPostgres
	Pool *pgxpool.Pool
}

// registerSpecimenOperations wires the five /api/v1/specimens
// endpoints (CONTRACT §10, bead mi-quf). Routes registered here are
// part of the protected group; the auth chain is applied by mux at
// /api/v1/.
func registerSpecimenOperations(api huma.API, sd SpecimensDeps) {
	if sd.Repo == nil {
		// No DB wired (e.g. openapi-spec dump). Register operations
		// with handlers that surface a 503 — the spec still includes
		// them so the generated frontend client stays accurate.
		registerSpecimenStubs(api)
		return
	}

	huma.Register(api, huma.Operation{
		OperationID: "list-specimens",
		Method:      http.MethodGet,
		Path:        "/api/v1/specimens",
		Summary:     "List specimens",
		Description: "Cursor-paginated list of specimens. Filters compose with AND. " +
			"When `q` is set, results order by tsv-rank instead of created_at; " +
			"the `collector_id` filter is a v1 stub (per mi-quf) and currently " +
			"returns no results pending B-4.",
		Tags: []string{"specimens"},
	}, makeListSpecimensHandler(sd))

	huma.Register(api, huma.Operation{
		OperationID:   "create-specimen",
		Method:        http.MethodPost,
		Path:          "/api/v1/specimens",
		Summary:       "Create a specimen",
		DefaultStatus: http.StatusCreated,
		Tags:          []string{"specimens"},
		Errors:        []int{http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity},
	}, makeCreateSpecimenHandler(sd))

	huma.Register(api, huma.Operation{
		OperationID: "get-specimen",
		Method:      http.MethodGet,
		Path:        "/api/v1/specimens/{id}",
		Summary:     "Get a specimen by id",
		Tags:        []string{"specimens"},
		Errors:      []int{http.StatusNotFound},
	}, makeGetSpecimenHandler(sd))

	huma.Register(api, huma.Operation{
		OperationID: "patch-specimen",
		Method:      http.MethodPatch,
		Path:        "/api/v1/specimens/{id}",
		Summary:     "Update a specimen",
		Description: "Partial update. The `type` field is immutable — reclassification " +
			"is delete+recreate (CONTRACT §11 / design §2). `type_data` merges at the " +
			"top level: missing keys keep their previous value; explicit `null` clears.",
		Tags:   []string{"specimens"},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, makePatchSpecimenHandler(sd))

	huma.Register(api, huma.Operation{
		OperationID:   "delete-specimen",
		Method:        http.MethodDelete,
		Path:          "/api/v1/specimens/{id}",
		Summary:       "Delete a specimen",
		Description:   "Refuses with 409 if photos or journal_entries reference the specimen (per mi-quf).",
		DefaultStatus: http.StatusNoContent,
		Tags:          []string{"specimens"},
		Errors:        []int{http.StatusNotFound, http.StatusConflict},
	}, makeDeleteSpecimenHandler(sd))
}

// SpecimenTypeData is the polymorphic specimens.type_data field on
// the wire. It marshals as the inner JSON object directly (no
// wrapper); the OpenAPI schema is a `oneOf` of MineralData /
// RockData / MeteoriteData. The matching variant for a given
// specimen is determined by the parent's `type` field.
type SpecimenTypeData json.RawMessage

// MarshalJSON emits the underlying bytes verbatim, defaulting to "{}"
// for an empty value (the column is NOT NULL).
func (t SpecimenTypeData) MarshalJSON() ([]byte, error) {
	if len(t) == 0 {
		return []byte("{}"), nil
	}
	return []byte(t), nil
}

// UnmarshalJSON captures the raw bytes; the dispatch on parent type
// happens at the handler boundary.
func (t *SpecimenTypeData) UnmarshalJSON(b []byte) error {
	*t = append((*t)[:0], b...)
	return nil
}

// Schema implements huma.SchemaProvider, advertising the field as
// anyOf the three type-specific shapes so the OpenAPI spec carries
// the full surface (CONTRACT §10 OpenAPI in-sync). anyOf is used
// rather than oneOf because each variant has all-optional fields:
// an empty object would match all three under oneOf and fail
// validation. The authoritative discriminator is the parent
// specimen's `type` enum; the handler dispatches typed validation
// (`domain.ValidateTypeData`) once the parent type is known.
func (SpecimenTypeData) Schema(r huma.Registry) *huma.Schema {
	return &huma.Schema{
		AnyOf: []*huma.Schema{
			r.Schema(reflect.TypeOf(domain.MineralData{}), true, "MineralData"),
			r.Schema(reflect.TypeOf(domain.RockData{}), true, "RockData"),
			r.Schema(reflect.TypeOf(domain.MeteoriteData{}), true, "MeteoriteData"),
		},
		Description: "Polymorphic type-specific fields. The matching variant is " +
			"determined by the parent specimen's `type`: MineralData when " +
			"type=mineral, RockData when type=rock, MeteoriteData when " +
			"type=meteorite. Server-side validation enforces the " +
			"matching shape after dispatching on `type`.",
	}
}

// SpecimenView is the response DTO for a single specimen.
type SpecimenView struct {
	ID            string             `json:"id"`
	Type          string             `json:"type"`
	CatalogNumber *string            `json:"catalog_number,omitempty"`
	Name          string             `json:"name"`
	Description   string             `json:"description"`
	Visibility    string             `json:"visibility"`
	AuthorID      string             `json:"author_id"`
	AcquiredAt    *string            `json:"acquired_at,omitempty"`
	AcquiredFrom  *string            `json:"acquired_from,omitempty"`
	PriceCents    *int64             `json:"price_cents,omitempty"`
	SourceNotes   *string            `json:"source_notes,omitempty"`
	LocalityText  *string            `json:"locality_text,omitempty"`
	Locality      *domain.Locality   `json:"locality,omitempty"`
	MassG         *float64           `json:"mass_g,omitempty"`
	Dimensions    *domain.Dimensions `json:"dimensions,omitempty"`
	TypeData      SpecimenTypeData   `json:"type_data"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

// SpecimenCreateBody is the request DTO for POST /specimens.
type SpecimenCreateBody struct {
	Type          string             `json:"type" enum:"mineral,rock,meteorite" doc:"discriminator for type_data"`
	Name          string             `json:"name" minLength:"1"`
	Description   string             `json:"description,omitempty"`
	Visibility    string             `json:"visibility,omitempty" enum:"private,unlisted,public"`
	CatalogNumber *string            `json:"catalog_number,omitempty"`
	AcquiredAt    *string            `json:"acquired_at,omitempty" format:"date"`
	AcquiredFrom  *string            `json:"acquired_from,omitempty"`
	PriceCents    *int64             `json:"price_cents,omitempty"`
	SourceNotes   *string            `json:"source_notes,omitempty"`
	LocalityText  *string            `json:"locality_text,omitempty"`
	Locality      *domain.Locality   `json:"locality,omitempty"`
	MassG         *float64           `json:"mass_g,omitempty"`
	Dimensions    *domain.Dimensions `json:"dimensions,omitempty"`
	TypeData      SpecimenTypeData   `json:"type_data,omitempty"`
}

// SpecimenPatchBody is the request DTO for PATCH /specimens/{id}.
// Every field is optional (pointer = absent when nil). The TypeData
// field uses *json.RawMessage so the handler can distinguish "field
// missing" (nil pointer) from "merge this object" (non-nil) — see
// PATCH semantics in the bead.
type SpecimenPatchBody struct {
	Name          *string            `json:"name,omitempty" minLength:"1"`
	Description   *string            `json:"description,omitempty"`
	Visibility    *string            `json:"visibility,omitempty" enum:"private,unlisted,public"`
	CatalogNumber *string            `json:"catalog_number,omitempty"`
	AcquiredAt    *string            `json:"acquired_at,omitempty" format:"date"`
	AcquiredFrom  *string            `json:"acquired_from,omitempty"`
	PriceCents    *int64             `json:"price_cents,omitempty"`
	SourceNotes   *string            `json:"source_notes,omitempty"`
	LocalityText  *string            `json:"locality_text,omitempty"`
	Locality      *domain.Locality   `json:"locality,omitempty"`
	MassG         *float64           `json:"mass_g,omitempty"`
	Dimensions    *domain.Dimensions `json:"dimensions,omitempty"`
	TypeData      *json.RawMessage   `json:"type_data,omitempty"`
	// Type is rejected with 409 when present and different from the
	// stored type. Documented in the operation description.
	Type *string `json:"type,omitempty"`
}

// SpecimensListInput holds the LIST query parameters.
type SpecimensListInput struct {
	Limit            int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Cursor           string `query:"cursor"`
	Type             string `query:"type" enum:"mineral,rock,meteorite"`
	Visibility       string `query:"visibility" enum:"private,unlisted,public"`
	HasCatalogNumber string `query:"has_catalog_number" enum:"true,false"`
	AcquiredAfter    string `query:"acquired_after" format:"date"`
	AcquiredBefore   string `query:"acquired_before" format:"date"`
	Query            string `query:"q"`
	CollectorID      string `query:"collector_id" format:"uuid" doc:"v1 stub: returns empty list pending B-4"`
}

// SpecimensListBody is the LIST response shape.
type SpecimensListBody struct {
	Items      []SpecimenView `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

type specimenListOutput struct {
	Body SpecimensListBody `json:"body"`
}

type specimenViewOutput struct {
	Body SpecimenView `json:"body"`
}

type specimenCreatedOutput struct {
	Location string       `header:"Location"`
	Body     SpecimenView `json:"body"`
}

type specimenIDPath struct {
	ID string `path:"id" format:"uuid"`
}

type specimenCreateInput struct {
	Body SpecimenCreateBody
}

type specimenPatchInput struct {
	ID   string `path:"id" format:"uuid"`
	Body SpecimenPatchBody
}

type specimenDeleteOutput struct{}

// makeListSpecimensHandler builds the GET /api/v1/specimens handler.
func makeListSpecimensHandler(sd SpecimensDeps) func(context.Context, *SpecimensListInput) (*specimenListOutput, error) {
	return func(ctx context.Context, in *SpecimensListInput) (*specimenListOutput, error) {
		filter := domain.SpecimenFilter{Query: in.Query}
		if in.Type != "" {
			t := domain.SpecimenType(in.Type)
			filter.Type = &t
		}
		if in.Visibility != "" {
			v := domain.Visibility(in.Visibility)
			filter.Visibility = &v
		}
		if in.HasCatalogNumber != "" {
			b := in.HasCatalogNumber == "true"
			filter.HasCatalogNumber = &b
		}
		if in.AcquiredAfter != "" {
			if _, err := time.Parse(dateLayout, in.AcquiredAfter); err != nil {
				return nil, huma.Error400BadRequest("acquired_after must be YYYY-MM-DD")
			}
			s := in.AcquiredAfter
			filter.AcquiredAfter = &s
		}
		if in.AcquiredBefore != "" {
			if _, err := time.Parse(dateLayout, in.AcquiredBefore); err != nil {
				return nil, huma.Error400BadRequest("acquired_before must be YYYY-MM-DD")
			}
			s := in.AcquiredBefore
			filter.AcquiredBefore = &s
		}
		if filter.AcquiredAfter != nil && filter.AcquiredBefore != nil &&
			*filter.AcquiredAfter > *filter.AcquiredBefore {
			return nil, huma.Error422UnprocessableEntity(
				"acquired_after must be <= acquired_before")
		}
		if in.CollectorID != "" {
			cid, err := uuid.Parse(in.CollectorID)
			if err != nil {
				return nil, huma.Error400BadRequest("collector_id must be a uuid")
			}
			filter.CollectorID = &cid
		}

		page := domain.Page{Limit: in.Limit, Cursor: in.Cursor}
		items, next, err := sd.Repo.List(ctx, filter, page)
		if err != nil {
			return nil, fmt.Errorf("list specimens: %w", err)
		}

		out := SpecimensListBody{
			Items: make([]SpecimenView, 0, len(items)),
		}
		for _, s := range items {
			out.Items = append(out.Items, toSpecimenView(s))
		}
		if next != "" {
			s := string(next)
			out.NextCursor = &s
		}
		return &specimenListOutput{Body: out}, nil
	}
}

// makeCreateSpecimenHandler builds the POST handler. Validates type
// + type_data, generates the UUIDv7 id, populates author_id from
// auth context, writes the row, and returns the created view.
func makeCreateSpecimenHandler(sd SpecimensDeps) func(context.Context, *specimenCreateInput) (*specimenCreatedOutput, error) {
	return func(ctx context.Context, in *specimenCreateInput) (*specimenCreatedOutput, error) {
		body := in.Body

		t, err := parseSpecimenType(body.Type)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		visibility, err := parseVisibility(body.Visibility)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		td := []byte(body.TypeData)
		if len(td) == 0 {
			td = []byte("{}")
		}
		if err := domain.ValidateTypeData(t, td); err != nil {
			return nil, huma.Error422UnprocessableEntity("invalid type_data: " + err.Error())
		}

		acquiredAt, err := parseDate(body.AcquiredAt)
		if err != nil {
			return nil, huma.Error400BadRequest("acquired_at: " + err.Error())
		}

		now := time.Now().UTC()
		user := auth.FromContext(ctx)
		s := domain.Specimen{
			ID:            domain.NewID(),
			Type:          t,
			CatalogNumber: body.CatalogNumber,
			Name:          body.Name,
			Description:   body.Description,
			Visibility:    visibility,
			AuthorID:      user.ID,
			AcquiredAt:    acquiredAt,
			AcquiredFrom:  body.AcquiredFrom,
			PriceCents:    body.PriceCents,
			SourceNotes:   body.SourceNotes,
			LocalityText:  body.LocalityText,
			Locality:      body.Locality,
			MassG:         body.MassG,
			Dimensions:    body.Dimensions,
			TypeData:      td,
			CreatedAt:     now,
			UpdatedAt:     now,
		}

		if err := sd.Repo.Create(ctx, sd.Pool, s); err != nil {
			if errors.Is(err, domain.ErrSpecimenConflict) {
				return nil, huma.Error409Conflict(
					"catalog_number must be unique")
			}
			return nil, fmt.Errorf("create specimen: %w", err)
		}

		got, err := sd.Repo.GetByID(ctx, s.ID)
		if err != nil {
			return nil, fmt.Errorf("create specimen: reload: %w", err)
		}
		return &specimenCreatedOutput{
			Location: "/api/v1/specimens/" + s.ID.String(),
			Body:     toSpecimenView(got),
		}, nil
	}
}

// makeGetSpecimenHandler builds GET /api/v1/specimens/{id}.
func makeGetSpecimenHandler(sd SpecimensDeps) func(context.Context, *specimenIDPath) (*specimenViewOutput, error) {
	return func(ctx context.Context, in *specimenIDPath) (*specimenViewOutput, error) {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("id must be a uuid")
		}
		s, err := sd.Repo.GetByID(ctx, id)
		if err != nil {
			if errors.Is(err, domain.ErrSpecimenNotFound) {
				return nil, huma.Error404NotFound("specimen not found")
			}
			return nil, fmt.Errorf("get specimen: %w", err)
		}
		return &specimenViewOutput{Body: toSpecimenView(s)}, nil
	}
}

// makePatchSpecimenHandler builds PATCH /api/v1/specimens/{id}. The
// implementation loads the existing row, applies provided fields,
// merges type_data at the top level, then rewrites in a single
// transaction.
func makePatchSpecimenHandler(sd SpecimensDeps) func(context.Context, *specimenPatchInput) (*specimenViewOutput, error) {
	return func(ctx context.Context, in *specimenPatchInput) (*specimenViewOutput, error) {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("id must be a uuid")
		}
		body := in.Body

		var updated domain.Specimen
		err = db.RunInTx(ctx, sd.Pool, func(tx pgx.Tx) error {
			cur, gerr := sd.Repo.GetByID(ctx, id)
			if gerr != nil {
				return gerr
			}

			if body.Type != nil && *body.Type != string(cur.Type) {
				return domain.ErrSpecimenTypeImmutable
			}

			if body.Name != nil {
				cur.Name = *body.Name
			}
			if body.Description != nil {
				cur.Description = *body.Description
			}
			if body.Visibility != nil {
				v, verr := parseVisibility(*body.Visibility)
				if verr != nil {
					return huma.Error400BadRequest(verr.Error())
				}
				cur.Visibility = v
			}
			if body.CatalogNumber != nil {
				cn := *body.CatalogNumber
				if cn == "" {
					cur.CatalogNumber = nil
				} else {
					cur.CatalogNumber = &cn
				}
			}
			if body.AcquiredAt != nil {
				d, derr := parseDate(body.AcquiredAt)
				if derr != nil {
					return huma.Error400BadRequest("acquired_at: " + derr.Error())
				}
				cur.AcquiredAt = d
			}
			if body.AcquiredFrom != nil {
				cur.AcquiredFrom = body.AcquiredFrom
			}
			if body.PriceCents != nil {
				cur.PriceCents = body.PriceCents
			}
			if body.SourceNotes != nil {
				cur.SourceNotes = body.SourceNotes
			}
			if body.LocalityText != nil {
				cur.LocalityText = body.LocalityText
			}
			if body.Locality != nil {
				cur.Locality = body.Locality
			}
			if body.MassG != nil {
				cur.MassG = body.MassG
			}
			if body.Dimensions != nil {
				cur.Dimensions = body.Dimensions
			}

			if body.TypeData != nil {
				merged, merr := mergeTypeData(cur.TypeData, *body.TypeData)
				if merr != nil {
					return huma.Error400BadRequest("type_data: " + merr.Error())
				}
				if verr := domain.ValidateTypeData(cur.Type, merged); verr != nil {
					return huma.Error422UnprocessableEntity(
						"invalid type_data: " + verr.Error())
				}
				cur.TypeData = merged
			}

			cur.UpdatedAt = time.Now().UTC()
			updated = cur
			return sd.Repo.Update(ctx, tx, cur)
		})
		if err != nil {
			return nil, mapPatchError(err)
		}

		// Re-read so the response reflects database-side state
		// (e.g. the updated_at timestamp at storage precision).
		got, err := sd.Repo.GetByID(ctx, updated.ID)
		if err != nil {
			return nil, fmt.Errorf("patch specimen: reload: %w", err)
		}
		return &specimenViewOutput{Body: toSpecimenView(got)}, nil
	}
}

// makeDeleteSpecimenHandler builds DELETE /api/v1/specimens/{id}.
// Wrapped in a transaction so the FK existence check and the delete
// can't race against a concurrent child insert.
func makeDeleteSpecimenHandler(sd SpecimensDeps) func(context.Context, *specimenIDPath) (*specimenDeleteOutput, error) {
	return func(ctx context.Context, in *specimenIDPath) (*specimenDeleteOutput, error) {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return nil, huma.Error400BadRequest("id must be a uuid")
		}
		err = db.RunInTx(ctx, sd.Pool, func(tx pgx.Tx) error {
			return sd.Repo.Delete(ctx, tx, id)
		})
		if err != nil {
			switch {
			case errors.Is(err, domain.ErrSpecimenNotFound):
				return nil, huma.Error404NotFound("specimen not found")
			case errors.Is(err, domain.ErrSpecimenReferenced):
				return nil, huma.Error409Conflict(
					"specimen has dependent photos or journal_entries; delete those first")
			}
			return nil, fmt.Errorf("delete specimen: %w", err)
		}
		return &specimenDeleteOutput{}, nil
	}
}

// mapPatchError translates errors raised during a transactional
// PATCH back into the wire layer. huma.StatusError values pass
// through unchanged; domain sentinels become matching huma errors.
func mapPatchError(err error) error {
	var se huma.StatusError
	if errors.As(err, &se) {
		return err
	}
	switch {
	case errors.Is(err, domain.ErrSpecimenNotFound):
		return huma.Error404NotFound("specimen not found")
	case errors.Is(err, domain.ErrSpecimenTypeImmutable):
		return huma.Error409Conflict(
			"type is immutable; reclassify by delete + recreate")
	case errors.Is(err, domain.ErrSpecimenConflict):
		return huma.Error409Conflict("catalog_number must be unique")
	}
	return fmt.Errorf("patch specimen: %w", err)
}

// mergeTypeData applies the patch JSON object on top of the current
// type_data: missing keys keep their value, present keys overwrite,
// `null` clears. The result is the new bytes to store.
//
// The function refuses non-object PATCH input — the wire contract
// shapes type_data as a JSON object across all three variants.
func mergeTypeData(current, patch []byte) ([]byte, error) {
	if len(patch) == 0 || string(patch) == "null" {
		return nil, errors.New("must be a JSON object, not null")
	}
	var pm map[string]json.RawMessage
	if err := json.Unmarshal(patch, &pm); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}

	cm := map[string]json.RawMessage{}
	if len(current) > 0 {
		if err := json.Unmarshal(current, &cm); err != nil {
			// Stored bytes should always be a valid object — if
			// not, the migration or a previous write is at fault.
			return nil, fmt.Errorf("stored type_data is not an object: %w", err)
		}
	}
	for k, v := range pm {
		if string(v) == "null" {
			delete(cm, k)
			continue
		}
		cm[k] = v
	}
	out, err := json.Marshal(cm)
	if err != nil {
		return nil, fmt.Errorf("encode merged type_data: %w", err)
	}
	return out, nil
}

// parseSpecimenType validates the wire enum.
func parseSpecimenType(s string) (domain.SpecimenType, error) {
	switch s {
	case string(domain.SpecimenMineral),
		string(domain.SpecimenRock),
		string(domain.SpecimenMeteorite):
		return domain.SpecimenType(s), nil
	}
	return "", fmt.Errorf("type must be one of mineral|rock|meteorite, got %q", s)
}

// parseVisibility validates the wire enum, defaulting to "private"
// per the schema's column default.
func parseVisibility(s string) (domain.Visibility, error) {
	if s == "" {
		return domain.VisibilityPrivate, nil
	}
	switch s {
	case string(domain.VisibilityPrivate),
		string(domain.VisibilityUnlisted),
		string(domain.VisibilityPublic):
		return domain.Visibility(s), nil
	}
	return "", fmt.Errorf("visibility must be one of private|unlisted|public, got %q", s)
}

// parseDate parses an optional YYYY-MM-DD string into *time.Time
// (midnight UTC). nil/empty input yields a nil pointer.
func parseDate(s *string) (*time.Time, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	t, err := time.Parse(dateLayout, *s)
	if err != nil {
		return nil, fmt.Errorf("must be YYYY-MM-DD")
	}
	return &t, nil
}

// toSpecimenView converts a domain.Specimen into the wire DTO.
func toSpecimenView(s domain.Specimen) SpecimenView {
	v := SpecimenView{
		ID:            s.ID.String(),
		Type:          string(s.Type),
		CatalogNumber: s.CatalogNumber,
		Name:          s.Name,
		Description:   s.Description,
		Visibility:    string(s.Visibility),
		AuthorID:      s.AuthorID.String(),
		AcquiredFrom:  s.AcquiredFrom,
		PriceCents:    s.PriceCents,
		SourceNotes:   s.SourceNotes,
		LocalityText:  s.LocalityText,
		Locality:      s.Locality,
		MassG:         s.MassG,
		Dimensions:    s.Dimensions,
		TypeData:      SpecimenTypeData(s.TypeData),
		CreatedAt:     s.CreatedAt,
		UpdatedAt:     s.UpdatedAt,
	}
	if s.AcquiredAt != nil {
		d := s.AcquiredAt.UTC().Format(dateLayout)
		v.AcquiredAt = &d
	}
	return v
}

// registerSpecimenStubs registers the operations with placeholder
// handlers that always return 503. This branch is taken when the
// process is built without a DB pool (e.g. the `openapi` subcommand
// running against an empty deps struct), so the spec still includes
// the operations.
func registerSpecimenStubs(api huma.API) {
	noDB := func(_ context.Context, _ *struct{}) (*specimenViewOutput, error) {
		return nil, huma.Error503ServiceUnavailable("specimen handlers require a database connection")
	}
	noDBList := func(_ context.Context, _ *SpecimensListInput) (*specimenListOutput, error) {
		return nil, huma.Error503ServiceUnavailable("specimen handlers require a database connection")
	}

	huma.Register(api, huma.Operation{
		OperationID: "list-specimens",
		Method:      http.MethodGet,
		Path:        "/api/v1/specimens",
		Summary:     "List specimens",
		Tags:        []string{"specimens"},
	}, noDBList)

	huma.Register(api, huma.Operation{
		OperationID:   "create-specimen",
		Method:        http.MethodPost,
		Path:          "/api/v1/specimens",
		Summary:       "Create a specimen",
		DefaultStatus: http.StatusCreated,
		Tags:          []string{"specimens"},
		Errors:        []int{http.StatusServiceUnavailable},
	}, func(_ context.Context, _ *specimenCreateInput) (*specimenCreatedOutput, error) {
		return nil, huma.Error503ServiceUnavailable("specimen handlers require a database connection")
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-specimen",
		Method:      http.MethodGet,
		Path:        "/api/v1/specimens/{id}",
		Summary:     "Get a specimen by id",
		Tags:        []string{"specimens"},
	}, func(_ context.Context, _ *specimenIDPath) (*specimenViewOutput, error) {
		return noDB(nil, nil)
	})

	huma.Register(api, huma.Operation{
		OperationID: "patch-specimen",
		Method:      http.MethodPatch,
		Path:        "/api/v1/specimens/{id}",
		Summary:     "Update a specimen",
		Tags:        []string{"specimens"},
	}, func(_ context.Context, _ *specimenPatchInput) (*specimenViewOutput, error) {
		return nil, huma.Error503ServiceUnavailable("specimen handlers require a database connection")
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-specimen",
		Method:        http.MethodDelete,
		Path:          "/api/v1/specimens/{id}",
		Summary:       "Delete a specimen",
		DefaultStatus: http.StatusNoContent,
		Tags:          []string{"specimens"},
	}, func(_ context.Context, _ *specimenIDPath) (*specimenDeleteOutput, error) {
		return nil, huma.Error503ServiceUnavailable("specimen handlers require a database connection")
	})
}
