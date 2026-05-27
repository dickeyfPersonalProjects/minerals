// Package incidentregister implements the Quebec Law 25
// confidentiality-incident register (mi-2p6i). The register records
// every confidentiality incident affecting personal information held
// by the Service — per the Privacy Officer's reference at
// ~/minerals-legal/confidentiality-incident-register.md and the
// *Regulation respecting confidentiality incidents* (Law 25) +
// GDPR Art. 33(5).
//
// # Why a separate package with its own DDL
//
// Law 25 requires the register survive independently of ordinary
// application data lifecycle — in particular it must NOT be reachable
// by the GDPR/Law 25 right-to-erasure flow (mi-nwg5), even though its
// entries reference affected individuals. Two structural guarantees
// enforce that:
//
//   - SEPARATE DATABASE. The Store runs against its own *pgxpool.Pool
//     opened from INCIDENT_REGISTER_DATABASE_URL (see cmd/minerals).
//     The app's migrations/ and erasure SQL operate on a different
//     database and can never name this table. The Store bootstraps its
//     OWN schema via EnsureSchema (CREATE TABLE IF NOT EXISTS) —
//     deliberately NOT a migrations/ file — so the app's migrate tool
//     has no knowledge of it.
//
//   - APPEND-ONLY / TAMPER-EVIDENT. The Store exposes ONLY
//     Create/GetByID/List/Export/Verify. There is NO Delete, Update,
//     Purge, Drop, or Truncate method — so neither the erasure flow nor
//     any other caller can remove an entry through this package. A
//     reflection guard test asserts that invariant. Each entry carries
//     a sha256 hash chain (entry_hash = sha256(prev_hash || canonical
//     fields)); Verify recomputes the chain and reports any break.
//
//   - RETENTION. retain_until = became_aware_date + 5 years is recorded
//     per entry. Because there is no hard-delete API at all, the >=5yr
//     retention rule is enforced structurally rather than by a guard.
package incidentregister

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// retentionYears is the Law 25 minimum retention for a register entry,
// counted from the date the operator became aware of the incident.
const retentionYears = 5

// genesisHash is the prev_hash of the first entry in the chain. A fixed,
// non-empty sentinel makes "first entry" explicit and keeps the hash
// input shape uniform for every row.
const genesisHash = "GENESIS"

// NewIncident is the operator-supplied content of a register entry: the
// 8 Law 25 fields plus the recording operator. The Store derives
// everything else (seq, hashes, retain_until, recorded_at, id) — those
// are not caller-settable, which is part of what makes the register
// tamper-evident.
type NewIncident struct {
	// PersonalInfoInvolved — what data was involved (email, images,
	// collection data, etc.; or why it can't be described). [field 1]
	PersonalInfoInvolved string
	// Circumstances — what happened and how. [field 2]
	Circumstances string
	// IncidentOccurredAt — date/period the incident occurred, free text
	// because it is often approximate or unknown. [field 3]
	IncidentOccurredAt string
	// BecameAwareDate — the date the operator became aware. Drives
	// retention (retain_until = this + 5y). [field 4]
	BecameAwareDate time.Time
	// PeopleAffected — number (or best estimate) of affected
	// individuals, free text. [field 5]
	PeopleAffected string
	// RiskAssessment — risk of serious injury (Yes/No) plus the factors
	// considered: sensitivity, anticipated consequences, likelihood of
	// misuse. [field 6]
	RiskAssessment string
	// CAINotified / CAINotifiedDetail — whether the Commission d'accès à
	// l'information was notified, and the date or the reason it was not
	// (when notification was required). [field 7a]
	CAINotified       bool
	CAINotifiedDetail string
	// IndividualsNotified / IndividualsNotifiedDetail — whether affected
	// individuals were notified, and the date/method or the reason they
	// were not. [field 7b]
	IndividualsNotified       bool
	IndividualsNotifiedDetail string
	// MeasuresTaken — steps to reduce risk of injury and prevent
	// recurrence. [field 8]
	MeasuresTaken string
	// RecordedBy — identity of the operator filing the entry (the
	// authenticated admin's user id / email). Recorded for accountability
	// per the "Entry recorded by" line in the legal template.
	RecordedBy string
}

// Incident is a persisted register entry: the operator content plus the
// Store-derived metadata. It is append-only — once written, no Store
// method mutates or removes it.
type Incident struct {
	ID  uuid.UUID `json:"id"`
	Seq int64     `json:"seq"`

	PersonalInfoInvolved      string    `json:"personal_info_involved"`
	Circumstances             string    `json:"circumstances"`
	IncidentOccurredAt        string    `json:"incident_occurred_at"`
	BecameAwareDate           time.Time `json:"became_aware_date"`
	PeopleAffected            string    `json:"people_affected"`
	RiskAssessment            string    `json:"risk_assessment"`
	CAINotified               bool      `json:"cai_notified"`
	CAINotifiedDetail         string    `json:"cai_notified_detail"`
	IndividualsNotified       bool      `json:"individuals_notified"`
	IndividualsNotifiedDetail string    `json:"individuals_notified_detail"`
	MeasuresTaken             string    `json:"measures_taken"`

	RecordedBy  string    `json:"recorded_by"`
	RecordedAt  time.Time `json:"recorded_at"`
	RetainUntil time.Time `json:"retain_until"`

	// PrevHash links to the previous entry's EntryHash (genesisHash for
	// the first). EntryHash = sha256(PrevHash || canonical fields).
	PrevHash  string `json:"prev_hash"`
	EntryHash string `json:"entry_hash"`
}

// retainUntil returns the Law 25 retention deadline for an entry whose
// operator became aware on awareDate: awareDate + 5 years (UTC date).
func retainUntil(awareDate time.Time) time.Time {
	return awareDate.UTC().AddDate(retentionYears, 0, 0)
}

// canonicalBytes is the deterministic serialization of the entry's
// content + immutable metadata that feeds the hash chain. Field order
// and the length-prefixed framing are fixed: any change to a recorded
// field (or to seq / recorded_by / recorded_at / retain_until) changes
// the canonical bytes and therefore breaks Verify. The length prefix
// (\x1f-separated "len:value") makes the encoding injective so two
// different field splits can't collide.
func (in Incident) canonicalBytes() []byte {
	var b strings.Builder
	write := func(s string) {
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteByte(':')
		b.WriteString(s)
		b.WriteByte('\x1f')
	}
	write(strconv.FormatInt(in.Seq, 10))
	write(in.ID.String())
	write(in.PersonalInfoInvolved)
	write(in.Circumstances)
	write(in.IncidentOccurredAt)
	write(in.BecameAwareDate.UTC().Format(time.RFC3339Nano))
	write(in.PeopleAffected)
	write(in.RiskAssessment)
	write(strconv.FormatBool(in.CAINotified))
	write(in.CAINotifiedDetail)
	write(strconv.FormatBool(in.IndividualsNotified))
	write(in.IndividualsNotifiedDetail)
	write(in.MeasuresTaken)
	write(in.RecordedBy)
	write(in.RecordedAt.UTC().Format(time.RFC3339Nano))
	write(in.RetainUntil.UTC().Format(time.RFC3339Nano))
	return []byte(b.String())
}

// computeHash returns the entry_hash for in given the preceding entry's
// hash: sha256(prevHash || canonical fields), hex-encoded.
func computeHash(prevHash string, in Incident) string {
	h := sha256.New()
	h.Write([]byte(prevHash))
	h.Write(in.canonicalBytes())
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyResult is the outcome of replaying the hash chain over a set of
// entries (see Store.Verify / Store.Export).
type VerifyResult struct {
	// OK is true when every entry's hash links correctly and the seq
	// sequence is contiguous from 1.
	OK bool `json:"ok"`
	// Count is the number of entries checked.
	Count int `json:"count"`
	// BrokenAtSeq is the seq of the first entry that failed
	// verification, or 0 when OK.
	BrokenAtSeq int64 `json:"broken_at_seq,omitempty"`
	// Detail is a human-readable description of the first failure, empty
	// when OK.
	Detail string `json:"detail,omitempty"`
}

// verifyChain replays the hash chain over entries (which MUST be ordered
// by ascending seq) and reports the first inconsistency. It checks three
// things per entry: seq is contiguous from 1, prev_hash links to the
// prior entry's entry_hash (genesisHash for the first), and entry_hash
// equals the recomputed hash.
func verifyChain(entries []Incident) VerifyResult {
	prev := genesisHash
	for i, e := range entries {
		wantSeq := int64(i + 1)
		if e.Seq != wantSeq {
			return VerifyResult{OK: false, Count: len(entries), BrokenAtSeq: e.Seq,
				Detail: "non-contiguous seq: got " + strconv.FormatInt(e.Seq, 10) +
					", expected " + strconv.FormatInt(wantSeq, 10)}
		}
		if e.PrevHash != prev {
			return VerifyResult{OK: false, Count: len(entries), BrokenAtSeq: e.Seq,
				Detail: "prev_hash does not link to the preceding entry"}
		}
		if got := computeHash(prev, e); got != e.EntryHash {
			return VerifyResult{OK: false, Count: len(entries), BrokenAtSeq: e.Seq,
				Detail: "entry_hash mismatch: record has been altered"}
		}
		prev = e.EntryHash
	}
	return VerifyResult{OK: true, Count: len(entries)}
}

// Export is the full register dump returned by Store.Export: every entry
// plus the integrity check over them. It is the CAI-requestable copy of
// the register.
type Export struct {
	Incidents []Incident   `json:"incidents"`
	Integrity VerifyResult `json:"integrity"`
}
