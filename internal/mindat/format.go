package mindat

import (
	"log/slog"
	"regexp"
	"strings"
)

// NormalizeChemicalFormula converts an HTML-flavored chemical formula
// (as Mindat returns it) into clean Unicode text. The conversions:
//
//   - <sub>0..9</sub>               → ₀ … ₉   (U+2080–U+2089)
//   - <sup>0..9</sup>               → ⁰ … ⁹
//   - <sup>+</sup>, <sup>-</sup>    → ⁺ ⁻
//   - <sup>n+</sup>                 → ⁿ⁺
//   - &middot;                      → · (U+00B7)
//   - &amp;                         → &
//   - &nbsp;                        → (space, U+0020)
//   - &minus;                       → − (U+2212)
//
// Multi-character <sub>/<sup> content is translated character-by-
// character so multi-digit subscripts (e.g. <sub>10</sub> → ₁₀) round-
// trip correctly. Any sub/sup whose inner content can't be mapped
// (and any other HTML tag) is treated by the safety-net pass at the
// end: the tag is stripped but its inner text is kept. Unknown HTML
// entities (anything starting with `&` we don't recognize) are passed
// through verbatim and logged via slog so we notice new Mindat
// encodings rather than silently mangling them.
//
// The function is idempotent: an already-clean Unicode formula is
// returned unchanged. Callers should apply this at every
// chemical_formula write boundary (Mindat ingestion AND user input)
// so the column is uniformly Unicode at rest.
func NormalizeChemicalFormula(s string) string {
	if s == "" {
		return s
	}

	// Fast path: no markup or entities to normalize.
	if !strings.ContainsAny(s, "<&") {
		return s
	}

	out := s

	// Sub/sup with character-by-character translation. Anything that
	// fails to map keeps the original tag for the safety-net pass.
	out = subTagRe.ReplaceAllStringFunc(out, func(match string) string {
		inner := subTagRe.FindStringSubmatch(match)[1]
		if mapped, ok := mapRunes(inner, subscriptRune); ok {
			return mapped
		}
		return match
	})
	out = supTagRe.ReplaceAllStringFunc(out, func(match string) string {
		inner := supTagRe.FindStringSubmatch(match)[1]
		if mapped, ok := mapRunes(inner, superscriptRune); ok {
			return mapped
		}
		return match
	})

	// Known entities.
	for raw, repl := range knownEntities {
		out = strings.ReplaceAll(out, raw, repl)
	}

	// Log any remaining entities so we notice new Mindat encodings.
	// We do NOT decode them — silently guessing at unknown encodings
	// risks corrupting data we'd rather leave intact.
	if remaining := unknownEntityRe.FindAllString(out, -1); len(remaining) > 0 {
		slog.Warn("mindat: unknown HTML entities in chemical_formula",
			"entities", remaining, "input", s)
	}

	// Safety net: strip any remaining HTML tags (their inner text is
	// preserved). This catches surprises like <b>...</b> or sub/sup
	// content we couldn't map; in either case the resulting plain text
	// is a strict improvement over leaving raw markup in the column.
	out = anyTagRe.ReplaceAllString(out, "")

	return out
}

var (
	subTagRe        = regexp.MustCompile(`(?i)<sub>([^<]*)</sub>`)
	supTagRe        = regexp.MustCompile(`(?i)<sup>([^<]*)</sup>`)
	anyTagRe        = regexp.MustCompile(`<[^>]*>`)
	unknownEntityRe = regexp.MustCompile(`&[a-zA-Z][a-zA-Z0-9]*;|&#[0-9]+;|&#x[0-9a-fA-F]+;`)
)

// knownEntities lists every HTML entity NormalizeChemicalFormula
// decodes. Anything outside this map is logged and passed through.
var knownEntities = map[string]string{
	"&middot;": "·",
	"&amp;":    "&",
	"&nbsp;":   " ",
	"&minus;":  "−",
}

// subscriptRune returns the Unicode subscript form of r, or (0, false)
// if r has no subscript equivalent.
func subscriptRune(r rune) (rune, bool) {
	if r >= '0' && r <= '9' {
		return '₀' + (r - '0'), true
	}
	return 0, false
}

// superscriptRune returns the Unicode superscript form of r, or
// (0, false) if r has no superscript equivalent. The non-contiguous
// codepoints for ¹/²/³ (inherited from Latin-1) are special-cased.
func superscriptRune(r rune) (rune, bool) {
	switch r {
	case '0':
		return '⁰', true
	case '1':
		return '¹', true
	case '2':
		return '²', true
	case '3':
		return '³', true
	case '4', '5', '6', '7', '8', '9':
		return '⁴' + (r - '4'), true
	case '+':
		return '⁺', true
	case '-':
		return '⁻', true
	case 'n':
		return 'ⁿ', true
	}
	return 0, false
}

// hasRadioactiveElement reports whether the Mindat `elements` field
// contains uranium or thorium — the rule for deriving the Radioactive
// boolean (mi-8pcs). Mindat returns `elements` as a whitespace- and/or
// comma-separated list of element symbols (e.g. "U O", "K,Al,Si,O,H").
// We split, trim, and exact-match against U/Th — never substring —
// so e.g. "Pu" does not falsely match "U".
//
// What this catches: uraninite, autunite, torbernite, monazite, and
// the rest of the field-collectible radioactives.
//
// What this deliberately misses:
//   - K-40 radioactivity (orthoclase, microcline). Real but not field-
//     meaningful, and most users would be surprised to find feldspar
//     ticked. Potassium is excluded by design.
//   - Trace U/Th in otherwise non-radioactive species (e.g. smoky
//     quartz with trace U). Mindat's `elements` reflects the species
//     formula, not specific specimens, so trace contributions never
//     appear here anyway.
func hasRadioactiveElement(elements string) bool {
	if elements == "" {
		return false
	}
	for _, tok := range elementSplitRe.Split(elements, -1) {
		switch strings.ToUpper(strings.TrimSpace(tok)) {
		case "U", "TH":
			return true
		}
	}
	return false
}

// isCarbonate reports whether the Strunz 10th-edition classification
// code names a carbonate (or nitrate) — the rule for deriving the
// ReactsToAcid boolean (mi-8pcs). Strunz class 05 covers carbonates
// and nitrates: calcite, dolomite, malachite, azurite, smithsonite,
// rhodochrosite, witherite — all of which fizz reproducibly in cold
// dilute HCl (dolomite more weakly, but still observably). Nitrates
// are field-rare in non-arid environments but also under class 05.
//
// The input may look like "5.AB.05", "05.AB.05", or just "5" — we
// parse the leading class number and ignore the rest of the dotted
// hierarchy.
//
// Why this rule is narrow:
//   - Phosphates (08.*) are acid-soluble but don't fizz in cold HCl.
//   - Sulfides (02.*) react to produce H2S but it isn't the visible
//     fizz the checkbox implies.
//   - Silicates need HF, not the field-test HCl this boolean stands
//     for.
//
// Operator override on the form always wins; this is a starting
// point, not the truth about the specific specimen.
func isCarbonate(strunz string) bool {
	strunz = strings.TrimSpace(strunz)
	if strunz == "" {
		return false
	}
	end := 0
	for end < len(strunz) && strunz[end] >= '0' && strunz[end] <= '9' {
		end++
	}
	if end == 0 {
		return false
	}
	// Leading digits — accept "5" or "05" as class 5.
	switch strunz[:end] {
	case "5", "05":
		return true
	}
	return false
}

var elementSplitRe = regexp.MustCompile(`[\s,]+`)

// mapRunes applies mapper to every rune in s. Returns (mapped, true)
// only when every rune mapped successfully; on any miss the caller
// keeps the original input so we don't silently drop information.
func mapRunes(s string, mapper func(rune) (rune, bool)) (string, bool) {
	if s == "" {
		return "", false
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		mapped, ok := mapper(r)
		if !ok {
			return "", false
		}
		b.WriteRune(mapped)
	}
	return b.String(), true
}
