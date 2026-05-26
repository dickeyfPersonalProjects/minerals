package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/dickeyfPersonalProjects/minerals/internal/legal"
	"github.com/dickeyfPersonalProjects/minerals/internal/markdown"
)

// LegalView is the wire shape of a legal document (mi-97kr). html is
// the server-rendered, sanitized HTML (CONTRACT.md §17 pipeline); the
// SPA injects it directly, exactly as it does journal body_html.
type LegalView struct {
	Slug  string `json:"slug" doc:"URL slug: \"privacy\" or \"terms\"."`
	Title string `json:"title" doc:"Human-readable document title."`
	HTML  string `json:"html" doc:"Server-rendered, sanitized HTML (CONTRACT.md §17 pipeline)."`
}

type legalOutput struct {
	Body LegalView
}

type legalInput struct {
	Slug string `path:"slug" doc:"Document slug." example:"privacy"`
}

// registerLegalOperations registers the PUBLIC legal-document
// endpoint (mi-97kr). The Privacy Policy and Terms of Service must be
// reachable BEFORE login (the registration consent links to them), so
// — like healthz/readyz/openapi — these operations carry no auth
// middleware.
//
// The markdown is rendered once here at registration time (the text
// is static and embedded) and cached as sanitized HTML, so each
// request is a map lookup. A render failure for the operator-approved
// text would be a build-time bug; it is surfaced as a 500 rather than
// silently serving empty content.
func registerLegalOperations(api huma.API) {
	renderer := markdown.NewRenderer()

	docs, err := legal.Documents()
	// rendered maps slug → the prepared view. renderErr is set when
	// any document failed to load or render; the handler then returns
	// 500 for that slug instead of serving a half-baked page.
	rendered := make(map[string]LegalView, len(docs))
	var renderErr error
	if err != nil {
		renderErr = err
	} else {
		for _, d := range docs {
			html, rerr := renderer.RenderString(d.Markdown)
			if rerr != nil {
				renderErr = rerr
				break
			}
			rendered[d.Slug] = LegalView{Slug: d.Slug, Title: d.Title, HTML: html}
		}
	}

	huma.Register(api, huma.Operation{
		OperationID: "legal-document",
		Method:      http.MethodGet,
		Path:        "/api/v1/legal/{slug}",
		Summary:     "Legal document (privacy policy / terms of service)",
		Description: "Returns the operator-approved legal document for {slug} " +
			"(\"privacy\" or \"terms\") as server-rendered, sanitized HTML " +
			"(CONTRACT.md §17 pipeline). Public — no authentication required.",
		Tags:   []string{"legal"},
		Errors: []int{http.StatusNotFound, http.StatusInternalServerError},
	}, func(_ context.Context, in *legalInput) (*legalOutput, error) {
		if renderErr != nil {
			return nil, newAPIError(http.StatusInternalServerError,
				"internal_error", "legal document unavailable", nil)
		}
		view, ok := rendered[in.Slug]
		if !ok {
			return nil, newAPIError(http.StatusNotFound,
				"legal_document_not_found", "no such legal document", nil)
		}
		return &legalOutput{Body: view}, nil
	})
}
