package mindat

import "testing"

// Internal test file: hasRadioactiveElement and isCarbonate are
// package-private helpers (mi-8pcs). The mapping rules are the
// contract; test them directly so a regression in the table or the
// parser is visible at the unit level, not just through the HTTP
// pipeline.

func TestHasRadioactiveElement(t *testing.T) {
	cases := []struct {
		name     string
		elements string
		want     bool
	}{
		{"empty", "", false},
		{"whitespace only", "   ", false},

		// Space-separated (Mindat's typical shape).
		{"uraninite — U O", "U O", true},
		{"autunite — Ca U P O H", "Ca U P O H", true},
		{"monazite — Ce La Nd Th P O", "Ce La Nd Th P O", true},

		// Comma-separated (defensive — Mindat has shipped both forms).
		{"comma-separated U", "K,Al,Si,O,U", true},
		{"comma+space mixed", "K, Al, Si, O, Th", true},

		// Negatives.
		{"microcline — K explicitly excluded", "K Al Si O", false},
		{"quartz", "Si O", false},
		{"calcite", "Ca C O", false},

		// Substring traps the regex must NOT trigger on.
		{"plutonium — Pu must not match U", "Pu O", false},
		{"thallium — Tl must not match Th", "Tl Cl", false},
		{"thorianite — Th does match Th", "Th O", true},

		// Lowercase tolerance (Mindat is canonical-case but be safe).
		{"lowercase u", "u o", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasRadioactiveElement(tc.elements); got != tc.want {
				t.Errorf("hasRadioactiveElement(%q) = %v, want %v", tc.elements, got, tc.want)
			}
		})
	}
}

func TestIsCarbonate(t *testing.T) {
	cases := []struct {
		name   string
		strunz string
		want   bool
	}{
		{"empty", "", false},

		// Class 5 — carbonates (and nitrates).
		{"calcite 5.AB.05", "5.AB.05", true},
		{"dolomite 5.AB.10", "5.AB.10", true},
		{"malachite 5.BA.10", "5.BA.10", true},
		{"zero-padded 05.AB.05", "05.AB.05", true},
		{"bare class 5", "5", true},

		// Other classes — must not fizz under this rule.
		{"pyrite — sulfide 02.EB.05a", "2.EB.05a", false},
		{"apatite — phosphate 8.BN.05", "8.BN.05", false},
		{"quartz — oxide 4.DA.05", "4.DA.05", false},
		{"feldspar — silicate 9.FA.30", "9.FA.30", false},
		{"borate 6.GA.05", "6.GA.05", false},

		// Garbage tolerance.
		{"non-numeric leading", "AB.05", false},
		{"whitespace padding", "  5.AB.05  ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCarbonate(tc.strunz); got != tc.want {
				t.Errorf("isCarbonate(%q) = %v, want %v", tc.strunz, got, tc.want)
			}
		})
	}
}
