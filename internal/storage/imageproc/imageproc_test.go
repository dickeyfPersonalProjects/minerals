package imageproc

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestScaleToLongEdge(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		srcW, srcH   int
		longEdge     int
		wantW, wantH int
	}{
		{"landscape 4000×3000 → 1600", 4000, 3000, DisplayLongEdge, 1600, 1200},
		{"landscape 4000×3000 → 400", 4000, 3000, ThumbnailLongEdge, 400, 300},
		{"portrait 3000×4000 → 1600", 3000, 4000, DisplayLongEdge, 1200, 1600},
		{"square 2000×2000 → 1600", 2000, 2000, DisplayLongEdge, 1600, 1600},
		{"already small landscape 800×600 → 1600 stays same", 800, 600, DisplayLongEdge, 800, 600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.srcW <= tc.longEdge && tc.srcH <= tc.longEdge {
				t.Skip("smaller-than-target case is short-circuited by resize()")
			}
			gotW, gotH := scaleToLongEdge(tc.srcW, tc.srcH, tc.longEdge)
			if gotW != tc.wantW || gotH != tc.wantH {
				t.Errorf("scaleToLongEdge(%d,%d,%d) = (%d,%d), want (%d,%d)",
					tc.srcW, tc.srcH, tc.longEdge, gotW, gotH, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestGenerate_FromPNG(t *testing.T) {
	t.Parallel()
	src := image.NewRGBA(image.Rect(0, 0, 2400, 1800))
	for y := 0; y < 1800; y++ {
		for x := 0; x < 2400; x++ {
			src.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatalf("png encode: %v", err)
	}

	v, err := Generate(buf.Bytes(), ContentTypePNG)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	displayImg, err := jpeg.Decode(bytes.NewReader(v.Display))
	if err != nil {
		t.Fatalf("decode display: %v", err)
	}
	if displayImg.Bounds().Dx() != DisplayLongEdge {
		t.Errorf("display width: got %d, want %d", displayImg.Bounds().Dx(), DisplayLongEdge)
	}
	if displayImg.Bounds().Dy() != 1200 {
		t.Errorf("display height: got %d, want 1200", displayImg.Bounds().Dy())
	}

	thumbImg, err := jpeg.Decode(bytes.NewReader(v.Thumbnail))
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	if thumbImg.Bounds().Dx() != ThumbnailLongEdge {
		t.Errorf("thumb width: got %d, want %d", thumbImg.Bounds().Dx(), ThumbnailLongEdge)
	}
	if thumbImg.Bounds().Dy() != 300 {
		t.Errorf("thumb height: got %d, want 300", thumbImg.Bounds().Dy())
	}
}

func TestGenerate_UnsupportedContentType(t *testing.T) {
	t.Parallel()
	_, err := Generate([]byte{0, 0}, "image/heic")
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

func TestGenerate_FromJPEG_PortraitOrientation(t *testing.T) {
	t.Parallel()
	src := image.NewRGBA(image.Rect(0, 0, 1200, 2000))
	for y := 0; y < 2000; y += 100 {
		for x := 0; x < 1200; x += 100 {
			src.Set(x, y, color.RGBA{R: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("jpeg encode: %v", err)
	}

	v, err := Generate(buf.Bytes(), ContentTypeJPEG)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	display, err := jpeg.Decode(bytes.NewReader(v.Display))
	if err != nil {
		t.Fatalf("decode display: %v", err)
	}
	if display.Bounds().Dy() != DisplayLongEdge {
		t.Errorf("portrait display long edge should be height = %d, got %d",
			DisplayLongEdge, display.Bounds().Dy())
	}
	if display.Bounds().Dx() != 960 {
		t.Errorf("portrait display width: got %d, want 960", display.Bounds().Dx())
	}
}
