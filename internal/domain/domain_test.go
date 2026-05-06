package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewID_ReturnsUUIDv7(t *testing.T) {
	id := NewID()
	if id == uuid.Nil {
		t.Fatal("NewID returned the nil UUID")
	}
	if got := id.Version(); got != 7 {
		t.Fatalf("NewID returned UUID v%d, want v7", got)
	}
}

func TestNewID_ProducesDistinctValues(t *testing.T) {
	const n = 32
	seen := make(map[uuid.UUID]struct{}, n)
	for i := 0; i < n; i++ {
		id := NewID()
		if _, dup := seen[id]; dup {
			t.Fatalf("NewID returned a duplicate at iter %d: %s", i, id)
		}
		seen[id] = struct{}{}
	}
}
