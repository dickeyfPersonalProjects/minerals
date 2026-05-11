// Package pdf renders print-ready QR-sticker sheets for the mi-c78
// epic. The PDF layout is template-driven: callers pass a Template
// (page size + label geometry) and a slice of specimen URLs; the
// package encodes each URL as a QR PNG and tiles them across the
// page at the exact coordinates the named Avery sheet expects.
//
// Library choices (CONTRACT.md §16):
//   - github.com/skip2/go-qrcode (MIT, pure-Go) — QR encoding to PNG
//   - github.com/go-pdf/fpdf    (MIT, pure-Go) — PDF generation
//
// Both are MIT-licensed and pure-Go, so they pass the §16 license
// allowlist and the CGO_ENABLED=0 build constraint.
package pdf

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/go-pdf/fpdf"
	"github.com/skip2/go-qrcode"
)

// inchMM converts inches to millimetres. The PDF is laid out in mm
// so Letter (Avery 5xxx) and A4 (Avery L7160) share one unit system.
const inchMM = 25.4

func inToMM(in float64) float64 { return in * inchMM }

// labelInsetFraction is the per-side margin around the QR inside its
// cell, expressed as a fraction of cell width/height. 5% per the
// mi-c78.2 spec keeps the code clear of the sticker's edge die-cut
// while leaving most of the cell for the QR pattern.
const labelInsetFraction = 0.05

// qrPNGSize is the pixel size requested from the QR encoder. The
// final placement scales the image to fit the cell; we ask for a
// generous source so downscaling stays crisp at common print DPIs.
const qrPNGSize = 512

// ErrNoLabels is returned by Generate when the urls slice is empty.
// Maps to 400 at the API boundary (per mi-c78.2: empty sheets reject).
var ErrNoLabels = errors.New("qr sheet has no labels to render")

// Template captures the geometry of a single Avery template. All
// distances are in millimetres; PageSize is the fpdf page-format
// name ("Letter" or "A4"). Capacity = Cols * Rows.
type Template struct {
	Name       string
	PageSize   string
	Cols       int
	Rows       int
	LabelW     float64
	LabelH     float64
	MarginTop  float64
	MarginLeft float64
	GapH       float64
	GapV       float64
}

// Capacity returns the number of labels that fit on one page of the
// template (Cols * Rows).
func (t Template) Capacity() int { return t.Cols * t.Rows }

// templates is the v1 vocabulary (mi-c78 epic spec). The numeric
// values match the bead table exactly; do not edit without bumping
// the mi-c78.2 acceptance test that pins label alignment.
var templates = map[string]Template{
	"avery-5160": {
		Name: "avery-5160", PageSize: "Letter",
		Cols: 3, Rows: 10,
		LabelW: inToMM(2.625), LabelH: inToMM(1.0),
		MarginTop: inToMM(0.5), MarginLeft: inToMM(0.19),
		GapH: inToMM(0.125), GapV: 0,
	},
	"avery-5163": {
		Name: "avery-5163", PageSize: "Letter",
		Cols: 2, Rows: 5,
		LabelW: inToMM(4.0), LabelH: inToMM(2.0),
		MarginTop: inToMM(0.5), MarginLeft: inToMM(0.15),
		GapH: inToMM(0.19), GapV: 0,
	},
	"avery-5164": {
		Name: "avery-5164", PageSize: "Letter",
		Cols: 2, Rows: 3,
		LabelW: inToMM(4.0), LabelH: inToMM(3.33),
		MarginTop: inToMM(0.5), MarginLeft: inToMM(0.15),
		GapH: inToMM(0.19), GapV: 0,
	},
	"avery-22806": {
		Name: "avery-22806", PageSize: "Letter",
		Cols: 3, Rows: 4,
		LabelW: inToMM(2.0), LabelH: inToMM(2.0),
		MarginTop: inToMM(0.5), MarginLeft: inToMM(0.75),
		GapH: inToMM(0.31), GapV: inToMM(0.25),
	},
	"avery-l7160": {
		Name: "avery-l7160", PageSize: "A4",
		Cols: 3, Rows: 7,
		LabelW: 63.5, LabelH: 38.1,
		MarginTop: 15.15, MarginLeft: 4.65,
		GapH: 2.54, GapV: 0,
	},
}

// TemplateByName resolves a wire-format template id (e.g.
// "avery-5160") to its geometry. Unknown templates return false.
func TemplateByName(name string) (Template, bool) {
	t, ok := templates[name]
	return t, ok
}

// Generate renders the supplied URLs onto a multi-page PDF sized
// for the template. Labels are placed in row-major order starting
// at the top-left cell; the PDF auto-paginates when the count
// exceeds Capacity(). Returns ErrNoLabels when urls is empty.
func Generate(template Template, urls []string) ([]byte, error) {
	if len(urls) == 0 {
		return nil, ErrNoLabels
	}
	doc := fpdf.New("P", "mm", template.PageSize, "")
	doc.SetMargins(0, 0, 0)
	doc.SetAutoPageBreak(false, 0)

	perPage := template.Capacity()
	for i, url := range urls {
		if i%perPage == 0 {
			doc.AddPage()
		}
		slot := i % perPage
		row := slot / template.Cols
		col := slot % template.Cols

		cellX := template.MarginLeft + float64(col)*(template.LabelW+template.GapH)
		cellY := template.MarginTop + float64(row)*(template.LabelH+template.GapV)

		png, err := qrcode.Encode(url, qrcode.Medium, qrPNGSize)
		if err != nil {
			return nil, fmt.Errorf("encode qr %q: %w", url, err)
		}

		// QR codes are square; fit them in the largest square that
		// honours the 5% inset on both axes, then centre within the
		// cell so the remaining slack is distributed evenly.
		boxW := template.LabelW * (1 - 2*labelInsetFraction)
		boxH := template.LabelH * (1 - 2*labelInsetFraction)
		qrSize := boxW
		if boxH < qrSize {
			qrSize = boxH
		}
		qrX := cellX + (template.LabelW-qrSize)/2
		qrY := cellY + (template.LabelH-qrSize)/2

		imgName := fmt.Sprintf("qr-%d", i)
		doc.RegisterImageOptionsReader(imgName,
			fpdf.ImageOptions{ImageType: "png"}, bytes.NewReader(png))
		if err := doc.Error(); err != nil {
			return nil, fmt.Errorf("register qr image %d: %w", i, err)
		}
		doc.ImageOptions(imgName, qrX, qrY, qrSize, qrSize, false,
			fpdf.ImageOptions{ImageType: "png"}, 0, "")
	}

	var buf bytes.Buffer
	if err := doc.Output(&buf); err != nil {
		return nil, fmt.Errorf("write pdf: %w", err)
	}
	return buf.Bytes(), nil
}
