package db

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSpecimenCursor_DefaultRoundTrip(t *testing.T) {
	now := time.Date(2026, 5, 7, 14, 0, 0, 0, time.UTC)
	id := uuid.MustParse("01906f70-2ba8-7000-8000-000000000001")
	enc, err := encodeCursor(specimenCursor{CreatedAt: &now, ID: id})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if enc == "" {
		t.Fatal("expected non-empty cursor")
	}
	got, err := decodeCursor(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != id {
		t.Errorf("id round-trip: got %s, want %s", got.ID, id)
	}
	if got.CreatedAt == nil || !got.CreatedAt.Equal(now) {
		t.Errorf("created_at round-trip: got %v, want %v", got.CreatedAt, now)
	}
	if got.Rank != nil {
		t.Errorf("rank should be nil for default cursor, got %v", *got.Rank)
	}
	if got.isRank() {
		t.Error("default cursor should not isRank")
	}
}

func TestSpecimenCursor_RankRoundTrip(t *testing.T) {
	rank := 0.418
	id := uuid.MustParse("01906f70-2ba8-7000-8000-000000000002")
	enc, err := encodeCursor(specimenCursor{Rank: &rank, ID: id})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := decodeCursor(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != id {
		t.Errorf("id mismatch")
	}
	if got.Rank == nil || *got.Rank != rank {
		t.Errorf("rank mismatch: got %v, want %v", got.Rank, rank)
	}
	if !got.isRank() {
		t.Error("rank cursor should isRank")
	}
	if got.CreatedAt != nil {
		t.Errorf("created_at should be nil for rank cursor, got %v", got.CreatedAt)
	}
}

func TestSpecimenCursor_EmptyEncodesEmpty(t *testing.T) {
	enc, err := encodeCursor(specimenCursor{})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if enc != "" {
		t.Fatalf("expected empty string for zero cursor, got %q", enc)
	}
	got, err := decodeCursor("")
	if err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	if got.ID != uuid.Nil || got.CreatedAt != nil || got.Rank != nil {
		t.Errorf("decoded empty cursor not zero: %+v", got)
	}
}

func TestSpecimenCursor_RejectsMalformed(t *testing.T) {
	if _, err := decodeCursor("not-base64!"); err == nil {
		t.Error("expected error for non-base64 cursor")
	}
	if _, err := decodeCursor("Zm9vYmFy"); err == nil {
		t.Error("expected error for non-JSON cursor")
	}
	// Valid base64+JSON but missing id.
	enc, _ := encodeCursor(specimenCursor{ID: uuid.MustParse("01906f70-2ba8-7000-8000-000000000003")})
	_ = enc
	// Manually construct a payload missing id.
	if _, err := decodeCursor("eyJjIjoiMjAyNi0wNS0wN1QwMDowMDowMFoifQ"); err == nil {
		t.Error("expected error when id missing")
	}
}
