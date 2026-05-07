package db

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// specimenCursor is the decoded form of a list-specimens pagination
// cursor. Exactly one of the two ordering shapes is populated:
//
//   - Default ordering (created_at DESC, id DESC): CreatedAt + ID.
//   - tsv-rank ordering (ts_rank DESC, id DESC): Rank + ID, with
//     CreatedAt zero. Rank is preserved verbatim so subsequent pages
//     compare against the same value the previous query returned.
//
// The cursor is opaque to clients (CONTRACT.md §10.3); the encoded
// shape may change between versions without breaking compatibility
// because the only operations on it are encode/decode round-trips.
type specimenCursor struct {
	CreatedAt *time.Time `json:"c,omitempty"`
	Rank      *float64   `json:"r,omitempty"`
	ID        uuid.UUID  `json:"i"`
}

func (c specimenCursor) isRank() bool { return c.Rank != nil }

// encodeCursor base64-encodes the JSON form of c. Returns "" when c
// is the zero value (used as the end-of-results sentinel).
func encodeCursor(c specimenCursor) (string, error) {
	if c.CreatedAt == nil && c.Rank == nil && c.ID == uuid.Nil {
		return "", nil
	}
	b, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("specimen cursor: marshal: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// decodeCursor parses the encoded form. An empty string yields a
// zero specimenCursor and no error.
func decodeCursor(s string) (specimenCursor, error) {
	var c specimenCursor
	if s == "" {
		return c, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, fmt.Errorf("specimen cursor: base64 decode: %w", err)
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("specimen cursor: unmarshal: %w", err)
	}
	if c.ID == uuid.Nil {
		return c, fmt.Errorf("specimen cursor: missing id")
	}
	return c, nil
}
