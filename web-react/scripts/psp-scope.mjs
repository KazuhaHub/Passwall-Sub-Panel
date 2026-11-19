// The "new scope" selector, generated once.
//
// CONTRACT (the same text, in context, is at the top of src/styles/ui.css)
//   Markers: `.psp-ui` / `[data-slot]` are NEW (U); `.psp-legacy` is OLD (L). An
//   element is in NEW scope iff the nearest marker at or above it is U. Markers
//   alternate to NEW_SCOPE_LEVELS levels (psp-scope-chain.mjs); runs of the same
//   kind collapse, so U inside U costs nothing. No `@scope`: older iOS Safari
//   lacks it.
//
// DERIVATION (descendant combinators only). Write a chain as a string over {U, L}.
// For level k = 0..N-1 let S_0 = U and S_k = (L U)^k for k >= 1 (a descendant
// chain, last U is the subject), and E_0 = L and E_k = S_k L. Level k matches
//   S_k:not(E_k, E_k *)      the element is that last U ...
//   S_k *:not(E_k, E_k *)    ... or below it,
// unless it is at or below an L under the same prefix. The union over k is the
// new scope:
//   - a chain ending in U: take k = the longest (LU)^k subsequence; E_k would need
//     (LU)^(k+1), which does not exist, so it matches;
//   - a chain ending in L: every level's E_k matches (the final L comes after any
//     U), so nothing matches;
//   - a chain with more alternations than N-1: every lower level is excluded
//     because (LU)^(k+1) contains E_k as a prefix (k >= 1) or an L (k = 0), and
//     level N-1 is exceeded, so such an element is (wrongly) NOT new. That limit
//     is what NEW_SCOPE_LEVELS documents; LegacyBoundary warns in development.
// scripts/check-scope.mjs proves all of this exhaustively in Chrome.
//
// ui.css writes placeholder pseudo-classes; vite.config.ts expands them here:
//   :psp-new         :where(<scope>)          zero specificity (the layered reset)
//   :psp-new-root    :where(<entry point>)    new scope whose PARENT is not new:
//                                              where inheriting properties are set
//   :psp-new-strong  :is(<scope>)             specificity of the strongest member
//                                              (the unlayered shim)
// Any other `:psp-*` token is an error, as is one in an @import-ed file.
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { NEW_SCOPE_LEVELS } from './psp-scope-chain.mjs'

const U = ':is(.psp-ui, [data-slot])'
const L = '.psp-legacy'

export function newScopeMembers(levels = NEW_SCOPE_LEVELS) {
  const members = []
  for (let k = 0; k < levels; k++) {
    const subject = k === 0 ? U : Array.from({ length: k }, () => `${L} ${U}`).join(' ')
    const excl = k === 0 ? L : `${subject} ${L}`
    const not = `:not(${excl}, ${excl} *)`
    members.push(`${subject}${not}`, `${subject} *${not}`)
  }
  return members
}

export const NEW_SCOPE_MEMBERS = newScopeMembers()

// Entry points: new-scope elements whose parent is not in new scope. Only a
// MARKER can be one (an unmarked element has the same nearest marker as its
// parent, and a legacy parent only admits a new child that is itself a U), so the
// cheap marker test goes first: it rejects almost every element immediately,
// which matters because these rules would otherwise evaluate the whole scope
// selector, twice, for every element in the document (measured: ~290 ms of ~480
// ms of style recalculation on a 36,000-element legacy page).
export function newRootSelector(levels = NEW_SCOPE_LEVELS) {
  const scope = `:is(${newScopeMembers(levels).join(', ')})`
  return `${U}:not(${scope} > *)`
}

const TOKENS = {
  'psp-new-strong': levels => `:is(${newScopeMembers(levels).join(', ')})`,
  'psp-new-root': levels => `:where(${newRootSelector(levels)})`,
  'psp-new': levels => `:where(${newScopeMembers(levels).join(', ')})`,
}

// Comments are left alone: they mention the placeholders by name, and expanding
// those would bloat intermediate output (and could trip on prose like `:psp-*`).
export function expandScope(css, levels = NEW_SCOPE_LEVELS) {
  return css
    .split(/(\/\*[\s\S]*?\*\/)/)
    .map((part, i) => i % 2 === 1 ? part : part.replace(/:psp-[A-Za-z0-9_-]+/g, tok => {
      const make = TOKENS[tok.slice(1)]
      if (!make) throw new Error(`unknown scope placeholder ${tok} (known: ${Object.keys(TOKENS).map(t => ':' + t).join(', ')})`)
      return make(levels)
    }))
    .join('')
}

// Placeholders are expanded only in the entry stylesheet; the build inlines
// @import-ed files itself, so one hiding there would reach the browser as an
// invalid selector. Reject it instead.
export function assertNoPlaceholderImports(css, dir, read = p => readFileSync(p, 'utf8')) {
  for (const m of css.matchAll(/@import\s+["'](\.{1,2}\/[^"']+)["']/g)) {
    const file = resolve(dir, m[1])
    let text
    try { text = read(file) } catch { continue }
    if (/:psp-/.test(text ?? '')) throw new Error(`${file} is @import-ed by the scope stylesheet but contains a :psp-* placeholder; only the entry file is expanded`)
  }
}
