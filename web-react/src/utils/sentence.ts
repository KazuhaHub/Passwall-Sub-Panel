// A REASON WITH NO HOLES IN IT. The risk center's reasons are sentences with
// numbers in them, and the numbers come from what the server stored: a
// verdict's evidence, a record's params. When one is not there — a verdict
// stored without evidence, params missing a field, a row an older build
// wrote — interpolation prints it as nothing, and the admin reads
// 「有 组互不相连的常驻省份（容错 ）」. Every reason builder (reasonText,
// riskCodeText, flagText) fills its sentence through here instead, and reads
// the same sentence without numbers when one is missing. Pure: no I/O.
import type { Translate } from './geoAnomaly'

/** A value a sentence can print: a finite number, or a non-empty string. */
function printable(v: unknown): boolean {
  return (typeof v === 'number' && Number.isFinite(v)) || (typeof v === 'string' && v !== '')
}

// A placeholder still in the output. i18next leaves a variable it was not
// given in place (skipOnVariables, its default; pinned by sentence.test.ts
// with the app's own options), so a value withheld below shows up here.
const HOLE = /\{\{[^}]*\}\}/

/**
 * `key` filled from `values`, or `bare()` — the same sentence without its
 * numbers — when the string names a value that is missing. Only the
 * printable values are passed on: one that is absent, undefined, NaN or ''
 * stays a placeholder instead of printing as a blank, and is caught. A value
 * the string does not name is never checked, so a builder may hand over
 * everything it has. `defaultValue` is what a missing STRING reads as, the
 * caller's own fallback, returned as it is.
 */
export function sentence(
  t: Translate, key: string, values: Record<string, unknown>, bare: () => string, defaultValue?: string,
): string {
  const given: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(values)) {
    if (printable(v)) given[k] = v
  }
  const out = t(key, defaultValue === undefined ? given : { ...given, defaultValue })
  return HOLE.test(out) ? bare() : out
}
