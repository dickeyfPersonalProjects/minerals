package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/config"
	"github.com/dickeyfPersonalProjects/minerals/internal/db"
)

// Exit codes for `minerals bootstrap-claim-orphans` (bead mi-c1y).
const (
	bootstrapExitOK           = 0 // success (writes performed) or dry-run completed
	bootstrapExitUserNotFound = 1 // resolved user does not exist in users table
	bootstrapExitGuardTripped = 2 // safety guard refused (bad args, pending status, multiple users, missing --yes)
	bootstrapExitDBError      = 3 // database error before / during the transaction
)

// orphanColumns enumerates every (table, column) pair the bootstrap
// command reassigns from the v1 stub-overseer UUID to the new V2
// user. The set mirrors migration 0011's FK list; tables without an
// ownership column (photos — inherit via specimen; specimen_collectors
// / journal_entry_files — link rows; shares / auth.sessions — V2
// artifacts that hold no V1 rows) are deliberately excluded. The
// preview, the UPDATE loop, and the integration test all read from
// here so divergence is impossible.
var orphanColumns = []struct {
	table  string
	column string
}{
	{"specimens", "author_id"},
	{"collectors", "author_id"},
	{"journal_entries", "author_id"},
	{"files", "uploaded_by"},
	{"mineral_species", "author_id"},
	{"qr_sheets", "user_id"},
}

// bootstrapClaimOrphansHelp is the help text printed when --help is
// passed or when args are malformed. Documents the bead's NULL-vs-
// stub-overseer reality and the safety guards (see bead mi-c1y).
const bootstrapClaimOrphansHelp = `usage: minerals bootstrap-claim-orphans (--user-email <addr> | --user-sub <sub>) [--dry-run | --yes]

One-shot V1 → V2 upgrade tool. Reassigns every row currently owned by the
v1 stub-overseer identity (sub=` + auth.StubUserSub + `) to the resolved
real user, so the SPA's per-author filters surface them after first login.

Flags:
  --user-email <addr>   look up target user by users.email
  --user-sub <sub>      alternative: look up by users.keycloak_sub
  --dry-run             print per-table plan, perform no writes
  --yes                 required to perform writes (refuses otherwise)

Exit codes:
  0  success (writes performed) or dry-run completed
  1  user not found
  2  safety guard tripped (pending user, multiple active users, missing --yes, bad args)
  3  database error

Safety guards (ALL must pass before any write):
  - target user must exist AND have status='active' (not 'pending')
  - target user must NOT be the stub-overseer sentinel
  - the users table must contain exactly ONE active non-stub user
    (the V2-launch personal-app reality; for multi-user reassignment
    use SQL directly — this is intentionally a one-shot operator tool)

Idempotency: a second run after a successful claim finds zero
orphans and exits 0 with a zero-row summary.`

// bootstrapArgs is the parsed CLI surface — the only inputs the
// command takes. Validated by parseBootstrapArgs.
type bootstrapArgs struct {
	email   string
	sub     string
	dryRun  bool
	confirm bool // --yes
}

// parseBootstrapArgs validates the flag combinations the bead allows.
// Argument errors map to exit 2 (guard) because they cause the
// command to refuse rather than to fail.
func parseBootstrapArgs(args []string) (bootstrapArgs, error) {
	var a bootstrapArgs
	fs := flag.NewFlagSet("bootstrap-claim-orphans", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&a.email, "user-email", "", "look up the target user by users.email")
	fs.StringVar(&a.sub, "user-sub", "", "look up the target user by users.keycloak_sub")
	fs.BoolVar(&a.dryRun, "dry-run", false, "print the plan without performing any writes")
	fs.BoolVar(&a.confirm, "yes", false, "required to perform writes; without it the command prints the plan and refuses")
	if err := fs.Parse(args); err != nil {
		return bootstrapArgs{}, fmt.Errorf("parse args: %w", err)
	}
	if fs.NArg() > 0 {
		return bootstrapArgs{}, fmt.Errorf("unexpected positional argument %q", fs.Arg(0))
	}
	if a.email == "" && a.sub == "" {
		return bootstrapArgs{}, errors.New("one of --user-email or --user-sub is required")
	}
	if a.email != "" && a.sub != "" {
		return bootstrapArgs{}, errors.New("--user-email and --user-sub are mutually exclusive")
	}
	if a.dryRun && a.confirm {
		return bootstrapArgs{}, errors.New("--dry-run and --yes are mutually exclusive")
	}
	return a, nil
}

// runBootstrapClaimOrphans is the main.go dispatcher entry. It owns
// the bead's exit-code matrix: on any non-zero return from
// bootstrapClaimOrphansMain it calls os.Exit directly so main's
// generic err→exit-1 path is bypassed. Returning nil here lets main
// drop through to a normal exit 0.
func runBootstrapClaimOrphans(args []string) error {
	// Show help and exit 0 when the operator just asked for usage —
	// kubectl exec ergonomics. (-h / --help are flag.ContinueOnError
	// territory; intercepting them here is the cleanest path.)
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			fmt.Println(bootstrapClaimOrphansHelp)
			return nil
		}
	}
	code, err := bootstrapClaimOrphansMain(context.Background(), os.Stdout, args)
	if err != nil {
		slog.Error("bootstrap-claim-orphans failed", "err", err, "exit_code", code)
	}
	if code == bootstrapExitOK {
		return nil
	}
	os.Exit(code)
	return nil // unreachable
}

// bootstrapClaimOrphansMain is the testable entry. It owns config
// loading + pool lifecycle so unit tests of the guard logic can call
// the inner helper (bootstrapClaimOrphansWithPool) directly.
func bootstrapClaimOrphansMain(ctx context.Context, out io.Writer, args []string) (int, error) {
	a, err := parseBootstrapArgs(args)
	if err != nil {
		// Always show usage on parse failure — the operator typically
		// runs this exactly once during an upgrade and the help text
		// is the contract.
		_, _ = fmt.Fprintln(out, bootstrapClaimOrphansHelp)
		return bootstrapExitGuardTripped, fmt.Errorf("bootstrap-claim-orphans: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return bootstrapExitDBError, fmt.Errorf("bootstrap-claim-orphans: load config: %w", err)
	}
	if err := cfg.ValidateForMigrate(); err != nil {
		return bootstrapExitDBError, fmt.Errorf("bootstrap-claim-orphans: %w", err)
	}
	configureLogger(cfg.LogLevel)

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return bootstrapExitDBError, fmt.Errorf("bootstrap-claim-orphans: open pool: %w", err)
	}
	defer pool.Close()

	return bootstrapClaimOrphansWithPool(ctx, out, pool, a)
}

// targetUser is the resolved row the safety guards validate against,
// kept as a tiny value type so the post-validate helpers don't need
// to re-query.
type targetUser struct {
	ID    uuid.UUID
	Email string
	Sub   string
}

// bootstrapClaimOrphansWithPool is the heart of the command — guard
// checks, dry-run preview, transactional UPDATE. Separated from
// bootstrapClaimOrphansMain so an integration test can drive it
// against a per-test schema without poking process-level config.
func bootstrapClaimOrphansWithPool(
	ctx context.Context, out io.Writer, pool *pgxpool.Pool, a bootstrapArgs,
) (int, error) {
	target, code, err := resolveAndValidate(ctx, pool, a)
	if err != nil {
		return code, err
	}

	plan, err := previewClaim(ctx, pool)
	if err != nil {
		return bootstrapExitDBError, fmt.Errorf("preview: %w", err)
	}
	printPlan(out, target, plan, a.dryRun)

	if a.dryRun {
		return bootstrapExitOK, nil
	}
	if !a.confirm {
		_, _ = fmt.Fprintln(out, "\nNo writes performed (pass --yes to apply, or --dry-run to silence this notice).")
		return bootstrapExitGuardTripped, errors.New("bootstrap-claim-orphans: refusing to write without --yes")
	}

	results, err := claimOrphansTx(ctx, pool, target.ID)
	if err != nil {
		return bootstrapExitDBError, fmt.Errorf("transaction: %w", err)
	}
	printResults(out, target, results)
	return bootstrapExitOK, nil
}

// resolveAndValidate runs every safety guard the bead requires before
// any UPDATE is issued. It returns the validated target user on
// success, or (zero-value target, exit code, error) on the first
// guard failure — callers map the code straight into os.Exit.
func resolveAndValidate(ctx context.Context, pool *pgxpool.Pool, a bootstrapArgs) (targetUser, int, error) {
	var (
		target targetUser
		status string
		q      string
		arg    string
	)
	if a.email != "" {
		q = `SELECT id, email, keycloak_sub, status FROM users WHERE email = $1`
		arg = a.email
	} else {
		q = `SELECT id, email, keycloak_sub, status FROM users WHERE keycloak_sub = $1`
		arg = a.sub
	}
	if err := pool.QueryRow(ctx, q, arg).Scan(&target.ID, &target.Email, &target.Sub, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return targetUser{}, bootstrapExitUserNotFound,
				fmt.Errorf("user not found (looked up %s=%q)", lookupKind(a), arg)
		}
		return targetUser{}, bootstrapExitDBError, fmt.Errorf("user lookup: %w", err)
	}

	if status != "active" {
		return targetUser{}, bootstrapExitGuardTripped,
			fmt.Errorf("safety: target user %s is status=%q, expected 'active' (complete profile setup first)",
				target.Email, status)
	}
	if target.Sub == auth.StubUserSub {
		return targetUser{}, bootstrapExitGuardTripped,
			fmt.Errorf("safety: target user is the stub-overseer sentinel (sub=%q); pick a real user", target.Sub)
	}

	var realActive int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE status = 'active' AND keycloak_sub <> $1`,
		auth.StubUserSub,
	).Scan(&realActive); err != nil {
		return targetUser{}, bootstrapExitDBError, fmt.Errorf("user count: %w", err)
	}
	if realActive != 1 {
		return targetUser{}, bootstrapExitGuardTripped,
			fmt.Errorf("safety: expected exactly 1 active non-stub user, got %d — use SQL directly for multi-user reassignment", realActive)
	}

	return target, bootstrapExitOK, nil
}

func lookupKind(a bootstrapArgs) string {
	if a.email != "" {
		return "email"
	}
	return "keycloak_sub"
}

// previewClaim returns the row count, per orphanColumns entry, that
// currently belongs to the stub-overseer. The transactional UPDATE
// re-counts via RowsAffected — these numbers are an operator-facing
// preview only and don't gate writes.
func previewClaim(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	out := make([]int64, len(orphanColumns))
	stub := auth.StubUser.ID
	for i, oc := range orphanColumns {
		// Identifiers are compile-time literals from orphanColumns —
		// no injection surface.
		q := fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s = $1`, oc.table, oc.column)
		if err := pool.QueryRow(ctx, q, stub).Scan(&out[i]); err != nil {
			return nil, fmt.Errorf("%s.%s: %w", oc.table, oc.column, err)
		}
	}
	return out, nil
}

// claimOrphansTx runs the bead's transactional UPDATE: every orphan
// table moves stub-overseer rows to newID atomically. Per-table
// RowsAffected counts return in orphanColumns order.
func claimOrphansTx(ctx context.Context, pool *pgxpool.Pool, newID uuid.UUID) ([]int64, error) {
	results := make([]int64, len(orphanColumns))
	stub := auth.StubUser.ID
	err := db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
		for i, oc := range orphanColumns {
			q := fmt.Sprintf(`UPDATE %s SET %s = $1 WHERE %s = $2`, oc.table, oc.column, oc.column)
			tag, err := tx.Exec(ctx, q, newID, stub)
			if err != nil {
				return fmt.Errorf("update %s.%s: %w", oc.table, oc.column, err)
			}
			results[i] = tag.RowsAffected()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func printPlan(out io.Writer, t targetUser, counts []int64, dryRun bool) {
	header := "bootstrap-claim-orphans plan"
	if dryRun {
		header += " (dry-run)"
	}
	_, _ = fmt.Fprintln(out, header)
	_, _ = fmt.Fprintf(out, "  target user: %s (id=%s sub=%s)\n", t.Email, t.ID, t.Sub)
	_, _ = fmt.Fprintf(out, "  reassigning rows from stub-overseer (%s)\n\n", auth.StubUser.ID)
	var total int64
	for i, oc := range orphanColumns {
		_, _ = fmt.Fprintf(out, "  %-30s %d row(s)\n", oc.table+"."+oc.column, counts[i])
		total += counts[i]
	}
	_, _ = fmt.Fprintf(out, "  %-30s %d row(s)\n", "TOTAL", total)
}

func printResults(out io.Writer, t targetUser, counts []int64) {
	_, _ = fmt.Fprintln(out)
	var total int64
	for i, oc := range orphanColumns {
		_, _ = fmt.Fprintf(out, "Claimed %d %s\n", counts[i], oc.table)
		total += counts[i]
	}
	_, _ = fmt.Fprintf(out, "Total: %d rows reassigned to %s (sub=%s)\n", total, t.Email, t.Sub)
}
