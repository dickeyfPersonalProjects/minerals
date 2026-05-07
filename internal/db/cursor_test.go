package db

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCursorRoundTrip(t *testing.T) {
	want := uuid.MustParse("01927b3c-1234-7abc-9def-000000000001")
	wantTS := time.Date(2026, 5, 7, 12, 30, 0, 0, time.UTC)

	enc := EncodeCursor(wantTS, want)
	if enc == "" {
		t.Fatal("empty cursor")
	}
	if strings.ContainsAny(enc, "+/=") {
		t.Errorf("cursor uses non-url-safe chars: %q", enc)
	}

	gotTS, gotID, err := DecodeCursor(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !gotTS.Equal(wantTS) {
		t.Errorf("ts: got %v want %v", gotTS, wantTS)
	}
	if gotID != want {
		t.Errorf("id: got %v want %v", gotID, want)
	}
}

func TestCursorEmpty(t *testing.T) {
	ts, id, err := DecodeCursor("")
	if err != nil {
		t.Fatalf("empty cursor errored: %v", err)
	}
	if !ts.IsZero() || id != uuid.Nil {
		t.Errorf("empty cursor produced non-zero values: ts=%v id=%v", ts, id)
	}
}

func TestCursorMalformed(t *testing.T) {
	cases := []string{
		"not-base64-!!",
		"YWJj",   // valid base64, not JSON
		"e30",    // "{}" — missing id
		"bnVsbA", // "null"
	}
	for _, s := range cases {
		if _, _, err := DecodeCursor(s); err == nil {
			t.Errorf("expected error for %q", s)
		}
	}
}

func TestRankCursorRoundTrip(t *testing.T) {
	wantID := uuid.MustParse("01927b3c-1234-7abc-9def-000000000002")
	wantTS := time.Date(2026, 5, 7, 12, 30, 0, 0, time.UTC)
	wantRank := float32(0.4321)

	enc := EncodeRankCursor(wantRank, wantTS, wantID)
	if enc == "" {
		t.Fatal("empty rank cursor")
	}
	if strings.ContainsAny(enc, "+/=") {
		t.Errorf("rank cursor uses non-url-safe chars: %q", enc)
	}

	gotRank, gotTS, gotID, err := DecodeRankCursor(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if gotRank != wantRank {
		t.Errorf("rank: got %v want %v", gotRank, wantRank)
	}
	if !gotTS.Equal(wantTS) {
		t.Errorf("ts: got %v want %v", gotTS, wantTS)
	}
	if gotID != wantID {
		t.Errorf("id: got %v want %v", gotID, wantID)
	}
}

func TestRankCursorRejectsDefaultCursor(t *testing.T) {
	// A default cursor (no `mode: rank`) MUST be rejected when fed
	// to DecodeRankCursor — the caller switched ordering and the
	// existing cursor is no longer valid (per §10).
	wantID := uuid.MustParse("01927b3c-1234-7abc-9def-000000000003")
	wantTS := time.Date(2026, 5, 7, 12, 30, 0, 0, time.UTC)
	enc := EncodeCursor(wantTS, wantID)
	if _, _, _, err := DecodeRankCursor(enc); err == nil {
		t.Error("expected error feeding default cursor to DecodeRankCursor")
	}
}

func TestDefaultCursorRejectsRankCursor(t *testing.T) {
	// And the inverse: a rank cursor (with `mode: rank`) must be
	// rejected when the caller asks for default-ordering decoding.
	wantID := uuid.MustParse("01927b3c-1234-7abc-9def-000000000004")
	wantTS := time.Date(2026, 5, 7, 12, 30, 0, 0, time.UTC)
	enc := EncodeRankCursor(0.5, wantTS, wantID)
	if _, _, err := DecodeCursor(enc); err == nil {
		t.Error("expected error feeding rank cursor to DecodeCursor")
	}
}

func TestRankCursorEmpty(t *testing.T) {
	rank, ts, id, err := DecodeRankCursor("")
	if err != nil {
		t.Fatalf("empty rank cursor errored: %v", err)
	}
	if rank != 0 || !ts.IsZero() || id != uuid.Nil {
		t.Errorf("empty rank cursor produced non-zero: rank=%v ts=%v id=%v", rank, ts, id)
	}
}
