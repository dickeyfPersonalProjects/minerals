// Collectors HTTP surface (mi-yvt / B-1). Implements the five §10
// CRUD endpoints on top of the domain.CollectorRepo interface:
//
//	GET    /api/v1/collectors
//	POST   /api/v1/collectors
//	GET    /api/v1/collectors/{id}
//	PATCH  /api/v1/collectors/{id}
//	DELETE /api/v1/collectors/{id}
//
// Pagination follows CONTRACT.md §10.3 (cursor + limit, default 50,
// max 200). Error responses use the §10 envelope via apiError.
package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// CollectorView is the wire shape of a collector resource. Field
// names are snake_case and match the database column names; the
// frontend client is regenerated from this type's OpenAPI schema.
type CollectorView struct {
	ID        uuid.UUID `json:"id" doc:"UUIDv7 primary key."`
	Name      string    `json:"name" doc:"Display name; unique across all collectors."`
	Notes     *string   `json:"notes" doc:"Optional free-form notes; null when unset."`
	AuthorID  uuid.UUID `json:"author_id" doc:"UUID of the user who created the row (CONTRACT.md §13)."`
	CreatedAt time.Time `json:"created_at" doc:"RFC 3339 creation timestamp (timestamptz)."`
	UpdatedAt time.Time `json:"updated_at" doc:"RFC 3339 last-update timestamp (timestamptz)."`
}

func toCollectorView(c domain.Collector) CollectorView {
	return CollectorView{
		ID:        c.ID,
		Name:      c.Name,
		Notes:     c.Notes,
		AuthorID:  c.AuthorID,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}
}

// listCollectorsInput is the typed query-string surface for
// GET /api/v1/collectors.
type listCollectorsInput struct {
	Limit  int    `query:"limit" minimum:"1" maximum:"200" doc:"Page size (1-200; defaults to 50, values above 200 silently clamped)."`
	Cursor string `query:"cursor" doc:"Opaque pagination cursor returned by the previous page (CONTRACT.md §10.3)."`
	Q      string `query:"q" doc:"Free-form name filter (case-insensitive substring match)."`
}

type listCollectorsOutput struct {
	Body collectorListBody
}

type collectorListBody struct {
	Items      []CollectorView `json:"items" doc:"Page of collectors in (created_at DESC, id DESC) order."`
	NextCursor *string         `json:"next_cursor" doc:"Cursor for the next page; null at end of results."`
}

type getCollectorInput struct {
	ID string `path:"id" doc:"Collector UUID."`
}

type collectorResponseOutput struct {
	Body CollectorView
}

type createCollectorInput struct {
	Body createCollectorBody
}

type createCollectorBody struct {
	Name  string  `json:"name" minLength:"1" maxLength:"200" doc:"Display name; must be unique."`
	Notes *string `json:"notes,omitempty" doc:"Optional free-form notes."`
}

type createCollectorOutput struct {
	Location string `header:"Location" doc:"URL of the newly created collector."`
	Body     CollectorView
}

type patchCollectorInput struct {
	ID   string `path:"id" doc:"Collector UUID."`
	Body patchCollectorBody
}

// patchCollectorBody uses pointers so a missing field stays untouched
// and `null` clears nullable fields. Per CONTRACT.md §10 PATCH
// semantics: omitted fields preserve existing values.
type patchCollectorBody struct {
	Name  *string `json:"name,omitempty" minLength:"1" maxLength:"200" doc:"New display name; omit to leave unchanged."`
	Notes *string `json:"notes,omitempty" doc:"New notes; omit to leave unchanged. Pass JSON null to clear."`
}

type deleteCollectorInput struct {
	ID string `path:"id" doc:"Collector UUID."`
}

// deleteCollectorOutput has no body or headers — huma writes the
// configured DefaultStatus (204) when the handler returns nil.
type deleteCollectorOutput struct{}

// CollectorService wires huma operations against a domain.CollectorRepo.
// Construct one in api.New() when deps.CollectorRepo is non-nil.
type CollectorService struct {
	repo  domain.CollectorRepo
	authz authzGuard
}

func registerCollectorOperations(api huma.API, authMW authMiddlewares, guard authzGuard, repo domain.CollectorRepo) {
	if repo == nil {
		return
	}
	s := &CollectorService{repo: repo, authz: guard}
	mws := authMW.Protected()
	optionalMWs := authMW.Optional()

	huma.Register(api, huma.Operation{
		OperationID: "list-collectors",
		Method:      http.MethodGet,
		Path:        "/api/v1/collectors",
		Summary:     "List collectors",
		Description: "Cursor-paginated list of collectors. Default ordering is `created_at DESC, id DESC`. Pass `?q=<text>` for substring match on name (case-insensitive). " +
			"Anonymous callers receive an empty list — collectors are owned per-user with no public tier (CONTRACT.md §13 v2).",
		Tags:        []string{"collectors"},
		Errors:      []int{http.StatusBadRequest},
		Middlewares: optionalMWs,
	}, s.list)

	huma.Register(api, huma.Operation{
		OperationID:   "create-collector",
		Method:        http.MethodPost,
		Path:          "/api/v1/collectors",
		Summary:       "Create a collector",
		Description:   "Creates a new collector. Returns 409 when `name` is non-unique.",
		Tags:          []string{"collectors"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusConflict},
		Middlewares:   mws,
	}, s.create)

	huma.Register(api, huma.Operation{
		OperationID: "get-collector",
		Method:      http.MethodGet,
		Path:        "/api/v1/collectors/{id}",
		Summary:     "Get a collector by id",
		Description: "Returns 404 (not 403/401) when the caller does not own the collector (CONTRACT.md §13 v2 don't-leak-existence rule).",
		Tags:        []string{"collectors"},
		Errors:      []int{http.StatusBadRequest, http.StatusNotFound},
		Middlewares: optionalMWs,
	}, s.get)

	huma.Register(api, huma.Operation{
		OperationID: "patch-collector",
		Method:      http.MethodPatch,
		Path:        "/api/v1/collectors/{id}",
		Summary:     "Update a collector",
		Description: "Partial update; omitted fields keep their previous values. Returns 409 when `name` would collide with another collector.",
		Tags:        []string{"collectors"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict},
		Middlewares: mws,
	}, s.patch)

	huma.Register(api, huma.Operation{
		OperationID:   "delete-collector",
		Method:        http.MethodDelete,
		Path:          "/api/v1/collectors/{id}",
		Summary:       "Delete a collector",
		Description:   "Deletes the collector. Returns 409 when the collector is still referenced by `specimen_collectors`.",
		Tags:          []string{"collectors"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict},
		Middlewares:   mws,
	}, s.delete)
}

func (s *CollectorService) list(ctx context.Context, in *listCollectorsInput) (*listCollectorsOutput, error) {
	page := domain.Page{Limit: in.Limit, Cursor: in.Cursor}
	filter := domain.CollectorFilter{Query: in.Q}
	rows, cursor, err := s.repo.List(ctx, filter, page)
	if err != nil {
		return nil, mapListError(err)
	}
	items := make([]CollectorView, 0, len(rows))
	for _, r := range rows {
		items = append(items, toCollectorView(r))
	}
	body := collectorListBody{Items: items}
	if cursor != "" {
		c := string(cursor)
		body.NextCursor = &c
	}
	return &listCollectorsOutput{Body: body}, nil
}

func (s *CollectorService) get(ctx context.Context, in *getCollectorInput) (*collectorResponseOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}
	c, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, mapDomainError(err)
	}
	if err := s.authz.checkView(ctx, collectorResource(c),
		"collector_not_found", "no such collector"); err != nil {
		return nil, err
	}
	return &collectorResponseOutput{Body: toCollectorView(c)}, nil
}

func (s *CollectorService) create(ctx context.Context, in *createCollectorInput) (*createCollectorOutput, error) {
	name := strings.TrimSpace(in.Body.Name)
	if name == "" {
		return nil, newAPIError(http.StatusBadRequest, "invalid_name",
			"name is required", nil)
	}

	authorID := auth.FromContext(ctx).ID
	if err := s.authz.check(ctx, ownedResource("collectors", authorID), actCreate); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	c := domain.Collector{
		ID:        domain.NewID(),
		Name:      name,
		Notes:     in.Body.Notes,
		AuthorID:  authorID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.Create(ctx, nil, c); err != nil {
		return nil, mapDomainError(err)
	}
	out := &createCollectorOutput{
		Location: "/api/v1/collectors/" + c.ID.String(),
		Body:     toCollectorView(c),
	}
	return out, nil
}

func (s *CollectorService) patch(ctx context.Context, in *patchCollectorInput) (*collectorResponseOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}

	current, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, mapDomainError(err)
	}
	if err := s.authz.check(ctx, collectorResource(current), actEdit); err != nil {
		return nil, err
	}

	if in.Body.Name != nil {
		trimmed := strings.TrimSpace(*in.Body.Name)
		if trimmed == "" {
			return nil, newAPIError(http.StatusBadRequest, "invalid_name",
				"name must be non-empty when provided", nil)
		}
		current.Name = trimmed
	}
	if in.Body.Notes != nil {
		// JSON `null` for an *string field surfaces as a present-but-nil
		// pointer in `Notes` (omitempty would suppress). Convert: caller
		// wanting to clear notes sends `"notes": null`. We accept the
		// pointer as-is — empty-string is a valid value too.
		notes := *in.Body.Notes
		current.Notes = &notes
	}
	current.UpdatedAt = time.Now().UTC()

	if err := s.repo.Update(ctx, nil, current); err != nil {
		return nil, mapDomainError(err)
	}
	return &collectorResponseOutput{Body: toCollectorView(current)}, nil
}

func (s *CollectorService) delete(ctx context.Context, in *deleteCollectorInput) (*deleteCollectorOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}
	current, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, mapDomainError(err)
	}
	if err := s.authz.check(ctx, collectorResource(current), actDelete); err != nil {
		return nil, err
	}
	if err := s.repo.Delete(ctx, nil, id); err != nil {
		return nil, mapDomainError(err)
	}
	return &deleteCollectorOutput{}, nil
}

// mapDomainError translates repo sentinels into §10 envelope errors.
// Any other error becomes an opaque 500 with a generic message
// (matching CONTRACT.md §10's "no SQL fragments" rule).
func mapDomainError(err error) error {
	switch {
	case errors.Is(err, domain.ErrCollectorNotFound):
		return newAPIError(http.StatusNotFound, "collector_not_found",
			"no such collector", nil)
	case errors.Is(err, domain.ErrCollectorConflict):
		return newAPIError(http.StatusConflict, "collector_conflict",
			"a collector with that name already exists",
			map[string]any{"field": "name", "constraint": "unique"})
	case errors.Is(err, domain.ErrCollectorReferenced):
		return newAPIError(http.StatusConflict, "collector_referenced",
			"collector is still linked to one or more specimens",
			map[string]any{"constraint": "specimen_collectors_fk"})
	}
	return newAPIError(http.StatusInternalServerError, "internal_error",
		"internal server error", nil)
}

// mapListError handles the (rare) error class List can return — a
// malformed cursor is the only client-visible case.
func mapListError(err error) error {
	if strings.Contains(err.Error(), "cursor:") {
		return newAPIError(http.StatusBadRequest, "invalid_cursor",
			"cursor is malformed", nil)
	}
	return newAPIError(http.StatusInternalServerError, "internal_error",
		"internal server error", nil)
}

func parseUUID(raw, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, newAPIError(http.StatusBadRequest, "invalid_id",
			"id must be a UUID",
			map[string]any{"field": field})
	}
	return id, nil
}
