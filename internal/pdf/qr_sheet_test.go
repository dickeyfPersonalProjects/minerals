package pdf

import (
	"bytes"
	"errors"
	"testing"
)

func TestTemplateByName_KnownTemplates(t *testing.T) {
	cases := []struct {
		name     string
		pageSize string
		cap      int
	}{
		{"avery-5160", "Letter", 30},
		{"avery-5163", "Letter", 10},
		{"avery-5164", "Letter", 6},
		{"avery-22806", "Letter", 12},
		{"avery-l7160", "A4", 21},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := TemplateByName(tc.name)
			if !ok {
				t.Fatalf("template %q not found", tc.name)
			}
			if got.PageSize != tc.pageSize {
				t.Errorf("page size = %q want %q", got.PageSize, tc.pageSize)
			}
			if got.Capacity() != tc.cap {
				t.Errorf("capacity = %d want %d", got.Capacity(), tc.cap)
			}
		})
	}
}

func TestTemplateByName_UnknownReturnsFalse(t *testing.T) {
	if _, ok := TemplateByName("avery-totally-bogus"); ok {
		t.Fatal("expected unknown template to return false")
	}
}

// TestTemplate_LabelsFitInsidePage guards the geometry math against
// off-by-one regressions: for every template, the bottom-right of the
// last cell must land within the printable page area. A failure here
// usually means a wrong inch/mm conversion or a swapped width/height.
func TestTemplate_LabelsFitInsidePage(t *testing.T) {
	pageDimsMM := map[string]struct{ w, h float64 }{
		"Letter": {215.9, 279.4},
		"A4":     {210.0, 297.0},
	}
	const tol = 0.5 // mm, per mi-c78.2 acceptance criterion
	for name, tmpl := range templates {
		t.Run(name, func(t *testing.T) {
			page, ok := pageDimsMM[tmpl.PageSize]
			if !ok {
				t.Fatalf("unknown page size %q", tmpl.PageSize)
			}
			rightEdge := tmpl.MarginLeft +
				float64(tmpl.Cols)*tmpl.LabelW +
				float64(tmpl.Cols-1)*tmpl.GapH
			bottomEdge := tmpl.MarginTop +
				float64(tmpl.Rows)*tmpl.LabelH +
				float64(tmpl.Rows-1)*tmpl.GapV
			if rightEdge > page.w+tol {
				t.Errorf("right edge %.2fmm exceeds page width %.2fmm", rightEdge, page.w)
			}
			if bottomEdge > page.h+tol {
				t.Errorf("bottom edge %.2fmm exceeds page height %.2fmm", bottomEdge, page.h)
			}
		})
	}
}

func TestGenerate_EmptyURLs_ReturnsErrNoLabels(t *testing.T) {
	tmpl, _ := TemplateByName("avery-5160")
	_, err := Generate(tmpl, nil)
	if !errors.Is(err, ErrNoLabels) {
		t.Fatalf("err = %v, want ErrNoLabels", err)
	}
}

// TestGenerate_AllTemplates_ProducePDFBytes is the breadth test: every
// supported template renders cleanly with a representative URL set
// (one full page plus one overflow). The output must start with the
// PDF magic bytes; that's the cheapest sanity check that fpdf wrote
// a structurally valid file.
func TestGenerate_AllTemplates_ProducePDFBytes(t *testing.T) {
	for name := range templates {
		t.Run(name, func(t *testing.T) {
			tmpl, _ := TemplateByName(name)
			// One full page + 1 overflow specimen → exercises the
			// AddPage branch for every template.
			urls := make([]string, tmpl.Capacity()+1)
			for i := range urls {
				urls[i] = "https://example.test/specimens/abc-" + name
			}
			out, err := Generate(tmpl, urls)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if !bytes.HasPrefix(out, []byte("%PDF-")) {
				t.Fatalf("output does not start with PDF magic; got %q", string(out[:8]))
			}
			if len(out) < 1000 {
				t.Errorf("PDF suspiciously small: %d bytes", len(out))
			}
		})
	}
}

// TestGenerate_PaginatesAtCapacityBoundary verifies that the
// auto-pagination path agrees with the page-count formula the API
// surfaces via QRSheetTemplateCapacity. We render exactly capacity+1
// labels and re-parse the result for the page-count token.
func TestGenerate_PaginatesAtCapacityBoundary(t *testing.T) {
	tmpl, _ := TemplateByName("avery-5164") // capacity 6 — small and fast
	urls := make([]string, tmpl.Capacity()+1)
	for i := range urls {
		urls[i] = "https://example.test/specimens/x"
	}
	out, err := Generate(tmpl, urls)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// fpdf writes the page object count into the PDF trailer; the
	// cheapest cross-check is to count the "/Type /Page\n" occurrences,
	// which fpdf emits once per page object (not per /Pages root).
	pages := bytes.Count(out, []byte("/Type /Page\n"))
	if pages != 2 {
		t.Errorf("page count = %d, want 2 (capacity %d + 1 overflow)", pages, tmpl.Capacity())
	}
}
