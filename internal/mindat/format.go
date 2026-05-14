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
