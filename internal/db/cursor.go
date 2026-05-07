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

// rankCursor is the cursor shape used when list ordering is
// `ts_rank DESC, created_at DESC, id DESC` (the `?q=` search path).
// The presence of a `rank` field tells DecodeRankCursor which
// ordering produced it; a rank cursor decoded as a default cursor
// rejects with a missing-id error (the SPA discards cursors when
// `q` changes per CONTRACT.md §10).
type rankCursor struct {
	Rank      float32   `json:"rank"`
	CreatedAt time.Time `json:"created_at"`
	ID        uuid.UUID `json:"id"`
	// Mode discriminates the cursor flavor at decode time.
	Mode string `json:"mode"`
}

const cursorModeRank = "rank"

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

// EncodeRankCursor serialises a (rank, created_at, id) triple into
// the opaque base64 cursor used by `?q=` search results. The cursor
// is structurally distinct from a default cursor — DecodeCursor on
// it returns an error.
func EncodeRankCursor(rank float32, createdAt time.Time, id uuid.UUID) string {
	c := rankCursor{Rank: rank, CreatedAt: createdAt.UTC(), ID: id, Mode: cursorModeRank}
	b, err := json.Marshal(c)
	if err != nil {
		panic(fmt.Errorf("rank cursor marshal: %w", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor parses an opaque cursor produced by EncodeCursor. An
// empty input yields a zero cursor (caller treats this as "first
// page"). A malformed cursor — including a rank cursor presented
// under default-ordering semantics — yields an error; the handler
// maps it to a 400 envelope.
func DecodeCursor(s string) (time.Time, uuid.UUID, error) {
	if s == "" {
		return time.Time{}, uuid.Nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("cursor: invalid base64: %w", err)
	}
	// Reject rank cursors offered under default ordering: the SPA
	// must discard cursors when q is added/removed (per §10).
	var probe struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil && probe.Mode == cursorModeRank {
		return time.Time{}, uuid.Nil, fmt.Errorf("cursor: rank cursor used under default ordering")
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

// DecodeRankCursor parses an opaque cursor produced by
// EncodeRankCursor. An empty input yields zero values (first page).
// A default cursor presented under tsv-rank ordering yields an
// error.
func DecodeRankCursor(s string) (rank float32, createdAt time.Time, id uuid.UUID, err error) {
	if s == "" {
		return 0, time.Time{}, uuid.Nil, nil
	}
	raw, derr := base64.RawURLEncoding.DecodeString(s)
	if derr != nil {
		return 0, time.Time{}, uuid.Nil, fmt.Errorf("cursor: invalid base64: %w", derr)
	}
	var c rankCursor
	if uerr := json.Unmarshal(raw, &c); uerr != nil {
		return 0, time.Time{}, uuid.Nil, fmt.Errorf("cursor: invalid payload: %w", uerr)
	}
	if c.Mode != cursorModeRank {
		return 0, time.Time{}, uuid.Nil, fmt.Errorf("cursor: default cursor used under tsv-rank ordering")
	}
	if c.ID == uuid.Nil {
		return 0, time.Time{}, uuid.Nil, fmt.Errorf("cursor: missing id")
	}
	return c.Rank, c.CreatedAt, c.ID, nil
}
