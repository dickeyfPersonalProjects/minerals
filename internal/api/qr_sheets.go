// QR sheet HTTP surface (mi-c78.1 / mol-c78 epic). Implements the
// six §10 endpoints that back the printable-label sheet builder:
//
//	GET    /api/v1/qr-sheet                              (active sheet for current user)
//	POST   /api/v1/qr-sheet                              (create — 409 when one already exists)
//	PATCH  /api/v1/qr-sheet                              (switch template)
//	DELETE /api/v1/qr-sheet                              (discard)
//	POST   /api/v1/qr-sheet/specimens                    (append specimen — idempotent)
//	DELETE /api/v1/qr-sheet/specimens/{specimen_id}      (remove + repack positions)
//
// The sheet is implicit in the auth context (one per user); clients
// never name a sheet id on the wire.
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/pdf"
)

// QRSheetSpecimenView is one specimen row in the GET response. The
// thumbnail URL is derived from the underlying photo id (when the
// specimen has any photos) so the frontend can render the label
// preview without a second round-trip. Specimens with no photos
// surface a nil ThumbnailURL.
type QRSheetSpecimenView struct {
	SpecimenID   uuid.UUID `json:"specimen_id" doc:"UUID of the specimen."`
	Name         string    `json:"name" doc:"Display name of the specimen."`
	Position     int       `json:"position" doc:"1-indexed position on the sheet."`
	ThumbnailURL *string   `json:"thumbnail_url" doc:"Path to the specimen's first photo thumbnail; null when the specimen has no photos."`
	AddedAt      time.Time `json:"added_at" doc:"RFC 3339 timestamp the specimen was added to the sheet."`
}

// QRSheetView is the GET response shape — sheet metadata plus the
// derived page count and the ordered specimens list.
type QRSheetView struct {
	ID        uuid.UUID             `json:"id" doc:"UUIDv7 primary key of the sheet."`
	Template  string                `json:"template" doc:"Avery-style template identifier (e.g. 'avery-5160')."`
	PageCount int                   `json:"page_count" doc:"Number of pages needed to print every specimen at this template's capacity. 0 when the sheet is empty."`
	Specimens []QRSheetSpecimenView `json:"specimens" doc:"Specimens on the sheet in position-ascending order."`
	CreatedAt time.Time             `json:"created_at" doc:"RFC 3339 creation timestamp."`
	UpdatedAt time.Time             `json:"updated_at" doc:"RFC 3339 last-update timestamp."`
}

type createQRSheetInput struct {
	Body createQRSheetBody
}

type createQRSheetBody struct {
	Template string `json:"template" doc:"Avery-style template identifier; see GET /api/v1/qr-sheet for the supported vocabulary."`
}

type patchQRSheetInput struct {
	Body patchQRSheetBody
}

type patchQRSheetBody struct {
	Template string `json:"template" doc:"New template identifier."`
}

type qrSheetOutput struct {
	Body QRSheetView
}

type createQRSheetOutput struct {
	Location string `header:"Location" doc:"URL of the newly created sheet."`
	Body     QRSheetView
}

type deleteQRSheetOutput struct{}

type addQRSheetSpecimenInput struct {
	Body addQRSheetSpecimenBody
}

type addQRSheetSpecimenBody struct {
	SpecimenID string `json:"specimen_id" doc:"UUID of the specimen to append to the sheet."`
}

type addQRSheetSpecimenOutput struct {
	Body QRSheetView
}

type removeQRSheetSpecimenInput struct {
	SpecimenID string `path:"specimen_id" doc:"UUID of the specimen to remove from the sheet."`
}

type removeQRSheetSpecimenOutput struct{}

// generateQRSheetPDFOutput streams an application/pdf binary body
// back to the client. The body callback is the documented huma
// escape hatch for non-JSON responses (used in this package by the
// /docs and /healthz handlers); huma will still emit an OpenAPI
// operation for the endpoint via the registered Operation metadata.
type generateQRSheetPDFOutput struct {
	Body func(huma.Context)
}

// QRSheetService wires huma operations against a domain.QRSheetRepo
// and the specimen repo (needed to surface ErrSpecimenNotFound up
// front for the add path).
type QRSheetService struct {
	repo  domain.QRSheetRepo
	authz authzGuard
}

func registerQRSheetOperations(api huma.API, authMW authMiddlewares, guard authzGuard, repo domain.QRSheetRepo) {
	if repo == nil {
		return
	}
	s := &QRSheetService{repo: repo, authz: guard}
	mws := authMW.Protected()

	huma.Register(api, huma.Operation{
		OperationID: "get-qr-sheet",
		Method:      http.MethodGet,
		Path:        "/api/v1/qr-sheet",
		Summary:     "Get the current user's QR sheet",
		Description: "Returns the active QR sticker sheet for the authenticated user, including the ordered list of specimens and the calculated page count for the chosen template. 404 when the user has no active sheet.",
		Tags:        []string{"qr-sheets"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
		Middlewares: mws,
	}, s.get)

	huma.Register(api, huma.Operation{
		OperationID:   "create-qr-sheet",
		Method:        http.MethodPost,
		Path:          "/api/v1/qr-sheet",
		Summary:       "Create the current user's QR sheet",
		Description:   "Creates an empty sheet with the supplied template. Returns 409 when the user already has an active sheet — use PATCH to change template, DELETE + POST to reset.",
		Tags:          []string{"qr-sheets"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusConflict},
		Middlewares:   mws,
	}, s.create)

	huma.Register(api, huma.Operation{
		OperationID: "patch-qr-sheet",
		Method:      http.MethodPatch,
		Path:        "/api/v1/qr-sheet",
		Summary:     "Update the current user's QR sheet template",
		Description: "Switches the template on the active sheet. Specimen membership is preserved; only the page-count calculation changes. Returns 404 when the user has no active sheet.",
		Tags:        []string{"qr-sheets"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Middlewares: mws,
	}, s.patch)

	huma.Register(api, huma.Operation{
		OperationID:   "delete-qr-sheet",
		Method:        http.MethodDelete,
		Path:          "/api/v1/qr-sheet",
		Summary:       "Delete the current user's QR sheet",
		Description:   "Discards the active sheet and every specimen on it. Returns 404 when the user has no active sheet.",
		Tags:          []string{"qr-sheets"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusNotFound},
		Middlewares:   mws,
	}, s.delete)

	huma.Register(api, huma.Operation{
		OperationID: "add-qr-sheet-specimen",
		Method:      http.MethodPost,
		Path:        "/api/v1/qr-sheet/specimens",
		Summary:     "Append a specimen to the current user's QR sheet",
		Description: "Appends the supplied specimen at the next free position. Idempotent — re-adding a specimen already on the sheet succeeds without changing its position. Returns 404 when the user has no sheet, or when specimen_id doesn't exist.",
		Tags:        []string{"qr-sheets"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Middlewares: mws,
	}, s.addSpecimen)

	huma.Register(api, huma.Operation{
		OperationID:   "remove-qr-sheet-specimen",
		Method:        http.MethodDelete,
		Path:          "/api/v1/qr-sheet/specimens/{specimen_id}",
		Summary:       "Remove a specimen from the current user's QR sheet",
		Description:   "Removes the specimen and repacks remaining positions so they stay contiguous. Returns 404 when the user has no sheet, or when the specimen isn't on it.",
		Tags:          []string{"qr-sheets"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Middlewares:   mws,
	}, s.removeSpecimen)

	huma.Register(api, huma.Operation{
		OperationID: "generate-qr-sheet-pdf",
		Method:      http.MethodPost,
		Path:        "/api/v1/qr-sheet/pdf",
		Summary:     "Generate a print-ready PDF of the current user's QR sheet",
		Description: "Renders the user's active sheet as a multi-page PDF sized for the chosen Avery template. Each specimen becomes one QR-coded label whose payload is the specimen's absolute URL on this server. Response is `application/pdf` with `Content-Disposition: attachment; filename=\"qr-sheet.pdf\"`. Returns 404 when the user has no sheet and 400 when the sheet is empty.",
		Tags:        []string{"qr-sheets"},
		Errors: []int{
			http.StatusBadRequest,
			http.StatusUnauthorized,
			http.StatusNotFound,
			http.StatusInternalServerError,
		},
		Middlewares: append(huma.Middlewares{qrSheetBaseURLMiddleware}, mws...),
	}, s.generatePDF)
}

func (s *QRSheetService) get(ctx context.Context, _ *struct{}) (*qrSheetOutput, error) {
	user := auth.FromContext(ctx)
	if err := s.authz.check(ctx, ownedResource("qr-sheets", user.ID), actView); err != nil {
		return nil, err
	}
	view, err := s.loadView(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return &qrSheetOutput{Body: view}, nil
}

func (s *QRSheetService) create(
	ctx context.Context, in *createQRSheetInput,
) (*createQRSheetOutput, error) {
	template, err := parseQRTemplate(in.Body.Template)
	if err != nil {
		return nil, err
	}
	user := auth.FromContext(ctx)
	if err := s.authz.check(ctx, ownedResource("qr-sheets", user.ID), actCreate); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	sheet := domain.QRSheet{
		ID:        domain.NewID(),
		UserID:    user.ID,
		Template:  template,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.Create(ctx, nil, sheet); err != nil {
		return nil, mapQRSheetError(err)
	}
	view := toQRSheetView(sheet, nil)
	return &createQRSheetOutput{
		Location: "/api/v1/qr-sheet",
		Body:     view,
	}, nil
}

func (s *QRSheetService) patch(
	ctx context.Context, in *patchQRSheetInput,
) (*qrSheetOutput, error) {
	template, err := parseQRTemplate(in.Body.Template)
	if err != nil {
		return nil, err
	}
	user := auth.FromContext(ctx)
	if err := s.authz.check(ctx, ownedResource("qr-sheets", user.ID), actEdit); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if err := s.repo.UpdateTemplate(ctx, nil, user.ID, template, now); err != nil {
		return nil, mapQRSheetError(err)
	}
	view, err := s.loadView(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return &qrSheetOutput{Body: view}, nil
}

func (s *QRSheetService) delete(
	ctx context.Context, _ *struct{},
) (*deleteQRSheetOutput, error) {
	user := auth.FromContext(ctx)
	if err := s.authz.check(ctx, ownedResource("qr-sheets", user.ID), actDelete); err != nil {
		return nil, err
	}
	if err := s.repo.Delete(ctx, nil, user.ID); err != nil {
		return nil, mapQRSheetError(err)
	}
	return &deleteQRSheetOutput{}, nil
}

func (s *QRSheetService) addSpecimen(
	ctx context.Context, in *addQRSheetSpecimenInput,
) (*addQRSheetSpecimenOutput, error) {
	specimenID, err := parseUUID(in.Body.SpecimenID, "specimen_id")
	if err != nil {
		return nil, err
	}
	user := auth.FromContext(ctx)
	if err := s.authz.check(ctx, ownedResource("qr-sheets", user.ID), actEdit); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if err := s.repo.AddSpecimen(ctx, nil, user.ID, specimenID, now); err != nil {
		return nil, mapQRSheetError(err)
	}
	view, err := s.loadView(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return &addQRSheetSpecimenOutput{Body: view}, nil
}

func (s *QRSheetService) removeSpecimen(
	ctx context.Context, in *removeQRSheetSpecimenInput,
) (*removeQRSheetSpecimenOutput, error) {
	specimenID, err := parseUUID(in.SpecimenID, "specimen_id")
	if err != nil {
		return nil, err
	}
	user := auth.FromContext(ctx)
	if err := s.authz.check(ctx, ownedResource("qr-sheets", user.ID), actEdit); err != nil {
		return nil, err
	}
	if err := s.repo.RemoveSpecimen(ctx, nil, user.ID, specimenID); err != nil {
		return nil, mapQRSheetError(err)
	}
	return &removeQRSheetSpecimenOutput{}, nil
}

// generatePDF builds a PDF of the user's active sheet and streams
// it back as application/pdf. 404 when no sheet, 400 when the sheet
// has no specimens. The QR payload for each label is the specimen's
// absolute URL on this server (scheme + host derived from the
// request via qrSheetBaseURLMiddleware).
func (s *QRSheetService) generatePDF(
	ctx context.Context, _ *struct{},
) (*generateQRSheetPDFOutput, error) {
	user := auth.FromContext(ctx)
	if err := s.authz.check(ctx, ownedResource("qr-sheets", user.ID), actView); err != nil {
		return nil, err
	}
	sheet, err := s.repo.GetByUser(ctx, user.ID)
	if err != nil {
		return nil, mapQRSheetError(err)
	}
	entries, err := s.repo.ListSpecimens(ctx, sheet.ID)
	if err != nil {
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"internal server error", nil)
	}
	if len(entries) == 0 {
		return nil, newAPIError(http.StatusBadRequest, "qr_sheet_empty",
			"qr sheet has no specimens; add specimens before generating a pdf",
			map[string]any{"constraint": "non_empty_sheet"})
	}

	template, ok := pdf.TemplateByName(string(sheet.Template))
	if !ok {
		// Stored template is not in the v1 vocabulary — the API layer
		// validates on write, so this can only fire if the DB has been
		// hand-edited or a template was removed from the code. 500
		// signals an internal mismatch rather than a client error.
		return nil, newAPIError(http.StatusInternalServerError, "qr_template_unknown",
			"qr sheet template is not renderable", nil)
	}

	base := baseURLFromContext(ctx)
	urls := make([]string, 0, len(entries))
	for _, e := range entries {
		urls = append(urls, base+"/specimens/"+e.SpecimenID.String())
	}

	body, err := pdf.Generate(template, urls)
	if err != nil {
		return nil, newAPIError(http.StatusInternalServerError, "pdf_render_failed",
			"failed to render qr sheet pdf", nil)
	}

	return &generateQRSheetPDFOutput{
		Body: func(c huma.Context) {
			c.SetHeader("Content-Type", "application/pdf")
			c.SetHeader("Content-Disposition", `attachment; filename="qr-sheet.pdf"`)
			c.SetHeader("Content-Length", fmt.Sprintf("%d", len(body)))
			c.SetHeader("Cache-Control", "private, no-store")
			_, _ = c.BodyWriter().Write(body)
		},
	}, nil
}

// baseURLCtxKey is the context-value key used by
// qrSheetBaseURLMiddleware to thread the request's scheme+host to
// handlers. Defining a private type prevents accidental collision
// with other packages' context values (Go std-lib convention).
type baseURLCtxKey struct{}

// qrSheetBaseURLMiddleware extracts the scheme+host pair from the
// incoming request and stashes it in the handler's ctx so the QR
// payloads embed an absolute URL pointing back at this server. v1
// runs single-host so Host (or the proxy-set X-Forwarded-* pair) is
// authoritative; later, when public sharing ships, this can be
// replaced with a configured PUBLIC_BASE_URL.
func qrSheetBaseURLMiddleware(ctx huma.Context, next func(huma.Context)) {
	r, _ := humago.Unwrap(ctx)
	next(huma.WithContext(ctx, context.WithValue(
		ctx.Context(), baseURLCtxKey{}, requestBaseURL(r))))
}

func baseURLFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(baseURLCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// requestBaseURL builds "scheme://host" from r. Honours
// X-Forwarded-Proto / X-Forwarded-Host when present so deployments
// fronted by a TLS-terminating proxy still emit https links. Falls
// back to r.TLS for direct exposure.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host
}

// loadView reads the user's sheet + its specimens and renders the
// wire view (with derived page count and per-specimen thumbnail URL).
// Returns mapped errors so handler funcs can pass them through unchanged.
func (s *QRSheetService) loadView(
	ctx context.Context, userID uuid.UUID,
) (QRSheetView, error) {
	sheet, err := s.repo.GetByUser(ctx, userID)
	if err != nil {
		return QRSheetView{}, mapQRSheetError(err)
	}
	entries, err := s.repo.ListSpecimens(ctx, sheet.ID)
	if err != nil {
		return QRSheetView{}, newAPIError(http.StatusInternalServerError, "internal_error",
			"internal server error", nil)
	}
	return toQRSheetView(sheet, entries), nil
}

func toQRSheetView(s domain.QRSheet, entries []domain.QRSheetEntry) QRSheetView {
	specimens := make([]QRSheetSpecimenView, 0, len(entries))
	for _, e := range entries {
		v := QRSheetSpecimenView{
			SpecimenID: e.SpecimenID,
			Name:       e.SpecimenName,
			Position:   e.Position,
			AddedAt:    e.AddedAt,
		}
		if e.FirstPhotoID != nil {
			url := "/api/v1/photos/" + e.FirstPhotoID.String() + "/thumb"
			v.ThumbnailURL = &url
		}
		specimens = append(specimens, v)
	}
	return QRSheetView{
		ID:        s.ID,
		Template:  string(s.Template),
		PageCount: qrSheetPageCount(s.Template, len(entries)),
		Specimens: specimens,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

// qrSheetPageCount = ceil(count / capacity); 0 when the sheet is empty.
// Unknown templates (which the API rejects at write time) fall back
// to one page per specimen so the GET handler never panics on stale
// or future data.
func qrSheetPageCount(t domain.QRSheetTemplate, count int) int {
	if count == 0 {
		return 0
	}
	capacity, ok := domain.QRSheetTemplateCapacity(t)
	if !ok || capacity <= 0 {
		return count
	}
	return (count + capacity - 1) / capacity
}

// parseQRTemplate validates the template against the v1 vocabulary
// (per the mi-c78 epic spec).
func parseQRTemplate(raw string) (domain.QRSheetTemplate, error) {
	t := domain.QRSheetTemplate(raw)
	if _, ok := domain.QRSheetTemplateCapacity(t); !ok {
		return "", newAPIError(http.StatusBadRequest, "invalid_template",
			"template is not a supported avery template",
			map[string]any{"field": "template", "value": raw})
	}
	return t, nil
}

func mapQRSheetError(err error) error {
	switch {
	case errors.Is(err, domain.ErrQRSheetNotFound):
		return newAPIError(http.StatusNotFound, "qr_sheet_not_found",
			"no active qr sheet for the current user", nil)
	case errors.Is(err, domain.ErrQRSheetConflict):
		return newAPIError(http.StatusConflict, "qr_sheet_conflict",
			"an active qr sheet already exists for the current user; PATCH to switch template or DELETE first to reset",
			map[string]any{"constraint": "one_sheet_per_user"})
	case errors.Is(err, domain.ErrQRSheetSpecimenNotFound):
		return newAPIError(http.StatusNotFound, "qr_sheet_specimen_not_found",
			"specimen is not on the current user's qr sheet", nil)
	case errors.Is(err, domain.ErrSpecimenNotFound):
		return newAPIError(http.StatusNotFound, "specimen_not_found",
			"no such specimen", nil)
	}
	return newAPIError(http.StatusInternalServerError, "internal_error",
		"internal server error", nil)
}
