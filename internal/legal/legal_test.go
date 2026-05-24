package legal

import (
	"strings"
	"testing"
)

func TestDocumentsReturnsBothDocs(t *testing.T) {
	docs, err := Documents()
	if err != nil {
		t.Fatalf("Documents() error = %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("Documents() len = %d, want 2", len(docs))
	}

	wantSlugs := map[string]string{
		"privacy": "Privacy Policy",
		"terms":   "Terms of Service",
	}
	for _, d := range docs {
		title, ok := wantSlugs[d.Slug]
		if !ok {
			t.Errorf("unexpected slug %q", d.Slug)
			continue
		}
		if d.Title != title {
			t.Errorf("slug %q title = %q, want %q", d.Slug, d.Title, title)
		}
		if strings.TrimSpace(d.Markdown) == "" {
			t.Errorf("slug %q has empty markdown", d.Slug)
		}
	}
}

// The approved text is operator-reviewed; guard against an empty or
// truncated embed by asserting on stable, content-bearing markers.
func TestDocumentsContentMarkers(t *testing.T) {
	docs, err := Documents()
	if err != nil {
		t.Fatalf("Documents() error = %v", err)
	}
	byslug := map[string]Document{}
	for _, d := range docs {
		byslug[d.Slug] = d
	}

	if !strings.Contains(byslug["privacy"].Markdown, "# Privacy Policy") {
		t.Error("privacy markdown missing '# Privacy Policy' heading")
	}
	if !strings.Contains(byslug["terms"].Markdown, "# Terms of Service") {
		t.Error("terms markdown missing '# Terms of Service' heading")
	}
}
