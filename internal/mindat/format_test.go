package mindat_test

import (
	"testing"

	"github.com/dickeyfPersonalProjects/minerals/internal/mindat"
)

func TestNormalizeChemicalFormula(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "empty",
			in:   "",
			want: "",
		},
		{
			name: "no markup is passed through unchanged",
			in:   "SiO2",
			want: "SiO2",
		},
		{
			name: "already unicode is idempotent",
			in:   "Pb(UO₂)₃O₃(OH)₂ · 3H₂O",
			want: "Pb(UO₂)₃O₃(OH)₂ · 3H₂O",
		},
		{
			name: "single digit subscript",
			in:   "H<sub>2</sub>O",
			want: "H₂O",
		},
		{
			name: "all digit subscripts",
			in:   "X<sub>0</sub><sub>1</sub><sub>2</sub><sub>3</sub><sub>4</sub><sub>5</sub><sub>6</sub><sub>7</sub><sub>8</sub><sub>9</sub>",
			want: "X₀₁₂₃₄₅₆₇₈₉",
		},
		{
			name: "multi-digit subscript",
			in:   "C<sub>10</sub>H<sub>16</sub>",
			want: "C₁₀H₁₆",
		},
		{
			name: "all digit superscripts",
			in:   "X<sup>0</sup><sup>1</sup><sup>2</sup><sup>3</sup><sup>4</sup><sup>5</sup><sup>6</sup><sup>7</sup><sup>8</sup><sup>9</sup>",
			want: "X⁰¹²³⁴⁵⁶⁷⁸⁹",
		},
		{
			name: "charge superscripts",
			in:   "Fe<sup>2+</sup>",
			want: "Fe²⁺",
		},
		{
			name: "negative charge",
			in:   "O<sup>-</sup>",
			want: "O⁻",
		},
		{
			name: "variable n+ charge",
			in:   "M<sup>n+</sup>",
			want: "Mⁿ⁺",
		},
		{
			name: "middot entity",
			in:   "CuSO<sub>4</sub> &middot; 5H<sub>2</sub>O",
			want: "CuSO₄ · 5H₂O",
		},
		{
			name: "amp entity",
			in:   "A &amp; B",
			want: "A & B",
		},
		{
			name: "nbsp entity becomes space",
			in:   "A&nbsp;B",
			want: "A B",
		},
		{
			name: "minus entity",
			in:   "Cl&minus;",
			want: "Cl−",
		},
		{
			name: "full bead example",
			in:   "Pb(UO<sub>2</sub>)<sub>3</sub>O<sub>3</sub>(OH)<sub>2</sub> &middot; 3H<sub>2</sub>O",
			want: "Pb(UO₂)₃O₃(OH)₂ · 3H₂O",
		},
		{
			name: "uppercase sub tag",
			in:   "H<SUB>2</SUB>O",
			want: "H₂O",
		},
		{
			name: "uppercase sup tag",
			in:   "Fe<SUP>3+</SUP>",
			want: "Fe³⁺",
		},
		{
			name: "stray unknown tag is stripped (inner kept)",
			in:   "H<b>2</b>O",
			want: "H2O",
		},
		{
			name: "unmappable sub content falls through to tag strip",
			// inner 'x' has no subscript form — the sub tag survives
			// the dedicated pass, then the safety net strips it. Inner
			// text is preserved.
			in:   "X<sub>x</sub>",
			want: "Xx",
		},
		{
			name: "unknown entity passes through verbatim",
			in:   "A &copy; B",
			want: "A &copy; B",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mindat.NormalizeChemicalFormula(tc.in)
			if got != tc.want {
				t.Errorf("NormalizeChemicalFormula(%q):\n  got  %q\n  want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Idempotence: running the normalizer twice yields the same result as
// running it once. Important because we apply it at both Mindat
// ingestion AND user-write paths — a user posting an already-clean
// value should not have it mutated.
func TestNormalizeChemicalFormula_Idempotent(t *testing.T) {
	inputs := []string{
		"",
		"SiO2",
		"H<sub>2</sub>O",
		"Pb(UO<sub>2</sub>)<sub>3</sub>O<sub>3</sub>(OH)<sub>2</sub> &middot; 3H<sub>2</sub>O",
		"Fe<sup>2+</sup>",
		"M<sup>n+</sup>",
	}
	for _, in := range inputs {
		first := mindat.NormalizeChemicalFormula(in)
		second := mindat.NormalizeChemicalFormula(first)
		if first != second {
			t.Errorf("not idempotent for %q: first=%q second=%q", in, first, second)
		}
	}
}
