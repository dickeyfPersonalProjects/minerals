// Package imageproc generates the display + thumbnail JPEG variants
// the upload pipeline writes to MinIO alongside the original (per
// CONTRACT.md §12).
//
// Decoders: image/jpeg, image/png stdlib + golang.org/x/image/webp.
// HEIC is NOT supported in v1 — pure-Go HEIC libraries are immature
// and §16 forbids cgo. The photos handler rejects image/heic at the
// content-type allowlist before reaching this package; a follow-up
// bead reopens v1.1 HEIC support if there's demand.
//
// Resize: golang.org/x/image/draw.CatmullRom (high-quality kernel,
// per §12 — "Resize via golang.org/x/image/draw with high-quality
// kernel").
package imageproc

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register webp decoder
)

// Variant target dimensions and JPEG quality (per CONTRACT.md §12).
const (
	DisplayLongEdge   = 1600
	DisplayJPEGQ      = 85
	ThumbnailLongEdge = 400
	ThumbnailJPEGQ    = 80
)

// Image content types this package can decode.
const (
	ContentTypeJPEG = "image/jpeg"
	ContentTypePNG  = "image/png"
	ContentTypeWebP = "image/webp"
)

// Variants holds the JPEG-encoded display and thumbnail bytes
// generated from a source image.
type Variants struct {
	Display   []byte
	Thumbnail []byte
}

// Generate decodes data (per contentType), produces display +
// thumbnail variants per the §12 sizing rules, and JPEG-encodes them.
// Errors include unsupported content types and malformed image bytes.
func Generate(data []byte, contentType string) (Variants, error) {
	src, err := decode(data, contentType)
	if err != nil {
		return Variants{}, err
	}

	display, err := encodeJPEG(resize(src, DisplayLongEdge), DisplayJPEGQ)
	if err != nil {
		return Variants{}, fmt.Errorf("imageproc: encode display: %w", err)
	}
	thumb, err := encodeJPEG(resize(src, ThumbnailLongEdge), ThumbnailJPEGQ)
	if err != nil {
		return Variants{}, fmt.Errorf("imageproc: encode thumbnail: %w", err)
	}
	return Variants{Display: display, Thumbnail: thumb}, nil
}

// ErrUnsupportedContentType is returned by Generate / decode when the
// caller passes a content type this package doesn't know how to
// decode.
var ErrUnsupportedContentType = errors.New("imageproc: unsupported content type")

// MaxPixels caps the decoded pixel count (width × height) we'll accept.
// The 100 MiB byte cap on uploads bounds only *compressed* input; a
// few-KB image header can declare enormous dimensions (e.g. 30000×30000)
// that decode to gigabytes of RGBA — a decompression-bomb DoS. We read
// the dimensions via DecodeConfig (which parses only the header, not the
// pixel data) and reject before the full decode allocates anything.
//
// 100 megapixels comfortably exceeds any legitimate phone/camera photo
// (a 100 MP source decodes to ~400 MiB RGBA) while rejecting bombs.
const MaxPixels = 100 * 1000 * 1000

// ErrImageTooLarge is returned by decode when an image's declared pixel
// dimensions exceed MaxPixels.
var ErrImageTooLarge = errors.New("imageproc: image pixel dimensions exceed cap")

func decode(data []byte, contentType string) (image.Image, error) {
	switch contentType {
	case ContentTypeJPEG, ContentTypePNG, ContentTypeWebP:
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedContentType, contentType)
	}

	// Pixel-dimension cap: parse just the header to reject oversized
	// images before the full decode allocates the pixel buffer. The
	// stdlib jpeg/png decoders and the x/image/webp decoder all register
	// with image, so image.DecodeConfig dispatches by sniffing the bytes.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return nil, fmt.Errorf("%w: %d×%d", ErrImageTooLarge, cfg.Width, cfg.Height)
	}

	switch contentType {
	case ContentTypeJPEG:
		return jpeg.Decode(bytes.NewReader(data))
	case ContentTypePNG:
		return png.Decode(bytes.NewReader(data))
	default: // ContentTypeWebP
		// image.Decode dispatches via the package-level registrations
		// in the side-effect import above.
		img, _, err := image.Decode(bytes.NewReader(data))
		return img, err
	}
}

// resize scales src so its longer edge equals longEdge. Aspect ratio
// is preserved; the resulting image is always RGBA (so the JPEG
// encoder gets a friendly input). Images smaller than longEdge in
// both dimensions pass through verbatim — re-encoding a small image
// to a larger canvas would only inflate file size for no quality
// gain.
func resize(src image.Image, longEdge int) image.Image {
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	if srcW <= longEdge && srcH <= longEdge {
		return src
	}
	dstW, dstH := scaleToLongEdge(srcW, srcH, longEdge)
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)
	return dst
}

// scaleToLongEdge returns the destination dimensions for an image of
// (srcW, srcH) scaled so the longer edge equals longEdge. Exposed
// for unit testing the resize math.
func scaleToLongEdge(srcW, srcH, longEdge int) (int, int) {
	if srcW >= srcH {
		dstW := longEdge
		dstH := int(float64(srcH) * float64(longEdge) / float64(srcW))
		if dstH < 1 {
			dstH = 1
		}
		return dstW, dstH
	}
	dstH := longEdge
	dstW := int(float64(srcW) * float64(longEdge) / float64(srcH))
	if dstW < 1 {
		dstW = 1
	}
	return dstW, dstH
}

func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
