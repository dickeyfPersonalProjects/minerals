// Package markdown is the server-side markdown rendering pipeline
// (CONTRACT.md §17). User-supplied markdown — journal entry body_md
// today, specimen.description in a follow-up — is rendered to HTML
// here before reaching the SPA: goldmark parses CommonMark to HTML,
// then bluemonday sanitizes against the §17 strict allowlist.
//
// Never trust the goldmark output as already-safe; the bluemonday
// step is mandatory. The renderer is safe for concurrent use.
package markdown

import (
	"bytes"
	"sync"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// Renderer turns user-supplied markdown into sanitized HTML.
type Renderer struct {
	md     goldmark.Markdown
	policy *bluemonday.Policy
	pool   sync.Pool
}

// NewRenderer returns a Renderer wired to the §17 pipeline.
func NewRenderer() *Renderer {
	r := &Renderer{
		md: goldmark.New(
			goldmark.WithExtensions(
				extension.Strikethrough,
				extension.Table,
			),
		),
		policy: newPolicy(),
	}
	r.pool.New = func() any { return new(bytes.Buffer) }
	return r
}

// Render renders src markdown to sanitized HTML bytes. The result is
// safe to write directly into an HTML document body via SPA
// innerHTML; callers MUST NOT additionally HTML-escape.
func (r *Renderer) Render(src []byte) ([]byte, error) {
	buf := r.pool.Get().(*bytes.Buffer)
	buf.Reset()
	defer r.pool.Put(buf)
	if err := r.md.Convert(src, buf); err != nil {
		return nil, err
	}
	return r.policy.SanitizeBytes(buf.Bytes()), nil
}

// RenderString is the convenience string wrapper.
func (r *Renderer) RenderString(src string) (string, error) {
	out, err := r.Render([]byte(src))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// newPolicy builds the §17 strict allowlist policy. The shape is
// intentionally narrow: anything not in the list is dropped or
// attribute-stripped. Updates to this policy require a §17 review.
func newPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	// Block-level + inline allowlist (§17).
	p.AllowElements(
		"p", "br",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"strong", "em", "del",
		"code", "pre",
		"ul", "ol", "li",
		"blockquote",
		"hr",
		"table", "thead", "tbody", "tr", "th", "td",
	)

	// Anchors. AllowURLSchemes restricts href to the §17 schemes;
	// relative URLs stay disabled (default), so anything that survives
	// counts as "fully qualified" and the *FullyQualifiedLinks
	// helpers cover every surviving link. AddTargetBlank causes
	// bluemonday to add rel="noopener" automatically; the noreferrer
	// helper appends to that rel attribute.
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("http", "https", "mailto")
	p.AddTargetBlankToFullyQualifiedLinks(true)
	p.RequireNoReferrerOnFullyQualifiedLinks(true)

	return p
}
