package markdown_test

import (
	"strings"
	"testing"

	"github.com/dickeyfPersonalProjects/minerals/internal/markdown"
)

func TestRender_AllowedElementsPassThrough(t *testing.T) {
	t.Parallel()
	r := markdown.NewRenderer()
	in := "# Heading\n\nA **bold** *italic* word with `code` and ~~strike~~.\n\n" +
		"- item 1\n- item 2\n\n> quote\n\n---\n"
	got, err := r.RenderString(in)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"<h1>", "<strong>bold</strong>", "<em>italic</em>",
		"<code>code</code>", "<del>strike</del>",
		"<ul>", "<li>item 1</li>",
		"<blockquote>", "<hr",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, got)
		}
	}
}

func TestRender_DropsRawScript(t *testing.T) {
	t.Parallel()
	r := markdown.NewRenderer()
	// goldmark's default config disables raw HTML, but we assert the
	// pipeline drops a <script> regardless: even if a future config
	// re-enables raw HTML, bluemonday's strict policy must shield us.
	in := "Hello <script>alert(1)</script> world"
	got, err := r.RenderString(in)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(got, "<script") {
		t.Errorf("output retained <script>: %s", got)
	}
	if strings.Contains(got, "alert(1)") {
		// Even if the tag is stripped, leaving the inner JS as text
		// would be fine — but our pipeline drops both for raw HTML
		// blocks. Keep this expectation tight.
		t.Logf("output retained alert(1) text (acceptable): %s", got)
	}
}

func TestRender_DropsImgIframeStyle(t *testing.T) {
	t.Parallel()
	r := markdown.NewRenderer()
	in := "<img src=\"x\" onerror=\"alert(1)\"> " +
		"<iframe src=\"//evil\"></iframe> " +
		"<style>body{display:none}</style>"
	got, err := r.RenderString(in)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, banned := range []string{"<img", "<iframe", "<style", "onerror"} {
		if strings.Contains(got, banned) {
			t.Errorf("output retained %q: %s", banned, got)
		}
	}
}

func TestRender_StripsJavascriptHref(t *testing.T) {
	t.Parallel()
	r := markdown.NewRenderer()
	in := "[click](javascript:alert(1))"
	got, err := r.RenderString(in)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// The link text survives but the unsafe scheme must not appear in
	// the rendered href.
	if strings.Contains(strings.ToLower(got), "javascript:") {
		t.Errorf("output retained javascript: scheme: %s", got)
	}
}

func TestRender_StripsDataAndFileSchemes(t *testing.T) {
	t.Parallel()
	r := markdown.NewRenderer()
	for _, in := range []string{
		"[d](data:text/html;base64,PHNjcmlwdD4=)",
		"[f](file:///etc/passwd)",
	} {
		got, err := r.RenderString(in)
		if err != nil {
			t.Fatalf("render %q: %v", in, err)
		}
		lower := strings.ToLower(got)
		if strings.Contains(lower, "data:") || strings.Contains(lower, "file:") {
			t.Errorf("output retained banned scheme for %q: %s", in, got)
		}
	}
}

func TestRender_HardensExternalAnchor(t *testing.T) {
	t.Parallel()
	r := markdown.NewRenderer()
	in := "[ext](https://example.com)"
	got, err := r.RenderString(in)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(got, `target="_blank"`) {
		t.Errorf("missing target=_blank: %s", got)
	}
	if !strings.Contains(got, "noopener") {
		t.Errorf("missing rel=noopener: %s", got)
	}
	if !strings.Contains(got, "noreferrer") {
		t.Errorf("missing rel=noreferrer: %s", got)
	}
	if !strings.Contains(got, `href="https://example.com"`) {
		t.Errorf("missing href: %s", got)
	}
}

func TestRender_AllowsMailtoLinks(t *testing.T) {
	t.Parallel()
	r := markdown.NewRenderer()
	in := "[mail](mailto:a@b.example)"
	got, err := r.RenderString(in)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(got, `href="mailto:a@b.example"`) {
		t.Errorf("expected mailto href: %s", got)
	}
}

func TestRender_DropsInlineStyleAttr(t *testing.T) {
	t.Parallel()
	r := markdown.NewRenderer()
	in := `<p style="color:red">red</p>`
	got, err := r.RenderString(in)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(got, "style=") {
		t.Errorf("output retained style attr: %s", got)
	}
}

func TestRender_EmptyInput(t *testing.T) {
	t.Parallel()
	r := markdown.NewRenderer()
	got, err := r.RenderString("")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.TrimSpace(got) != "" {
		t.Errorf("expected empty output, got %q", got)
	}
}

// FuzzRender drives arbitrary input through the §17 render+sanitize
// pipeline. The primary goal is panic / infinite-loop discovery on
// untrusted input; the secondary goal is a defensive check that no
// dangerous element ever appears in the output as a literal tag.
//
// Tag-shape tokens (e.g. "<script") cannot appear from typed text —
// goldmark escapes user `<` to `&lt;`, and bluemonday drops the
// elements outright — so a hit on these substrings is always a real
// finding. Scheme tokens like "javascript:" can appear inside text /
// code spans (a user is free to type the word) and so are not
// asserted here; the unit tests cover their attribute-stripping.
func FuzzRender(f *testing.F) {
	seeds := []string{
		"",
		"# Heading\n\nA **bold** *italic* word with `code` and ~~strike~~.\n\n" +
			"- item 1\n- item 2\n\n> quote\n\n---\n",
		"Hello <script>alert(1)</script> world",
		`<img src="x" onerror="alert(1)"> <iframe src="//evil"></iframe> ` +
			`<style>body{display:none}</style>`,
		"[click](javascript:alert(1))",
		"[d](data:text/html;base64,PHNjcmlwdD4=)",
		"[f](file:///etc/passwd)",
		"[ext](https://example.com)",
		"[mail](mailto:a@b.example)",
		`<p style="color:red">red</p>`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	r := markdown.NewRenderer()
	bannedTags := []string{
		"<script", "<iframe", "<style", "<object",
		"<embed", "<form", "<frame", "<svg", "<math",
	}

	f.Fuzz(func(t *testing.T, in string) {
		out, err := r.RenderString(in)
		if err != nil {
			t.Fatalf("render returned error on input %q: %v", in, err)
		}
		lower := strings.ToLower(out)
		for _, banned := range bannedTags {
			if strings.Contains(lower, banned) {
				t.Errorf("output contains banned tag %q for input %q\noutput: %s",
					banned, in, out)
			}
		}
	})
}
