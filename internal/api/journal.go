// Journal entries HTTP surface (mi-y6b / C-1). Implements the §10
// CRUD endpoints for the per-specimen observation log:
//
//	POST   /api/v1/specimens/{id}/journal
//	GET    /api/v1/specimens/{id}/journal
//	GET    /api/v1/journal/{id}
//	PATCH  /api/v1/journal/{id}
//	DELETE /api/v1/journal/{id}
//
// body_md is editable for typo fixes; created_at is immutable per
// design §2 — the server sets it on Create and never accepts a value
// from the client. The wire payload includes both body_md (raw) and
// body_html (server-rendered via the §17 markdown pipeline).
package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/markdown"
)

// MarkdownRenderer is the subset of *markdown.Renderer the journal
// service uses. Defining it here lets tests substitute a stub.
type MarkdownRenderer interface {
	RenderString(src string) (string, error)
}

// JournalView is the wire shape of a journal entry. body_html is the
// sanitized HTML rendered server-side; body_md is the raw markdown
// the client originally submitted.
type JournalView struct {
	ID         uuid.UUID `json:"id" doc:"UUIDv7 primary key."`
	SpecimenID uuid.UUID `json:"specimen_id" doc:"Owning specimen."`
	AuthorID   uuid.UUID `json:"author_id" doc:"UUID of the user who created the entry (CONTRACT.md §13)."`
	BodyMD     string    `json:"body_md" doc:"Raw markdown source (editable for typo fixes)."`
	BodyHTML   string    `json:"body_html" doc:"Server-rendered, sanitized HTML (CONTRACT.md §17 pipeline)."`
	CreatedAt  time.Time `json:"created_at" doc:"RFC 3339 creation timestamp; immutable after creation."`
	UpdatedAt  time.Time `json:"updated_at" doc:"RFC 3339 last-update timestamp."`
}

// JournalServiceDeps carries everything the journal handlers need.
type JournalServiceDeps struct {
	Entries  domain.JournalEntryRepo
	Markdown MarkdownRenderer
}

// JournalService wires huma operations against a JournalServiceDeps.
type JournalService struct {
	deps  JournalServiceDeps
	authz authzGuard
}

func registerJournalOperations(api huma.API, authMW authMiddlewares, guard authzGuard, deps *JournalServiceDeps) {
	if deps == nil || deps.Entries == nil {
		return
	}
	if deps.Markdown == nil {
		deps.Markdown = markdown.NewRenderer()
	}
	s := &JournalService{deps: *deps, authz: guard}
	mws := authMW.Protected()

	huma.Register(api, huma.Operation{
		OperationID:   "create-journal-entry",
		Method:        http.MethodPost,
		Path:          "/api/v1/specimens/{id}/journal",
		Summary:       "Create a journal entry for a specimen",
		Description:   "Creates an entry whose body is rendered server-side via the CONTRACT.md §17 markdown pipeline. `created_at` is server-set and immutable after creation.",
		Tags:          []string{"journal"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusUnprocessableEntity},
		Middlewares:   mws,
	}, s.create)

	huma.Register(api, huma.Operation{
		OperationID: "list-specimen-journal-entries",
		Method:      http.MethodGet,
		Path:        "/api/v1/specimens/{id}/journal",
		Summary:     "List a specimen's journal entries",
		Description: "Cursor-paginated list ordered `created_at DESC, id DESC` (CONTRACT.md §10.3). Each entry includes `body_html` alongside `body_md`.",
		Tags:        []string{"journal"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized},
		Middlewares: mws,
	}, s.list)

	huma.Register(api, huma.Operation{
		OperationID: "get-journal-entry",
		Method:      http.MethodGet,
		Path:        "/api/v1/journal/{id}",
		Summary:     "Get a journal entry by id",
		Tags:        []string{"journal"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Middlewares: mws,
	}, s.get)

	huma.Register(api, huma.Operation{
		OperationID: "patch-journal-entry",
		Method:      http.MethodPatch,
		Path:        "/api/v1/journal/{id}",
		Summary:     "Update a journal entry",
		Description: "Updates `body_md` (and re-renders `body_html`). `created_at` is immutable; supplying it in the body is rejected with 400.",
		Tags:        []string{"journal"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusUnprocessableEntity},
		Middlewares: mws,
	}, s.patch)

	huma.Register(api, huma.Operation{
		OperationID:   "delete-journal-entry",
		Method:        http.MethodDelete,
		Path:          "/api/v1/journal/{id}",
		Summary:       "Delete a journal entry",
		Description:   "Returns 409 when the entry still has attachments (`journal_entry_files` rows). Attachment deletion lands in C-2.",
		Tags:          []string{"journal"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict},
		Middlewares:   mws,
	}, s.delete)
}

type createJournalInput struct {
	SpecimenID string `path:"id" doc:"Specimen UUID."`
	Body       createJournalBody
}

type createJournalBody struct {
	BodyMD string `json:"body_md" minLength:"1" maxLength:"100000" doc:"Markdown source for the entry. Server renders to HTML at write and read time."`
}

type createJournalOutput struct {
	Location string `header:"Location" doc:"URL of the newly created entry."`
	Body     JournalView
}

type listJournalInput struct {
	SpecimenID string `path:"id" doc:"Specimen UUID."`
	Limit      int    `query:"limit" minimum:"1" maximum:"200" doc:"Page size (1-200; defaults to 50, values above 200 silently clamped)."`
	Cursor     string `query:"cursor" doc:"Opaque pagination cursor returned by the previous page (CONTRACT.md §10.3)."`
}

type listJournalOutput struct {
	Body journalListBody
}

type journalListBody struct {
	Items      []JournalView `json:"items" doc:"Page of journal entries (most-recent first)."`
	NextCursor *string       `json:"next_cursor" doc:"Cursor for the next page; null at end of results."`
}

type getJournalInput struct {
	ID string `path:"id" doc:"Journal entry UUID."`
}

type journalResponseOutput struct {
	Body JournalView
}

type patchJournalInput struct {
	ID   string `path:"id" doc:"Journal entry UUID."`
	Body patchJournalBody
}

// patchJournalBody uses pointers so omitted fields stay untouched.
// CreatedAt is included intentionally so the handler can reject any
// attempt to send it (the JSON tag is the same as the read-side
// field; clients mistakenly round-tripping the GET payload would
// otherwise mutate immutable state).
type patchJournalBody struct {
	BodyMD    *string    `json:"body_md,omitempty" minLength:"1" maxLength:"100000" doc:"New markdown source. Omit to leave unchanged."`
	CreatedAt *time.Time `json:"created_at,omitempty" doc:"REJECTED. created_at is immutable per design §2; supplying any value (including the existing one) returns 400."`
}

type deleteJournalInput struct {
	ID string `path:"id" doc:"Journal entry UUID."`
}

type deleteJournalOutput struct{}

func (s *JournalService) create(ctx context.Context, in *createJournalInput) (*createJournalOutput, error) {
	specimenID, err := parseUUID(in.SpecimenID, "id")
	if err != nil {
		return nil, err
	}
	if in.Body.BodyMD == "" {
		return nil, newAPIError(http.StatusBadRequest, "invalid_body_md",
			"body_md is required", map[string]any{"field": "body_md"})
	}
	html, err := s.deps.Markdown.RenderString(in.Body.BodyMD)
	if err != nil {
		return nil, newAPIError(http.StatusUnprocessableEntity, "markdown_render_failed",
			"failed to render markdown", map[string]any{"field": "body_md"})
	}

	authorID := auth.FromContext(ctx).ID
	if err := s.authz.check(ctx, ownedResource("journal", authorID), actCreate); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	e := domain.JournalEntry{
		ID:         domain.NewID(),
		SpecimenID: specimenID,
		AuthorID:   authorID,
		BodyMD:     in.Body.BodyMD,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.deps.Entries.Create(ctx, nil, e); err != nil {
		return nil, mapJournalError(err)
	}
	return &createJournalOutput{
		Location: "/api/v1/journal/" + e.ID.String(),
		Body:     toJournalView(e, html),
	}, nil
}

func (s *JournalService) list(ctx context.Context, in *listJournalInput) (*listJournalOutput, error) {
	specimenID, err := parseUUID(in.SpecimenID, "id")
	if err != nil {
		return nil, err
	}
	rows, cursor, err := s.deps.Entries.ListBySpecimen(ctx, specimenID, domain.Page{Limit: in.Limit, Cursor: in.Cursor})
	if err != nil {
		return nil, mapListError(err)
	}
	items := make([]JournalView, 0, len(rows))
	for _, e := range rows {
		html, rerr := s.deps.Markdown.RenderString(e.BodyMD)
		if rerr != nil {
			return nil, newAPIError(http.StatusInternalServerError, "internal_error",
				"failed to render stored markdown", nil)
		}
		items = append(items, toJournalView(e, html))
	}
	body := journalListBody{Items: items}
	if cursor != "" {
		c := string(cursor)
		body.NextCursor = &c
	}
	return &listJournalOutput{Body: body}, nil
}

func (s *JournalService) get(ctx context.Context, in *getJournalInput) (*journalResponseOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}
	e, err := s.deps.Entries.GetByID(ctx, id)
	if err != nil {
		return nil, mapJournalError(err)
	}
	if err := s.authz.check(ctx, journalResource(e), actView); err != nil {
		return nil, err
	}
	html, rerr := s.deps.Markdown.RenderString(e.BodyMD)
	if rerr != nil {
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"failed to render stored markdown", nil)
	}
	return &journalResponseOutput{Body: toJournalView(e, html)}, nil
}

func (s *JournalService) patch(ctx context.Context, in *patchJournalInput) (*journalResponseOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}
	if in.Body.CreatedAt != nil {
		return nil, newAPIError(http.StatusBadRequest, "created_at_immutable",
			"created_at cannot be modified",
			map[string]any{"field": "created_at"})
	}
	current, err := s.deps.Entries.GetByID(ctx, id)
	if err != nil {
		return nil, mapJournalError(err)
	}
	if err := s.authz.check(ctx, journalResource(current), actEdit); err != nil {
		return nil, err
	}
	if in.Body.BodyMD != nil {
		if *in.Body.BodyMD == "" {
			return nil, newAPIError(http.StatusBadRequest, "invalid_body_md",
				"body_md must be non-empty when provided",
				map[string]any{"field": "body_md"})
		}
		current.BodyMD = *in.Body.BodyMD
	}
	current.UpdatedAt = time.Now().UTC()

	if err := s.deps.Entries.Update(ctx, nil, current); err != nil {
		return nil, mapJournalError(err)
	}
	html, rerr := s.deps.Markdown.RenderString(current.BodyMD)
	if rerr != nil {
		return nil, newAPIError(http.StatusUnprocessableEntity, "markdown_render_failed",
			"failed to render markdown", map[string]any{"field": "body_md"})
	}
	return &journalResponseOutput{Body: toJournalView(current, html)}, nil
}

func (s *JournalService) delete(ctx context.Context, in *deleteJournalInput) (*deleteJournalOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}
	current, err := s.deps.Entries.GetByID(ctx, id)
	if err != nil {
		return nil, mapJournalError(err)
	}
	if err := s.authz.check(ctx, journalResource(current), actDelete); err != nil {
		return nil, err
	}
	if err := s.deps.Entries.Delete(ctx, nil, id); err != nil {
		return nil, mapJournalError(err)
	}
	return &deleteJournalOutput{}, nil
}

func toJournalView(e domain.JournalEntry, html string) JournalView {
	return JournalView{
		ID:         e.ID,
		SpecimenID: e.SpecimenID,
		AuthorID:   e.AuthorID,
		BodyMD:     e.BodyMD,
		BodyHTML:   html,
		CreatedAt:  e.CreatedAt,
		UpdatedAt:  e.UpdatedAt,
	}
}

func mapJournalError(err error) error {
	switch {
	case errors.Is(err, domain.ErrJournalEntryNotFound):
		return newAPIError(http.StatusNotFound, "journal_entry_not_found",
			"no such journal entry", nil)
	case errors.Is(err, domain.ErrJournalEntryConflict):
		return newAPIError(http.StatusConflict, "journal_entry_referenced",
			"journal entry still has attachments; delete those first",
			map[string]any{"constraint": "journal_entry_files"})
	}
	return newAPIError(http.StatusInternalServerError, "internal_error",
		"internal server error", nil)
}
