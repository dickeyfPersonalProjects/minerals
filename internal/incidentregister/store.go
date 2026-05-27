package incidentregister

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// ErrNotFound is returned by GetByID when no entry has the given id.
var ErrNotFound = errors.New("incidentregister: entry not found")

// appendLockKey is the pg_advisory_xact_lock key the Create path holds
// for the duration of its transaction. It serializes appends so the
// seq counter and prev_hash read-then-write is atomic against concurrent
// filers — without it two appends could read the same tail and produce a
// duplicate seq / forked chain. The value is an arbitrary constant
// unique to this register's database.
const appendLockKey int64 = 0x4C32355F49524547 // "L25_IREG" in ASCII

// table is the single register table. It lives in the register's own
// database (INCIDENT_REGISTER_DATABASE_URL), bootstrapped by EnsureSchema
// — never via the app's migrations/.
const table = "confidentiality_incidents"

// Store is the append-only, tamper-evident register backed by its own
// Postgres pool. It exposes ONLY Create/GetByID/List/Export/Verify and
// EnsureSchema: there is intentionally NO Delete/Update/Purge/Drop/
// Truncate method, which is what makes the GDPR erasure flow (mi-nwg5)
// structurally unable to remove an entry through this package. A guard
// test asserts the absence of any destructive method by reflection.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore binds a Store to the register's dedicated pool. The pool MUST
// come from INCIDENT_REGISTER_DATABASE_URL (a separate database), not the
// application pool — that physical separation is the Law 25 isolation
// guarantee. Call EnsureSchema once after construction.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// EnsureSchema creates the register table if it does not already exist.
// It is deliberately NOT a migrations/ file: the application's migrate
// tool and its erasure SQL operate on a different database and must have
// no name for this table. Safe to call on every boot (CREATE TABLE IF
// NOT EXISTS is idempotent).
func (s *Store) EnsureSchema(ctx context.Context) error {
	const ddl = `
		CREATE TABLE IF NOT EXISTS ` + table + ` (
			seq                          BIGINT       PRIMARY KEY,
			id                           UUID         NOT NULL UNIQUE,
			personal_info_involved       TEXT         NOT NULL,
			circumstances                TEXT         NOT NULL,
			incident_occurred_at         TEXT         NOT NULL,
			became_aware_date            DATE         NOT NULL,
			people_affected              TEXT         NOT NULL,
			risk_assessment              TEXT         NOT NULL,
			cai_notified                 BOOLEAN      NOT NULL,
			cai_notified_detail          TEXT         NOT NULL,
			individuals_notified         BOOLEAN      NOT NULL,
			individuals_notified_detail  TEXT         NOT NULL,
			measures_taken               TEXT         NOT NULL,
			recorded_by                  TEXT         NOT NULL,
			recorded_at                  TIMESTAMPTZ  NOT NULL,
			retain_until                 DATE         NOT NULL,
			prev_hash                    TEXT         NOT NULL,
			entry_hash                   TEXT         NOT NULL UNIQUE
		)`
	if _, err := s.pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("incidentregister: ensure schema: %w", err)
	}
	return nil
}

const selectColumns = `
	seq, id, personal_info_involved, circumstances, incident_occurred_at,
	became_aware_date, people_affected, risk_assessment,
	cai_notified, cai_notified_detail,
	individuals_notified, individuals_notified_detail,
	measures_taken, recorded_by, recorded_at, retain_until,
	prev_hash, entry_hash`

// scanIncident reads one row in selectColumns order.
func scanIncident(row pgx.Row) (Incident, error) {
	var e Incident
	err := row.Scan(
		&e.Seq, &e.ID, &e.PersonalInfoInvolved, &e.Circumstances, &e.IncidentOccurredAt,
		&e.BecameAwareDate, &e.PeopleAffected, &e.RiskAssessment,
		&e.CAINotified, &e.CAINotifiedDetail,
		&e.IndividualsNotified, &e.IndividualsNotifiedDetail,
		&e.MeasuresTaken, &e.RecordedBy, &e.RecordedAt, &e.RetainUntil,
		&e.PrevHash, &e.EntryHash,
	)
	return e, err
}

// Create appends a new entry to the register and returns the persisted
// row (with its derived seq, hashes, recorded_at, and retain_until). The
// whole operation runs in one transaction holding an advisory lock so the
// "read tail → compute next seq/prev_hash → insert" sequence is atomic
// against concurrent filers. became_aware_date is normalized to a UTC
// date; retain_until is that date + 5 years.
func (s *Store) Create(ctx context.Context, in NewIncident) (Incident, error) {
	var out Incident
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", appendLockKey); err != nil {
			return fmt.Errorf("acquire append lock: %w", err)
		}

		var prevSeq int64
		var prevHash string
		row := tx.QueryRow(ctx,
			"SELECT seq, entry_hash FROM "+table+" ORDER BY seq DESC LIMIT 1")
		switch err := row.Scan(&prevSeq, &prevHash); {
		case errors.Is(err, pgx.ErrNoRows):
			prevSeq = 0
			prevHash = genesisHash
		case err != nil:
			return fmt.Errorf("read chain tail: %w", err)
		}

		aware := in.BecameAwareDate.UTC().Truncate(24 * time.Hour)
		e := Incident{
			ID:                        domain.NewID(),
			Seq:                       prevSeq + 1,
			PersonalInfoInvolved:      in.PersonalInfoInvolved,
			Circumstances:             in.Circumstances,
			IncidentOccurredAt:        in.IncidentOccurredAt,
			BecameAwareDate:           aware,
			PeopleAffected:            in.PeopleAffected,
			RiskAssessment:            in.RiskAssessment,
			CAINotified:               in.CAINotified,
			CAINotifiedDetail:         in.CAINotifiedDetail,
			IndividualsNotified:       in.IndividualsNotified,
			IndividualsNotifiedDetail: in.IndividualsNotifiedDetail,
			MeasuresTaken:             in.MeasuresTaken,
			RecordedBy:                in.RecordedBy,
			// Truncate to microseconds: Postgres timestamptz has
			// microsecond precision, so a nanosecond-precise value here
			// would hash differently than the value read back from the
			// DB, breaking Verify on the round-trip. We commit to the
			// stored precision up front.
			RecordedAt:  time.Now().UTC().Truncate(time.Microsecond),
			RetainUntil: retainUntil(aware),
			PrevHash:    prevHash,
		}
		e.EntryHash = computeHash(prevHash, e)

		const ins = `
			INSERT INTO ` + table + ` (
				seq, id, personal_info_involved, circumstances, incident_occurred_at,
				became_aware_date, people_affected, risk_assessment,
				cai_notified, cai_notified_detail,
				individuals_notified, individuals_notified_detail,
				measures_taken, recorded_by, recorded_at, retain_until,
				prev_hash, entry_hash
			) VALUES (
				$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18
			)`
		if _, err := tx.Exec(ctx, ins,
			e.Seq, e.ID, e.PersonalInfoInvolved, e.Circumstances, e.IncidentOccurredAt,
			e.BecameAwareDate, e.PeopleAffected, e.RiskAssessment,
			e.CAINotified, e.CAINotifiedDetail,
			e.IndividualsNotified, e.IndividualsNotifiedDetail,
			e.MeasuresTaken, e.RecordedBy, e.RecordedAt, e.RetainUntil,
			e.PrevHash, e.EntryHash,
		); err != nil {
			return fmt.Errorf("insert entry: %w", err)
		}
		out = e
		return nil
	})
	if err != nil {
		return Incident{}, fmt.Errorf("incidentregister: create: %w", err)
	}
	return out, nil
}

// GetByID returns the entry with the given id, or ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (Incident, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+selectColumns+" FROM "+table+" WHERE id = $1", id)
	e, err := scanIncident(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, fmt.Errorf("incidentregister: get by id: %w", err)
	}
	return e, nil
}

// List returns every entry ordered by ascending seq (chronological /
// chain order). The register is operator-scale (incidents are rare), so
// there is no pagination.
func (s *Store) List(ctx context.Context) ([]Incident, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+selectColumns+" FROM "+table+" ORDER BY seq ASC")
	if err != nil {
		return nil, fmt.Errorf("incidentregister: list: %w", err)
	}
	defer rows.Close()

	var out []Incident
	for rows.Next() {
		e, err := scanIncident(rows)
		if err != nil {
			return nil, fmt.Errorf("incidentregister: list scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("incidentregister: list rows: %w", err)
	}
	return out, nil
}

// Verify replays the hash chain over the whole register and reports the
// first inconsistency (or OK). It is the integrity proof the CAI can ask
// for and the regression signal if a row is ever tampered with directly
// in the database.
func (s *Store) Verify(ctx context.Context) (VerifyResult, error) {
	entries, err := s.List(ctx)
	if err != nil {
		return VerifyResult{}, err
	}
	return verifyChain(entries), nil
}

// Export returns the full register plus its integrity result — the
// CAI-requestable copy of the register.
func (s *Store) Export(ctx context.Context) (Export, error) {
	entries, err := s.List(ctx)
	if err != nil {
		return Export{}, err
	}
	return Export{Incidents: entries, Integrity: verifyChain(entries)}, nil
}
