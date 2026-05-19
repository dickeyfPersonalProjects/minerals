package main

import (
	"strings"
	"testing"
)

// TestParseBootstrapArgs covers the CLI surface: every required /
// rejected flag combination from bead mi-c1y. The bead's exit-code
// matrix delegates argument errors to exit 2 (guard tripped), so
// every error case here also corresponds to a refused command.
func TestParseBootstrapArgs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		args    []string
		want    bootstrapArgs
		wantErr string // substring; empty means success
	}{
		{
			name: "email + dry-run",
			args: []string{"--user-email", "me@example.com", "--dry-run"},
			want: bootstrapArgs{email: "me@example.com", dryRun: true},
		},
		{
			name: "sub + yes",
			args: []string{"--user-sub", "kc-1234", "--yes"},
			want: bootstrapArgs{sub: "kc-1234", confirm: true},
		},
		{
			name: "email alone — refused at write time but parses",
			args: []string{"--user-email", "me@example.com"},
			want: bootstrapArgs{email: "me@example.com"},
		},
		{
			name:    "missing both lookups",
			args:    []string{"--dry-run"},
			wantErr: "one of --user-email or --user-sub is required",
		},
		{
			name:    "both lookups",
			args:    []string{"--user-email", "me@example.com", "--user-sub", "kc-1"},
			wantErr: "mutually exclusive",
		},
		{
			name:    "dry-run + yes",
			args:    []string{"--user-email", "me@example.com", "--dry-run", "--yes"},
			wantErr: "mutually exclusive",
		},
		{
			name:    "unknown flag",
			args:    []string{"--user-email", "me@example.com", "--no-such-flag"},
			wantErr: "parse args",
		},
		{
			name:    "stray positional",
			args:    []string{"--user-email", "me@example.com", "extra"},
			wantErr: "unexpected positional argument",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseBootstrapArgs(tc.args)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("args = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestLookupKind exercises the tiny helper so the QueryRow error
// message always names the field the operator actually passed.
func TestLookupKind(t *testing.T) {
	t.Parallel()
	if got := lookupKind(bootstrapArgs{email: "x"}); got != "email" {
		t.Errorf("email arg → %q, want \"email\"", got)
	}
	if got := lookupKind(bootstrapArgs{sub: "x"}); got != "keycloak_sub" {
		t.Errorf("sub arg → %q, want \"keycloak_sub\"", got)
	}
}

// TestOrphanColumnsCoverage is a guard against drift between the
// command's table list and migration 0011's FK list. If a future
// migration adds a new ownership column FK'd to users(id), the
// command must learn about it — otherwise the V2 upgrade will leave
// orphan rows invisible to the new operator. This test enumerates
// the V1-era columns the bead lists; it's a sanity check, not a
// schema introspection.
func TestOrphanColumnsCoverage(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"specimens":       "author_id",
		"collectors":      "author_id",
		"journal_entries": "author_id",
		"files":           "uploaded_by",
		"mineral_species": "author_id",
		"qr_sheets":       "user_id",
	}
	got := make(map[string]string, len(orphanColumns))
	for _, oc := range orphanColumns {
		got[oc.table] = oc.column
	}
	if len(got) != len(want) {
		t.Fatalf("orphanColumns has %d entries, want %d", len(got), len(want))
	}
	for table, col := range want {
		if got[table] != col {
			t.Errorf("orphanColumns[%q] = %q, want %q", table, got[table], col)
		}
	}
}
