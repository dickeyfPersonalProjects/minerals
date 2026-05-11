package exif

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
	"time"

	exif "github.com/dsoprea/go-exif/v3"
	exifcommon "github.com/dsoprea/go-exif/v3/common"
)

// makeJPEGWithExif constructs a minimal JPEG with an APP1/Exif segment
// carrying the supplied raw EXIF blob. The image content is a 2×2
// grayscale block — fixtures stay tiny, well under 1 KB.
func makeJPEGWithExif(t testing.TB, rawExif []byte) []byte {
	t.Helper()

	var imgBuf bytes.Buffer
	img := image.NewGray(image.Rect(0, 0, 2, 2))
	img.SetGray(0, 0, color.Gray{Y: 0})
	img.SetGray(1, 1, color.Gray{Y: 255})
	if err := jpeg.Encode(&imgBuf, img, &jpeg.Options{Quality: 50}); err != nil {
		t.Fatalf("jpeg encode: %v", err)
	}
	jp := imgBuf.Bytes()

	// jpeg.Encode emits SOI + (DQT | APP0 | ...) + ... + EOI. Insert
	// APP1/Exif immediately after the SOI marker; the JPEG spec
	// permits APP markers to appear anywhere before SOS.
	if len(jp) < 4 || jp[0] != 0xFF || jp[1] != 0xD8 {
		t.Fatalf("unexpected jpeg encoder output")
	}
	insertAt := 2 // right after SOI

	exifPayload := append([]byte("Exif\x00\x00"), rawExif...)
	if len(exifPayload)+2 > 65535 {
		t.Fatalf("exif segment too large for test")
	}
	var lenBytes [2]byte
	binary.BigEndian.PutUint16(lenBytes[:], uint16(len(exifPayload)+2)) //nolint:gosec // G115: bounded by the check above

	out := bytes.Buffer{}
	out.Write(jp[:insertAt])
	out.Write([]byte{0xFF, 0xE1})
	out.Write(lenBytes[:])
	out.Write(exifPayload)
	out.Write(jp[insertAt:])
	return out.Bytes()
}

// buildExifBlob constructs an EXIF blob with the provided IFD0 + Exif
// + GPS tags. tags is keyed by (ifdPath -> tagId -> value).
func buildExifBlob(t testing.TB, ifd0 map[uint16]any, exifSub map[uint16]any, includeGPS bool) []byte {
	t.Helper()

	im, err := exifcommon.NewIfdMappingWithStandard()
	if err != nil {
		t.Fatalf("ifd mapping: %v", err)
	}
	ti := exif.NewTagIndex()

	rootIb := exif.NewIfdBuilder(im, ti, exifcommon.IfdStandardIfdIdentity, binary.BigEndian)
	for tagID, val := range ifd0 {
		if err := rootIb.AddStandard(tagID, val); err != nil {
			t.Fatalf("root add %#04x: %v", tagID, err)
		}
	}

	if len(exifSub) > 0 {
		exifIb := exif.NewIfdBuilder(im, ti, exifcommon.IfdExifStandardIfdIdentity, binary.BigEndian)
		for tagID, val := range exifSub {
			if err := exifIb.AddStandard(tagID, val); err != nil {
				t.Fatalf("exif add %#04x: %v", tagID, err)
			}
		}
		if err := rootIb.AddChildIb(exifIb); err != nil {
			t.Fatalf("add exif child: %v", err)
		}
	}

	if includeGPS {
		gpsIb := exif.NewIfdBuilder(im, ti, exifcommon.IfdGpsInfoStandardIfdIdentity, binary.BigEndian)
		// GPSVersionID is the only safe GPS tag to encode without a
		// great deal of ceremony — it's a 4-byte fixed sequence.
		if err := gpsIb.AddStandard(0x0000, []uint8{2, 2, 0, 0}); err != nil {
			t.Fatalf("gps add: %v", err)
		}
		if err := rootIb.AddChildIb(gpsIb); err != nil {
			t.Fatalf("add gps child: %v", err)
		}
	}

	ibe := exif.NewIfdByteEncoder()
	out, err := ibe.EncodeToExif(rootIb)
	if err != nil {
		t.Fatalf("encode exif: %v", err)
	}
	return out
}

func parseExif(t *testing.T, raw []byte) (root, exifSub, gps *exif.Ifd) {
	t.Helper()
	im, err := exifcommon.NewIfdMappingWithStandard()
	if err != nil {
		t.Fatalf("ifd mapping: %v", err)
	}
	ti := exif.NewTagIndex()
	_, index, err := exif.Collect(im, ti, raw)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	root = index.RootIfd
	if c, _ := root.ChildWithIfdPath(exifcommon.IfdExifStandardIfdIdentity); c != nil {
		exifSub = c
	}
	if c, _ := root.ChildWithIfdPath(exifcommon.IfdGpsInfoStandardIfdIdentity); c != nil {
		gps = c
	}
	return
}

// extractExifFromJPEG returns the raw EXIF blob from a JPEG, or nil if
// no APP1/Exif segment is present.
func extractExifFromJPEG(t *testing.T, data []byte) []byte {
	t.Helper()
	_, ex, err := walkJPEGSegments(data, false)
	if err != nil {
		t.Fatalf("walk jpeg: %v", err)
	}
	return ex
}

func TestFilter_DropsGPSIFD(t *testing.T) {
	raw := buildExifBlob(t,
		map[uint16]any{
			0x010F: "Acme",                // Make (allowed)
			0x0110: "TestCam",             // Model (allowed)
			0x0132: "2026:05:07 12:00:00", // DateTime (allowed)
		},
		map[uint16]any{
			0x9003: "2026:05:07 12:00:00", // DateTimeOriginal (allowed)
		},
		true, // include GPS IFD
	)
	jp := makeJPEGWithExif(t, raw)

	// Sanity: the source has GPS.
	_, _, gps := parseExif(t, extractExifFromJPEG(t, jp))
	if gps == nil {
		t.Fatal("test fixture missing GPS IFD before filter")
	}

	out, err := Filter(jp, ContentTypeJPEG)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}

	filteredExif := extractExifFromJPEG(t, out)
	if filteredExif == nil {
		t.Fatal("filtered jpeg has no EXIF segment (allowlisted tags should survive)")
	}
	_, _, gps = parseExif(t, filteredExif)
	if gps != nil {
		t.Errorf("GPS IFD survived filter: %v", gps)
	}
}

func TestFilter_PreservesAllowlistedTags(t *testing.T) {
	raw := buildExifBlob(t,
		map[uint16]any{
			0x010F: "Acme",                // Make
			0x0110: "TestCam",             // Model
			0x0132: "2026:05:07 12:00:00", // DateTime
			// 0x013B (Artist) is NOT on the allowlist.
			0x013B: "Photographer",
		},
		map[uint16]any{
			0x9003: "2026:05:07 12:00:00",                                  // DateTimeOriginal
			0x920A: []exifcommon.Rational{{Numerator: 50, Denominator: 1}}, // FocalLength
		},
		false,
	)
	jp := makeJPEGWithExif(t, raw)

	out, err := Filter(jp, ContentTypeJPEG)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}

	filteredExif := extractExifFromJPEG(t, out)
	if filteredExif == nil {
		t.Fatal("filtered jpeg has no EXIF segment")
	}
	root, exifSub, _ := parseExif(t, filteredExif)

	if entries, _ := root.FindTagWithId(0x010F); len(entries) == 0 {
		t.Errorf("Make (0x010F) should be preserved")
	}
	if entries, _ := root.FindTagWithId(0x0110); len(entries) == 0 {
		t.Errorf("Model (0x0110) should be preserved")
	}
	if entries, _ := root.FindTagWithId(0x013B); len(entries) > 0 {
		t.Errorf("Artist (0x013B) should be DROPPED — not on allowlist")
	}

	if exifSub == nil {
		t.Fatal("Exif sub-IFD should survive (it has allowlisted tags)")
	}
	if entries, _ := exifSub.FindTagWithId(0x9003); len(entries) == 0 {
		t.Errorf("DateTimeOriginal (0x9003) should be preserved")
	}
	if entries, _ := exifSub.FindTagWithId(0x920A); len(entries) == 0 {
		t.Errorf("FocalLength (0x920A) should be preserved")
	}
}

func TestExtractDateTimeOriginal(t *testing.T) {
	raw := buildExifBlob(t,
		map[uint16]any{0x010F: "Acme"},
		map[uint16]any{0x9003: "2026:05:07 12:34:56"},
		false,
	)
	jp := makeJPEGWithExif(t, raw)

	got := ExtractDateTimeOriginal(jp, ContentTypeJPEG)
	if got == nil {
		t.Fatal("expected DateTimeOriginal to be extracted")
	}
	want := time.Date(2026, 5, 7, 12, 34, 56, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractDateTimeOriginal_AbsentReturnsNil(t *testing.T) {
	raw := buildExifBlob(t,
		map[uint16]any{0x010F: "Acme"},
		nil,
		false,
	)
	jp := makeJPEGWithExif(t, raw)

	if got := ExtractDateTimeOriginal(jp, ContentTypeJPEG); got != nil {
		t.Errorf("expected nil, got %v", *got)
	}
}

func TestStripPNGMetadata_DropsExifChunk(t *testing.T) {
	// Build a tiny PNG and inject an eXIf chunk between IHDR and IDAT.
	var pngBuf bytes.Buffer
	img := image.NewGray(image.Rect(0, 0, 2, 2))
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	original := pngBuf.Bytes()
	withExif := injectPNGChunk(t, original, "eXIf", []byte{0x00, 0x01, 0x02, 0x03})

	out, err := stripPNGMetadata(withExif)
	if err != nil {
		t.Fatalf("strip: %v", err)
	}
	if bytes.Contains(out, []byte("eXIf")) {
		t.Errorf("eXIf chunk should have been dropped from PNG")
	}
}

// injectPNGChunk inserts a chunk of the given type immediately after
// the IHDR header. Used to fabricate test PNGs with metadata chunks.
func injectPNGChunk(t *testing.T, src []byte, chunkType string, payload []byte) []byte {
	t.Helper()
	if len(src) < 8 {
		t.Fatalf("png too short")
	}
	// Skip past 8-byte signature + IHDR (length 4 + type 4 + 13 data + crc 4 = 25 bytes).
	insertAt := 8 + 25
	chunk := bytes.Buffer{}
	var lenBytes [4]byte
	binary.BigEndian.PutUint32(lenBytes[:], uint32(len(payload))) //nolint:gosec // G115: test fixture payloads are small (<1KB)
	chunk.Write(lenBytes[:])
	chunk.WriteString(chunkType)
	chunk.Write(payload)
	// CRC over type+payload. Use the standard CRC-32 IEEE poly via crc32.
	crc := pngCRC([]byte(chunkType), payload)
	var crcBytes [4]byte
	binary.BigEndian.PutUint32(crcBytes[:], crc)
	chunk.Write(crcBytes[:])
	out := bytes.Buffer{}
	out.Write(src[:insertAt])
	out.Write(chunk.Bytes())
	out.Write(src[insertAt:])
	return out.Bytes()
}

func pngCRC(parts ...[]byte) uint32 {
	const poly = uint32(0xEDB88320)
	crc := uint32(0xFFFFFFFF)
	for _, p := range parts {
		for _, b := range p {
			crc ^= uint32(b)
			for i := 0; i < 8; i++ {
				if crc&1 == 1 {
					crc = (crc >> 1) ^ poly
				} else {
					crc >>= 1
				}
			}
		}
	}
	return crc ^ 0xFFFFFFFF
}

func TestStripWebPMetadata_DropsExifChunk(t *testing.T) {
	// Build a minimal WebP RIFF container with a VP8L (image) chunk
	// and an EXIF chunk. We don't need a valid VP8L payload — strip
	// just walks the chunk index.
	var b bytes.Buffer
	b.WriteString("RIFF")
	b.Write([]byte{0, 0, 0, 0}) // placeholder size
	b.WriteString("WEBP")
	b.WriteString("VP8L")
	vp8lPayload := []byte{0x2F, 0x00, 0x00, 0x00, 0x00, 0x00}                               // minimal-looking
	if err := binary.Write(&b, binary.LittleEndian, uint32(len(vp8lPayload))); err != nil { //nolint:gosec // G115: 6-byte fixed fixture
		t.Fatalf("write len: %v", err)
	}
	b.Write(vp8lPayload)
	b.WriteString("EXIF")
	exifPayload := []byte{0x49, 0x49, 0x2A, 0x00, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00}
	if err := binary.Write(&b, binary.LittleEndian, uint32(len(exifPayload))); err != nil { //nolint:gosec // G115: 10-byte fixed fixture
		t.Fatalf("write exif len: %v", err)
	}
	b.Write(exifPayload)
	raw := b.Bytes()
	binary.LittleEndian.PutUint32(raw[4:8], uint32(len(raw)-8)) //nolint:gosec // G115: test fixture <1KB

	out, err := stripWebPMetadata(raw)
	if err != nil {
		t.Fatalf("strip webp: %v", err)
	}
	if bytes.Contains(out, []byte("EXIF")) {
		t.Errorf("EXIF chunk should have been dropped from WebP")
	}
}

// FuzzParseExif drives arbitrary bytes through the JPEG path of the
// EXIF filter (Filter + ExtractDateTimeOriginal). The parser handles
// untrusted upload bytes per CONTRACT.md §17 — the primary goal of
// this harness is to surface panics, infinite loops, or unbounded
// allocations from malformed JPEGs and EXIF blobs.
//
// Seed corpus mirrors the fixtures the unit tests construct: a JPEG
// with allowlisted root + Exif tags, a JPEG with a GPS sub-IFD, and
// a JPEG carrying only the bare minimum EXIF segment.
func FuzzParseExif(f *testing.F) {
	// Seed 1: allowlisted root + Exif tags only.
	raw1 := buildExifBlob(f,
		map[uint16]any{
			0x010F: "Acme",
			0x0110: "TestCam",
			0x0132: "2026:05:07 12:00:00",
		},
		map[uint16]any{
			0x9003: "2026:05:07 12:00:00",
			0x920A: []exifcommon.Rational{{Numerator: 50, Denominator: 1}},
		},
		false,
	)
	f.Add(makeJPEGWithExif(f, raw1))

	// Seed 2: includes a GPS IFD (which Filter must drop).
	raw2 := buildExifBlob(f,
		map[uint16]any{
			0x010F: "Acme",
			0x0110: "TestCam",
		},
		map[uint16]any{
			0x9003: "2026:05:07 12:00:00",
		},
		true,
	)
	f.Add(makeJPEGWithExif(f, raw2))

	// Seed 3: minimal EXIF (single tag, no Exif sub-IFD).
	raw3 := buildExifBlob(f,
		map[uint16]any{0x010F: "Acme"},
		nil,
		false,
	)
	f.Add(makeJPEGWithExif(f, raw3))

	f.Fuzz(func(_ *testing.T, data []byte) {
		// Filter must not panic on arbitrary input. Errors are fine —
		// the JPEG walker rejects malformed segments. We discard the
		// returned bytes; the goal is panic discovery.
		_, _ = Filter(data, ContentTypeJPEG)

		// ExtractDateTimeOriginal walks the same parser path with a
		// different exit; exercise both to widen coverage.
		_ = ExtractDateTimeOriginal(data, ContentTypeJPEG)
	})
}
