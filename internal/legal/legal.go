// Package legal serves the operator-approved static legal documents
// — the Privacy Policy and Terms of Service (mi-97kr, a V3 launch
// prerequisite for GDPR + Quebec Law 25 compliance).
//
// The markdown under content/ is the SINGLE SOURCE OF TRUTH for the
// legal text: it is the operator/lawyer-approved copy and must only
// be changed by editing those files (a content edit, not a code
// change). The package embeds the files into the binary so the
// pages are served identically in every deployment without a
// separate asset-shipping step.
//
// Rendering to HTML happens in the API layer via the CONTRACT.md §17
// markdown pipeline (goldmark → bluemonday), the same sanitizing
// renderer used for user journal entries — so even though this text
// is trusted, it still passes the strict allowlist.
package legal

import (
	"embed"
	"fmt"
)

//go:embed content/privacy.md content/terms.md
var contentFS embed.FS

// Document is a single static legal document.
type Document struct {
	// Slug is the URL-safe identifier used both in the API path
	// (/api/v1/legal/{slug}) and the SPA route (/privacy, /terms).
	Slug string
	// Title is the human-readable name, used for the document
	// heading and the browser tab title.
	Title string
	// Markdown is the raw CommonMark source (the approved text).
	Markdown string
}

// registry is the ordered list of documents the package serves.
// Order is the display/listing order. Adding a document is a matter
// of dropping a file under content/ and adding an entry here.
var registry = []struct {
	slug, title, file string
}{
	{"privacy", "Privacy Policy", "content/privacy.md"},
	{"terms", "Terms of Service", "content/terms.md"},
}

// Documents returns every legal document in display order, with the
// embedded markdown loaded. It returns an error only if an embedded
// file is missing — which is a build-time impossibility given the
// //go:embed directive, but is surfaced rather than panicked so
// callers can fail gracefully at startup.
func Documents() ([]Document, error) {
	docs := make([]Document, 0, len(registry))
	for _, e := range registry {
		b, err := contentFS.ReadFile(e.file)
		if err != nil {
			return nil, fmt.Errorf("legal: read %s: %w", e.file, err)
		}
		docs = append(docs, Document{Slug: e.slug, Title: e.title, Markdown: string(b)})
	}
	return docs, nil
}
