package db

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// listCursor is the encoded shape behind an opaque list cursor (per
// CONTRACT.md §10 — pagination contract). The exported wire form is
// always base64-encoded JSON; clients MUST treat the cursor as opaque.
type listCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        uuid.UUID `json:"id"`
}

// EncodeCursor serialises a (created_at, id) pair into the opaque
// base64 cursor used by list endpoints. An empty result returns an
// empty string — handlers map that to a JSON `null` next_cursor.
func EncodeCursor(createdAt time.Time, id uuid.UUID) string {
	c := listCursor{CreatedAt: createdAt.UTC(), ID: id}
	b, err := json.Marshal(c)
	if err != nil {
		// Marshalling a fixed-shape struct cannot fail in practice.
		panic(fmt.Errorf("cursor marshal: %w", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor parses an opaque cursor produced by EncodeCursor. An
// empty input yields a zero cursor (caller treats this as "first
// page"). A malformed cursor yields an error — the handler maps it
// to a 400 envelope.
func DecodeCursor(s string) (time.Time, uuid.UUID, error) {
	if s == "" {
		return time.Time{}, uuid.Nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("cursor: invalid base64: %w", err)
	}
	var c listCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("cursor: invalid payload: %w", err)
	}
	if c.ID == uuid.Nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("cursor: missing id")
	}
	return c.CreatedAt, c.ID, nil
}
