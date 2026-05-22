package db

import "testing"

// TestMaxConnsFromEnv covers the DB_MAX_CONNS override parsing added
// for the mi-hkh6 incident mitigation. A bad value must report ok=false
// so NewPool falls through to the compiled default rather than ever
// shrinking the pool to an unusable size.
func TestMaxConnsFromEnv(t *testing.T) {
	cases := []struct {
		name   string
		set    bool
		val    string
		want   int32
		wantOK bool
	}{
		{name: "unset", set: false, wantOK: false},
		{name: "empty", set: true, val: "", wantOK: false},
		{name: "valid", set: true, val: "25", want: 25, wantOK: true},
		{name: "zero rejected", set: true, val: "0", wantOK: false},
		{name: "negative rejected", set: true, val: "-5", wantOK: false},
		{name: "non-numeric rejected", set: true, val: "lots", wantOK: false},
		{name: "overflow rejected", set: true, val: "9999999999", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(maxConnsEnvVar, tc.val)
			} else {
				// t.Setenv with empty differs from unset; ensure unset.
				t.Setenv(maxConnsEnvVar, "")
			}
			got, ok := maxConnsFromEnv()
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Fatalf("got = %d, want %d", got, tc.want)
			}
		})
	}
}
