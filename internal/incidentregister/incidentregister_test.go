package incidentregister

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// mkEntry builds a fully-linked entry at seq given the prior hash. It
// mirrors what Store.Create does so the chain tests don't need a DB.
func mkEntry(seq int64, prev string, becameAware time.Time) Incident {
	e := Incident{
		ID:                   uuid.New(),
		Seq:                  seq,
		PersonalInfoInvolved: "email addresses",
		Circumstances:        "laptop lost",
		IncidentOccurredAt:   "2026-01-02",
		BecameAwareDate:      becameAware.UTC().Truncate(24 * time.Hour),
		PeopleAffected:       "~40",
		RiskAssessment:       "No — encrypted at rest",
		CAINotified:          false,
		MeasuresTaken:        "rotated keys",
		RecordedBy:           "operator-1",
		RecordedAt:           time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC),
	}
	e.RetainUntil = retainUntil(e.BecameAwareDate)
	e.PrevHash = prev
	e.EntryHash = computeHash(prev, e)
	return e
}

// TestRetainUntil_FiveYears asserts the Law 25 retention deadline is
// exactly became_aware + 5 years.
func TestRetainUntil_FiveYears(t *testing.T) {
	t.Parallel()
	aware := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	got := retainUntil(aware)
	want := time.Date(2031, 5, 24, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("retainUntil(%v) = %v, want %v", aware, got, want)
	}
}

// TestVerifyChain_Intact confirms a well-formed chain verifies OK.
func TestVerifyChain_Intact(t *testing.T) {
	t.Parallel()
	aware := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e1 := mkEntry(1, genesisHash, aware)
	e2 := mkEntry(2, e1.EntryHash, aware)
	e3 := mkEntry(3, e2.EntryHash, aware)

	res := verifyChain([]Incident{e1, e2, e3})
	if !res.OK {
		t.Fatalf("verifyChain OK = false, want true: %+v", res)
	}
	if res.Count != 3 {
		t.Errorf("Count = %d, want 3", res.Count)
	}
}

// TestVerifyChain_EmptyIsOK — an empty register is trivially intact.
func TestVerifyChain_EmptyIsOK(t *testing.T) {
	t.Parallel()
	if res := verifyChain(nil); !res.OK || res.Count != 0 {
		t.Fatalf("empty verifyChain = %+v, want OK with Count 0", res)
	}
}

// TestVerifyChain_TamperedContent detects a mutated recorded field — the
// entry_hash no longer matches the recomputed hash.
func TestVerifyChain_TamperedContent(t *testing.T) {
	t.Parallel()
	aware := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e1 := mkEntry(1, genesisHash, aware)
	e2 := mkEntry(2, e1.EntryHash, aware)

	// Alter a field WITHOUT recomputing the stored hash — simulates a
	// direct row edit.
	e2.RiskAssessment = "Yes — falsified after the fact"

	res := verifyChain([]Incident{e1, e2})
	if res.OK {
		t.Fatal("verifyChain OK = true on tampered content, want false")
	}
	if res.BrokenAtSeq != 2 {
		t.Errorf("BrokenAtSeq = %d, want 2", res.BrokenAtSeq)
	}
	if !strings.Contains(res.Detail, "entry_hash mismatch") {
		t.Errorf("Detail = %q, want entry_hash mismatch", res.Detail)
	}
}

// TestVerifyChain_BrokenLink detects a severed prev_hash link (e.g. a
// deleted middle entry re-numbered to look contiguous).
func TestVerifyChain_BrokenLink(t *testing.T) {
	t.Parallel()
	aware := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e1 := mkEntry(1, genesisHash, aware)
	e2 := mkEntry(2, "not-the-real-prev-hash", aware)

	res := verifyChain([]Incident{e1, e2})
	if res.OK {
		t.Fatal("verifyChain OK = true on broken link, want false")
	}
	if res.BrokenAtSeq != 2 || !strings.Contains(res.Detail, "prev_hash") {
		t.Errorf("got %+v, want broken prev_hash at seq 2", res)
	}
}

// TestVerifyChain_NonContiguousSeq detects a gap in the seq sequence.
func TestVerifyChain_NonContiguousSeq(t *testing.T) {
	t.Parallel()
	aware := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e1 := mkEntry(1, genesisHash, aware)
	e3 := mkEntry(3, e1.EntryHash, aware) // seq jumps 1 -> 3

	res := verifyChain([]Incident{e1, e3})
	if res.OK {
		t.Fatal("verifyChain OK = true on seq gap, want false")
	}
	if !strings.Contains(res.Detail, "non-contiguous seq") {
		t.Errorf("Detail = %q, want non-contiguous seq", res.Detail)
	}
}

// destructiveMethodPattern lists the method-name fragments a mutation /
// deletion API would use. The register MUST expose none of them — that
// absence is the structural guarantee the GDPR erasure flow (mi-nwg5)
// cannot reach the register through this package.
var destructiveMethodPattern = []string{
	"delete", "remove", "purge", "drop", "truncate", "update", "set", "patch", "erase", "destroy",
}

// TestStore_NoDestructiveMethods is the load-bearing append-only guard:
// it reflects over *Store's method set and fails if any method name
// looks like a mutation or deletion. If a future change adds, say, a
// `DeleteExpired` method, this test breaks deliberately — removing an
// entry must be a conscious decision that revisits the Law 25 retention
// contract, not an incidental addition.
func TestStore_NoDestructiveMethods(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeOf(&Store{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := strings.ToLower(typ.Method(i).Name)
		for _, bad := range destructiveMethodPattern {
			if strings.Contains(name, bad) {
				t.Errorf("*Store exposes destructive-looking method %q (contains %q): "+
					"the confidentiality-incident register is append-only and MUST NOT "+
					"offer a way to mutate or delete entries (Law 25 retention; mi-2p6i / mi-nwg5)",
					typ.Method(i).Name, bad)
			}
		}
	}
}

// TestStore_ExposesExpectedMethods is the positive complement: the
// append-only API surface is exactly Create/GetByID/List/Export/Verify/
// EnsureSchema. Guards against an accidental rename that would silently
// drop a capability the handlers depend on.
func TestStore_ExposesExpectedMethods(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeOf(&Store{})
	want := []string{"Create", "GetByID", "List", "Export", "Verify", "EnsureSchema"}
	for _, m := range want {
		if _, ok := typ.MethodByName(m); !ok {
			t.Errorf("*Store is missing expected method %q", m)
		}
	}
	if typ.NumMethod() != len(want) {
		t.Errorf("*Store has %d methods, want exactly %d (%v) — a new method appeared; "+
			"confirm it is not destructive", typ.NumMethod(), len(want), want)
	}
}
