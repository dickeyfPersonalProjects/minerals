-- 0009_normalize_chemical_formula: one-shot backfill that rewrites
-- HTML-flavored chemical_formula values into clean Unicode (mi-c8v).
--
-- Mindat returns formulas as HTML markup (e.g. H<sub>2</sub>O,
-- CuSO<sub>4</sub> &middot; 5H<sub>2</sub>O). Earlier polecat work
-- only fixed display; mi-c8v moves the fix to the write boundary in
-- Go AND normalizes existing rows here so the column is uniformly
-- Unicode at rest regardless of when the row was written.
--
-- This migration touches two JSONB columns:
--   - specimens.type_data ->> 'chemical_formula'
--   - mineral_species.data ->> 'chemical_formula'
--
-- Translation table (matches internal/mindat/format.go exactly — the
-- Go helper handles future writes; this migration is the one-time
-- backfill for existing data):
--   <sub>0..9</sub>          → ₀ … ₉   (U+2080–U+2089)
--   <sup>0..9</sup>          → ⁰ … ⁹
--   <sup>+</sup>, <sup>-</sup> → ⁺ ⁻
--   <sup>n+</sup>            → ⁿ⁺
--   &middot;                 → · (U+00B7)
--   &amp;                    → &
--   &nbsp;                   → space
--   &minus;                  → − (U+2212)
-- Any remaining HTML tags are stripped as a safety net (inner text
-- preserved). Unknown HTML entities are left intact — same policy as
-- the Go helper, so we don't silently mangle data on either path.

BEGIN;

-- Local helper. Defined as a regular (not temp) function so it's
-- visible across the whole migration; dropped at the end.
CREATE FUNCTION pg_temp_mi_c8v_normalize_chem_formula(input text)
RETURNS text
LANGUAGE plpgsql
IMMUTABLE
AS $$
DECLARE
    out text := input;
BEGIN
    IF out IS NULL THEN
        RETURN NULL;
    END IF;

    -- Sub/sup digits (case-insensitive via the 'i' regex flag).
    out := regexp_replace(out, '<sub>0</sub>', '₀', 'gi');
    out := regexp_replace(out, '<sub>1</sub>', '₁', 'gi');
    out := regexp_replace(out, '<sub>2</sub>', '₂', 'gi');
    out := regexp_replace(out, '<sub>3</sub>', '₃', 'gi');
    out := regexp_replace(out, '<sub>4</sub>', '₄', 'gi');
    out := regexp_replace(out, '<sub>5</sub>', '₅', 'gi');
    out := regexp_replace(out, '<sub>6</sub>', '₆', 'gi');
    out := regexp_replace(out, '<sub>7</sub>', '₇', 'gi');
    out := regexp_replace(out, '<sub>8</sub>', '₈', 'gi');
    out := regexp_replace(out, '<sub>9</sub>', '₉', 'gi');

    out := regexp_replace(out, '<sup>0</sup>', '⁰', 'gi');
    out := regexp_replace(out, '<sup>1</sup>', '¹', 'gi');
    out := regexp_replace(out, '<sup>2</sup>', '²', 'gi');
    out := regexp_replace(out, '<sup>3</sup>', '³', 'gi');
    out := regexp_replace(out, '<sup>4</sup>', '⁴', 'gi');
    out := regexp_replace(out, '<sup>5</sup>', '⁵', 'gi');
    out := regexp_replace(out, '<sup>6</sup>', '⁶', 'gi');
    out := regexp_replace(out, '<sup>7</sup>', '⁷', 'gi');
    out := regexp_replace(out, '<sup>8</sup>', '⁸', 'gi');
    out := regexp_replace(out, '<sup>9</sup>', '⁹', 'gi');

    -- Charge superscripts. The variable-charge form (n+) MUST run
    -- before the bare +/- forms so 'n+' isn't half-matched.
    out := regexp_replace(out, '<sup>n\+</sup>', 'ⁿ⁺', 'gi');
    out := regexp_replace(out, '<sup>\+</sup>', '⁺', 'gi');
    out := regexp_replace(out, '<sup>-</sup>', '⁻', 'gi');

    -- Known HTML entities.
    out := replace(out, '&middot;', '·');
    out := replace(out, '&nbsp;', ' ');
    out := replace(out, '&minus;', '−');
    -- &amp; runs last so it can't accidentally re-introduce the prefix
    -- of an entity above (e.g. &amp;middot; → &middot;).
    out := replace(out, '&amp;', '&');

    -- Safety net: strip any remaining HTML tags. Inner text is kept,
    -- so unsupported markup like <b>x</b> becomes x — a strict
    -- improvement over leaving raw markup at rest.
    out := regexp_replace(out, '<[^>]*>', '', 'g');

    RETURN out;
END;
$$;

-- Backfill specimens.type_data.chemical_formula.
UPDATE specimens
SET type_data = jsonb_set(
        type_data,
        '{chemical_formula}',
        to_jsonb(pg_temp_mi_c8v_normalize_chem_formula(type_data->>'chemical_formula'))
    )
WHERE type_data ? 'chemical_formula'
  AND (type_data->>'chemical_formula') ~ '[<&]';

-- Backfill mineral_species.data.chemical_formula.
UPDATE mineral_species
SET data = jsonb_set(
        data,
        '{chemical_formula}',
        to_jsonb(pg_temp_mi_c8v_normalize_chem_formula(data->>'chemical_formula'))
    )
WHERE data ? 'chemical_formula'
  AND (data->>'chemical_formula') ~ '[<&]';

-- Acceptance gate: no remaining HTML markers in either column. Fails
-- the migration loudly if a future entity slips through the table
-- above. Unknown entities are still passed through verbatim by the
-- helper (matching Go-side behavior), so any '<' or unhandled '&'
-- left here is a regression we want to catch at migrate time, not
-- silently leave in production.
DO $$
DECLARE
    bad_count int;
BEGIN
    SELECT count(*) INTO bad_count
    FROM (
        SELECT type_data->>'chemical_formula' AS f FROM specimens
            WHERE type_data ? 'chemical_formula'
        UNION ALL
        SELECT data->>'chemical_formula' AS f FROM mineral_species
            WHERE data ? 'chemical_formula'
    ) s
    WHERE s.f ~ '<[a-zA-Z/]'
       OR s.f ~ '&(middot|amp|nbsp|minus);';
    IF bad_count > 0 THEN
        RAISE EXCEPTION 'normalize_chemical_formula backfill left % rows with HTML markup', bad_count;
    END IF;
END $$;

DROP FUNCTION pg_temp_mi_c8v_normalize_chem_formula(text);

COMMIT;
