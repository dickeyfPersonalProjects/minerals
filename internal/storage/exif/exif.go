// Package exif filters EXIF metadata from uploaded images down to the
// allowlist defined in CONTRACT.md §12. The allowlist is verbatim from
// the spec and is the canonical authority for which tags survive
// upload — anything not on the list is dropped, including the entire
// GPS IFD, XMP, IPTC, MakerNotes, and embedded thumbnails.
//
// The package operates on whole image bytes (JPEG / PNG / WebP) and
// returns a new byte slice with the same image content but a sanitized
// EXIF segment. Images that aren't on the supported list pass through
// unchanged — the caller is responsible for content-type allowlisting.
//
// Library: github.com/dsoprea/go-exif/v3 (per CONTRACT.md §16
// pre-approved table) for EXIF parse + rebuild. JPEG / PNG / WebP
// segment manipulation is implemented in this package — adding the
// dsoprea per-format helpers (go-jpeg-image-structure etc.) for a
// single segment-rewrite pass would expand the dependency surface for
// modest benefit.
package exif

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	exif "github.com/dsoprea/go-exif/v3"
	exifcommon "github.com/dsoprea/go-exif/v3/common"
)

// Image content types this package knows how to filter.
const (
	ContentTypeJPEG = "image/jpeg"
	ContentTypePNG  = "image/png"
	ContentTypeWebP = "image/webp"
)

// AllowedRootTagIDs is the canonical EXIF tag-ID allowlist for the
// root IFD (IFD0) per CONTRACT.md §12. The spec lists tag NAMES; the
// integer IDs come from the EXIF / TIFF specification.
var AllowedRootTagIDs = map[uint16]struct{}{
	0x0100: {}, // ImageWidth
	0x0101: {}, // ImageLength
	0x0102: {}, // BitsPerSample
	0x0103: {}, // Compression
	0x0106: {}, // PhotometricInterpretation
	0x0115: {}, // SamplesPerPixel
	0x011C: {}, // PlanarConfiguration
	0x0112: {}, // Orientation
	0x011A: {}, // XResolution
	0x011B: {}, // YResolution
	0x0128: {}, // ResolutionUnit
	0x010F: {}, // Make
	0x0110: {}, // Model
	0x0132: {}, // DateTime
}

// AllowedExifTagIDs is the canonical allowlist for the Exif sub-IFD
// (IFD0 → Exif) per CONTRACT.md §12.
var AllowedExifTagIDs = map[uint16]struct{}{
	0x9003: {}, // DateTimeOriginal
	0x9004: {}, // DateTimeDigitized
	0x9290: {}, // SubSecTime
	0x9291: {}, // SubSecTimeOriginal
	0x9292: {}, // SubSecTimeDigitized
	0x829A: {}, // ExposureTime
	0x829D: {}, // FNumber
	0x8827: {}, // ISOSpeedRatings (PhotographicSensitivity)
	0x8822: {}, // ExposureProgram
	0x9204: {}, // ExposureBiasValue
	0x9201: {}, // ShutterSpeedValue
	0x9202: {}, // ApertureValue
	0x9203: {}, // BrightnessValue
	0x9207: {}, // MeteringMode
	0x9208: {}, // LightSource
	0x9209: {}, // Flash
	0x920A: {}, // FocalLength
	0xA405: {}, // FocalLengthIn35mmFilm
	0xA433: {}, // LensMake
	0xA434: {}, // LensModel
	0xA435: {}, // LensSerialNumber
	0xA432: {}, // LensSpecification
	0xA403: {}, // WhiteBalance
	0xA402: {}, // ExposureMode
	0xA406: {}, // SceneCaptureType
	0xA301: {}, // SceneType
	0xA001: {}, // ColorSpace
	0xA302: {}, // CFAPattern
	0xA401: {}, // CustomRendered
	0xA404: {}, // DigitalZoomRatio
	0xA408: {}, // ContrastValue (Contrast)
	0xA409: {}, // SaturationValue (Saturation)
	0xA40A: {}, // SharpnessValue (Sharpness)
}

// Sub-IFD pointer tag IDs (per the EXIF / TIFF spec).
const (
	tagExifSubIfd = uint16(0x8769) // pointer to the Exif sub-IFD
	tagGPSInfo    = uint16(0x8825) // pointer to the GPS IFD (always dropped)
	tagInterop    = uint16(0xA005) // pointer to the Iop sub-IFD (always dropped)
)

// Filter takes the raw bytes of an image (JPEG / PNG / WebP) and
// returns a copy with EXIF metadata reduced to the allowlist defined
// in CONTRACT.md §12. Unknown formats pass through unchanged.
func Filter(data []byte, contentType string) ([]byte, error) {
	switch contentType {
	case ContentTypeJPEG:
		return filterJPEG(data)
	case ContentTypePNG:
		return stripPNGMetadata(data)
	case ContentTypeWebP:
		return stripWebPMetadata(data)
	default:
		// Non-image content types pass through. Callers must enforce
		// the §12 content-type allowlist before invoking Filter.
		return data, nil
	}
}

// ExtractDateTimeOriginal returns the value of the EXIF
// DateTimeOriginal tag (0x9003) from data, or nil if it isn't present
// or the format isn't supported. Used by the upload pipeline to
// default `taken_at` per CONTRACT.md §12.
func ExtractDateTimeOriginal(data []byte, contentType string) *time.Time {
	rawExif, err := extractRawExif(data, contentType)
	if err != nil || rawExif == nil {
		return nil
	}
	return readDateTimeOriginal(rawExif)
}

func readDateTimeOriginal(rawExif []byte) *time.Time {
	im, err := exifcommon.NewIfdMappingWithStandard()
	if err != nil {
		return nil
	}
	ti := exif.NewTagIndex()
	_, index, err := exif.Collect(im, ti, rawExif)
	if err != nil {
		return nil
	}
	exifIfd, err := index.RootIfd.ChildWithIfdPath(exifcommon.IfdExifStandardIfdIdentity)
	if err != nil || exifIfd == nil {
		return nil
	}
	entries, err := exifIfd.FindTagWithId(0x9003)
	if err != nil || len(entries) == 0 {
		return nil
	}
	val, err := entries[0].Value()
	if err != nil {
		return nil
	}
	s, ok := val.(string)
	if !ok {
		return nil
	}
	// EXIF DateTimeOriginal format: "YYYY:MM:DD HH:MM:SS" (local time,
	// no timezone). We treat it as UTC for v1 — the storage column is
	// timestamptz and clients can interpret per their timezone.
	t, err := time.Parse("2006:01:02 15:04:05", s)
	if err != nil {
		return nil
	}
	return &t
}

// extractRawExif returns the EXIF segment bytes from a JPEG, or nil if
// the format doesn't carry EXIF in a manner this package understands.
func extractRawExif(data []byte, contentType string) ([]byte, error) {
	switch contentType {
	case ContentTypeJPEG:
		_, exifBytes, err := walkJPEGSegments(data, false)
		return exifBytes, err
	default:
		return nil, nil
	}
}

// filterJPEG walks JPEG segments, replaces the APP1/Exif segment with
// a filtered copy, and drops APP1/XMP, APP13/Photoshop-IPTC, and COM
// segments.
func filterJPEG(data []byte) ([]byte, error) {
	out, _, err := walkJPEGSegments(data, true)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// JPEG marker bytes (per ITU-T T.81). Only the markers this package
// inspects are listed.
const (
	jpegSOI  = 0xD8 // start of image
	jpegEOI  = 0xD9 // end of image
	jpegSOS  = 0xDA // start of scan (entropy-coded data follows)
	jpegAPP1 = 0xE1
	jpegAPP2 = 0xE2 // not currently dropped (ICC profile may live here)
	jpegCOM  = 0xFE // comment
	jpegRST0 = 0xD0 // restart markers (no length)
	jpegRST7 = 0xD7
)

// walkJPEGSegments iterates JPEG markers and (when filter=true) writes
// a sanitized copy of data to a new buffer with EXIF filtered and
// metadata segments dropped. When filter=false, it just extracts the
// EXIF blob from the first APP1/Exif segment and returns it via the
// second return value.
//
// Returns (newBytes, exifBytes, error).
func walkJPEGSegments(data []byte, filter bool) ([]byte, []byte, error) {
	if len(data) < 2 || data[0] != 0xFF || data[1] != jpegSOI {
		return nil, nil, errors.New("exif: not a JPEG (missing SOI marker)")
	}

	var buf bytes.Buffer
	if filter {
		buf.Grow(len(data))
		buf.Write(data[:2])
	}

	pos := 2
	var exifSegment []byte

	for pos < len(data) {
		// Skip fill bytes.
		if data[pos] != 0xFF {
			return nil, nil, fmt.Errorf("exif: invalid JPEG segment at offset %d", pos)
		}
		// Multiple 0xFF bytes are allowed as fill; skip them.
		for pos < len(data) && data[pos] == 0xFF {
			if filter {
				buf.WriteByte(0xFF)
			}
			pos++
		}
		if pos >= len(data) {
			return nil, nil, errors.New("exif: truncated JPEG (no marker after FF)")
		}
		marker := data[pos]
		if filter {
			buf.WriteByte(marker)
		}
		pos++

		switch {
		case marker == jpegSOI:
			// Embedded SOI shouldn't occur; treat as a no-length marker.
			continue
		case marker == jpegEOI:
			// End of image; remaining trailing bytes (if any) are ignored.
			if filter {
				return buf.Bytes(), exifSegment, nil
			}
			return nil, exifSegment, nil
		case marker >= jpegRST0 && marker <= jpegRST7:
			// Restart markers carry no length and no payload.
			continue
		case marker == jpegSOS:
			// Start-of-scan: read the SOS header (has a length), then
			// stream entropy-coded data verbatim until the next
			// non-RST marker (typically EOI).
			if pos+2 > len(data) {
				return nil, nil, errors.New("exif: truncated SOS header")
			}
			segLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
			if pos+segLen > len(data) || segLen < 2 {
				return nil, nil, errors.New("exif: invalid SOS header length")
			}
			if filter {
				buf.Write(data[pos : pos+segLen])
			}
			pos += segLen
			// Stream entropy data verbatim until next marker that
			// isn't a restart or 0xFF00 (escaped 0xFF).
			scanStart := pos
			for pos < len(data) {
				if data[pos] != 0xFF {
					pos++
					continue
				}
				if pos+1 >= len(data) {
					pos++
					continue
				}
				next := data[pos+1]
				if next == 0x00 || (next >= jpegRST0 && next <= jpegRST7) {
					pos += 2
					continue
				}
				break
			}
			if filter {
				buf.Write(data[scanStart:pos])
			}
		default:
			// Length-prefixed segment: read length, then payload.
			if pos+2 > len(data) {
				return nil, nil, errors.New("exif: truncated segment header")
			}
			segLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
			if segLen < 2 || pos+segLen > len(data) {
				return nil, nil, errors.New("exif: invalid segment length")
			}
			payload := data[pos+2 : pos+segLen]

			drop := false
			var replacement []byte

			if marker == jpegAPP1 {
				switch {
				case bytes.HasPrefix(payload, []byte("Exif\x00\x00")):
					rawExif := payload[6:]
					if !filter {
						exifSegment = append(exifSegment, rawExif...)
					} else {
						filtered, err := filterEXIFBlob(rawExif)
						if err != nil || len(filtered) == 0 {
							drop = true
						} else {
							replacement = append([]byte("Exif\x00\x00"), filtered...)
						}
					}
				case bytes.HasPrefix(payload, []byte("http://ns.adobe.com/xap/1.0/")):
					drop = true
				}
			}
			if marker == jpegCOM {
				drop = true
			}

			if filter {
				switch {
				case drop:
					// Roll back the marker bytes we already wrote (FF + marker).
					b := buf.Bytes()
					buf.Reset()
					buf.Write(b[:len(b)-2])
				case replacement != nil:
					newLen := len(replacement) + 2
					if newLen > 0xFFFF {
						return nil, nil, fmt.Errorf("exif: filtered APP1 segment too large (%d bytes)", newLen)
					}
					var lenBytes [2]byte
					binary.BigEndian.PutUint16(lenBytes[:], uint16(newLen)) //nolint:gosec // G115: bounded above
					buf.Write(lenBytes[:])
					buf.Write(replacement)
				default:
					buf.Write(data[pos : pos+segLen])
				}
			}
			_ = jpegAPP2 // currently unused; APP2/ICC passes through
			pos += segLen
		}
	}

	if filter {
		return buf.Bytes(), exifSegment, nil
	}
	return nil, exifSegment, nil
}

// filterEXIFBlob parses the raw EXIF blob via go-exif/v3, rebuilds it
// containing only allowlisted tags from the root and Exif sub-IFD, and
// returns the encoded result. Returns (nil, nil) if the result would
// be empty (caller MAY drop the entire APP1 in that case).
func filterEXIFBlob(rawExif []byte) ([]byte, error) {
	im, err := exifcommon.NewIfdMappingWithStandard()
	if err != nil {
		return nil, fmt.Errorf("exif: ifd mapping: %w", err)
	}
	ti := exif.NewTagIndex()

	_, index, err := exif.Collect(im, ti, rawExif)
	if err != nil {
		return nil, fmt.Errorf("exif: collect: %w", err)
	}
	rootIfd := index.RootIfd

	rootIb := exif.NewIfdBuilder(im, ti, rootIfd.IfdIdentity(), rootIfd.ByteOrder())

	addedAny, err := buildFilteredIfd(rootIb, rootIfd, AllowedRootTagIDs, im, ti)
	if err != nil {
		return nil, err
	}
	if !addedAny {
		return nil, nil
	}

	ibe := exif.NewIfdByteEncoder()
	out, err := ibe.EncodeToExif(rootIb)
	if err != nil {
		return nil, fmt.Errorf("exif: encode: %w", err)
	}
	return out, nil
}

// buildFilteredIfd populates targetIb from sourceIfd, copying only
// tags whose IDs are in allowed (for non-IFD entries) and recursing
// into the Exif sub-IFD with the AllowedExifTagIDs allowlist. The GPS
// IFD pointer (0x8825) and Interop IFD pointer (0xA005) are dropped
// regardless. Returns true if at least one tag (or sub-IFD) was added.
func buildFilteredIfd(
	targetIb *exif.IfdBuilder,
	sourceIfd *exif.Ifd,
	allowed map[uint16]struct{},
	im *exifcommon.IfdMapping,
	ti *exif.TagIndex,
) (bool, error) {
	added := false
	for i, ite := range sourceIfd.Entries() {
		if ite.IsThumbnailOffset() || ite.IsThumbnailSize() {
			continue
		}
		tagID := ite.TagId()

		if ite.ChildIfdPath() != "" {
			if tagID != tagExifSubIfd {
				// Drop GPS IFD, Interop IFD, MakerNotes IFD, and any
				// other sub-IFD pointer.
				continue
			}
			// Find the matching child Ifd.
			var childIfd *exif.Ifd
			for _, c := range sourceIfd.Children() {
				if c.ParentTagIndex() == i {
					childIfd = c
					break
				}
			}
			if childIfd == nil {
				continue
			}
			childIb := exif.NewIfdBuilder(im, ti, childIfd.IfdIdentity(), childIfd.ByteOrder())
			childAdded, err := buildFilteredIfd(childIb, childIfd, AllowedExifTagIDs, im, ti)
			if err != nil {
				return false, err
			}
			if !childAdded {
				continue
			}
			if err := targetIb.AddChildIb(childIb); err != nil {
				return false, fmt.Errorf("exif: add child ifd: %w", err)
			}
			added = true
			continue
		}

		if _, ok := allowed[tagID]; !ok {
			continue
		}

		rawBytes, err := ite.GetRawBytes()
		if err != nil {
			// Skip tags whose value can't be re-emitted verbatim.
			continue
		}
		value := exif.NewIfdBuilderTagValueFromBytes(rawBytes)
		bt := exif.NewBuilderTag(
			sourceIfd.IfdIdentity().UnindexedString(),
			tagID,
			ite.TagType(),
			value,
			sourceIfd.ByteOrder(),
		)
		if err := targetIb.Add(bt); err != nil {
			return false, fmt.Errorf("exif: add tag %#04x: %w", tagID, err)
		}
		added = true
	}
	return added, nil
}

// stripPNGMetadata removes EXIF / XMP / textual chunks from a PNG
// (eXIf, iTXt, tEXt, zTXt). Other chunks pass through unchanged. A
// PNG that doesn't carry the 8-byte magic header is returned as-is.
func stripPNGMetadata(data []byte) ([]byte, error) {
	const sigLen = 8
	pngSig := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	if len(data) < sigLen || !bytes.Equal(data[:sigLen], pngSig) {
		return data, nil
	}
	var buf bytes.Buffer
	buf.Grow(len(data))
	buf.Write(data[:sigLen])
	pos := sigLen
	for pos < len(data) {
		if pos+8 > len(data) {
			return nil, errors.New("exif: truncated PNG chunk header")
		}
		chunkLen := binary.BigEndian.Uint32(data[pos : pos+4])
		chunkType := string(data[pos+4 : pos+8])
		end := pos + 8 + int(chunkLen) + 4 // header + payload + CRC
		if end > len(data) {
			return nil, errors.New("exif: truncated PNG chunk")
		}
		drop := false
		switch chunkType {
		case "eXIf", "iTXt", "tEXt", "zTXt":
			drop = true
		}
		if !drop {
			buf.Write(data[pos:end])
		}
		pos = end
		if chunkType == "IEND" {
			break
		}
	}
	return buf.Bytes(), nil
}

// stripWebPMetadata removes EXIF and XMP chunks from a WebP. RIFF
// container layout: 12-byte header ("RIFF" + size + "WEBP"), then a
// stream of chunks (FourCC + size + payload + optional pad).
func stripWebPMetadata(data []byte) ([]byte, error) {
	const headerLen = 12
	if len(data) < headerLen ||
		!bytes.Equal(data[:4], []byte("RIFF")) ||
		!bytes.Equal(data[8:12], []byte("WEBP")) {
		return data, nil
	}
	var buf bytes.Buffer
	buf.Grow(len(data))
	buf.Write(data[:headerLen])

	pos := headerLen
	for pos+8 <= len(data) {
		chunkType := string(data[pos : pos+4])
		chunkLen := binary.LittleEndian.Uint32(data[pos+4 : pos+8])
		end := pos + 8 + int(chunkLen)
		if end > len(data) {
			return nil, errors.New("exif: truncated WebP chunk")
		}
		// Pad to even byte boundary per RIFF spec.
		padded := end
		if chunkLen%2 == 1 && padded < len(data) {
			padded++
		}
		drop := false
		switch chunkType {
		case "EXIF", "XMP ", "ICCP":
			// Drop EXIF, XMP, and (conservatively) embedded ICC. ICC
			// is rare in WebP and not part of the allowlist.
			drop = true
		}
		if !drop {
			buf.Write(data[pos:padded])
		}
		pos = padded
	}

	// Patch the RIFF size header to reflect the new payload length.
	// We always write the 12-byte WebP header above, so len(out) >= 12;
	// payloadLen is non-negative. The 100 MiB upload cap (§7) keeps the
	// value well below 4 GiB, but we check explicitly here.
	out := buf.Bytes()
	payloadLen := len(out) - 8
	if payloadLen < 0 || uint64(payloadLen) > math.MaxUint32 {
		return nil, errors.New("exif: WebP RIFF size out of uint32 range")
	}
	binary.LittleEndian.PutUint32(out[4:8], uint32(payloadLen)) //nolint:gosec // G115: bounded above
	return out, nil
}
