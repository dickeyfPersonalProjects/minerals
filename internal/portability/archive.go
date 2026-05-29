package portability

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

// Archive size guards (mi-dkuu §security). A ZIP is an untrusted upload,
// so every decompression is bounded: a single entry cannot inflate past
// maxEntryBytes and the whole archive cannot inflate past
// maxTotalUncompressedBytes, defeating zip-bomb amplification. These are
// deliberately generous (collections with many full-resolution photos)
// while still finite.
const (
	maxEntryBytes             = 256 << 20 // 256 MiB per entry (a single large original)
	maxTotalUncompressedBytes = 8 << 30   // 8 GiB total inflated
	maxJSONLineBytes          = 4 << 20   // 4 MiB per JSONL record
)

// Archive is a parsed, validated-on-open view over an import ZIP held in
// memory. It exposes the manifest and per-entity record readers, and
// random access to file binaries by in-archive path. Construct with
// OpenArchive.
type Archive struct {
	zr       *zip.Reader
	files    map[string]*zip.File // name -> entry, for O(1) lookup
	manifest Manifest
	// inflated tracks total decompressed bytes read across all entries
	// so the per-archive cap is enforced cumulatively, not just per call.
	inflated int64
}

// OpenArchive parses raw as a ZIP, reads and structurally validates
// manifest.json, and indexes the entries. It does NOT yet validate the
// data records or binaries — call the engine's validation for that. A
// malformed container or unreadable/empty manifest is reported as a
// *ValidationError so the caller can surface a 400.
func OpenArchive(raw []byte) (*Archive, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, &ValidationError{
			Code:    CodeMalformedArchive,
			Message: "the upload is not a valid ZIP archive",
		}
	}
	a := &Archive{zr: zr, files: make(map[string]*zip.File, len(zr.File))}
	for _, f := range zr.File {
		a.files[f.Name] = f
	}

	mf, ok := a.files[ManifestPath]
	if !ok {
		return nil, &ValidationError{
			Code:    CodeMalformedArchive,
			Message: "archive is missing manifest.json",
		}
	}
	data, err := a.readWhole(mf)
	if err != nil {
		return nil, &ValidationError{
			Code:    CodeMalformedArchive,
			Message: "could not read manifest.json: " + err.Error(),
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a.manifest); err != nil {
		return nil, &ValidationError{
			Code:    CodeMalformedArchive,
			Message: "manifest.json is not valid: " + err.Error(),
		}
	}
	return a, nil
}

// Manifest returns the parsed archive manifest.
func (a *Archive) Manifest() Manifest { return a.manifest }

// HasEntry reports whether the archive contains an entry at name.
func (a *Archive) HasEntry(name string) bool {
	_, ok := a.files[name]
	return ok
}

// readWhole reads an entry fully into memory under the per-entry and
// per-archive size caps.
func (a *Archive) readWhole(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	limited := io.LimitReader(rc, maxEntryBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxEntryBytes {
		return nil, fmt.Errorf("entry %q exceeds the %d-byte limit", f.Name, int64(maxEntryBytes))
	}
	if a.addInflated(int64(len(data))) {
		return nil, fmt.Errorf("archive exceeds the %d-byte total uncompressed limit", int64(maxTotalUncompressedBytes))
	}
	return data, nil
}

// addInflated adds n to the cumulative decompressed counter and reports
// whether the per-archive cap was exceeded.
func (a *Archive) addInflated(n int64) (exceeded bool) {
	a.inflated += n
	return a.inflated > maxTotalUncompressedBytes
}

// OpenFileBinary returns the bytes of the file binary at path, verifying
// its length and SHA-256 against the supplied integrity values. A
// missing entry, size mismatch, or hash mismatch is a *ValidationError.
func (a *Archive) OpenFileBinary(path, wantSHA256 string, wantSize int64) ([]byte, error) {
	f, ok := a.files[path]
	if !ok {
		return nil, &ValidationError{
			Code:    CodeIntegrity,
			Message: fmt.Sprintf("file binary %q referenced by the manifest is missing from the archive", path),
		}
	}
	data, err := a.readWhole(f)
	if err != nil {
		return nil, &ValidationError{
			Code:    CodeIntegrity,
			Message: fmt.Sprintf("could not read file binary %q: %v", path, err),
		}
	}
	if int64(len(data)) != wantSize {
		return nil, &ValidationError{
			Code:    CodeIntegrity,
			Message: fmt.Sprintf("file binary %q is %d bytes, manifest declares %d", path, len(data), wantSize),
		}
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != wantSHA256 {
		return nil, &ValidationError{
			Code:    CodeIntegrity,
			Message: fmt.Sprintf("file binary %q hash %s does not match manifest %s", path, got, wantSHA256),
		}
	}
	return data, nil
}

// RawFile returns the bytes of the binary at path bounded by the
// per-entry cap, without touching the cumulative-inflation counter. Used
// in the post-commit upload phase, after validation has already verified
// the binary's presence, size, and hash.
func (a *Archive) RawFile(path string) ([]byte, error) {
	f, ok := a.files[path]
	if !ok {
		return nil, fmt.Errorf("file binary %q not found", path)
	}
	return a.readEntryUncounted(f)
}

// readEntryUncounted reads an entry under the per-entry cap only.
func (a *Archive) readEntryUncounted(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, maxEntryBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxEntryBytes {
		return nil, fmt.Errorf("entry %q exceeds the %d-byte limit", f.Name, int64(maxEntryBytes))
	}
	return data, nil
}

// decodeJSONL reads the entry at path (when present) and decodes each
// non-blank line into a fresh T, appending to out. A missing entry is
// treated as an empty collection (not every archive carries every entity
// type). A malformed line is a *ValidationError naming the path.
func decodeJSONL[T any](a *Archive, path string) ([]T, error) {
	f, ok := a.files[path]
	if !ok {
		return nil, nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil, &ValidationError{Code: CodeMalformedArchive, Message: fmt.Sprintf("could not open %q: %v", path, err)}
	}
	defer func() { _ = rc.Close() }()

	// Bound total inflation for this entry while streaming line-by-line.
	limited := io.LimitReader(rc, maxEntryBytes+1)
	sc := bufio.NewScanner(limited)
	sc.Buffer(make([]byte, 0, 64*1024), maxJSONLineBytes)

	var out []T
	var read int64
	line := 0
	for sc.Scan() {
		line++
		b := sc.Bytes()
		read += int64(len(b)) + 1
		if read > maxEntryBytes {
			return nil, &ValidationError{Code: CodeMalformedArchive, Message: fmt.Sprintf("%q exceeds the per-entry size limit", path)}
		}
		if len(bytes.TrimSpace(b)) == 0 {
			continue
		}
		var rec T
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&rec); err != nil {
			return nil, &ValidationError{
				Code:    CodeMalformedArchive,
				Message: fmt.Sprintf("%s line %d is not a valid record: %v", path, line, err),
			}
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, &ValidationError{Code: CodeMalformedArchive, Message: fmt.Sprintf("could not read %q: %v", path, err)}
	}
	if a.addInflated(read) {
		return nil, &ValidationError{Code: CodeMalformedArchive, Message: "archive exceeds the total uncompressed limit"}
	}
	return out, nil
}
