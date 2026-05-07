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
