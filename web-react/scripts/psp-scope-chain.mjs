// The marker-chain model behind the "new scope" selector (see psp-scope.mjs and
// the contract at the top of src/styles/ui.css), as plain functions.
//
// A CHAIN is the markers on an element's ancestor line, root first, ending with
// the element itself if it is a marker: a string over 'U' (new: .psp-ui or
// [data-slot]) and 'L' (.psp-legacy). Unmarked elements do not appear. Runs of
// the same letter behave as one.
//
// Rule: an element is in new scope iff the NEAREST marker at or above it is U,
// i.e. the chain ends in 'U'. An element with no marker above is not new.

// How many alternations the generated selector supports. Level k (0..N-1) covers
// chains whose longest `(LU)^k` subsequence has length k, so a chain may
// alternate U L U L U L U (three L's, four U-runs) and no deeper.
export const NEW_SCOPE_LEVELS = 4

export function isNewChain(chain) {
  return chain.endsWith('U')
}

// Length of the longest `LULU...` run of PAIRS that is a subsequence of the
// chain (greedy matching is optimal for subsequences). This is the selector
// level a new element with that chain needs.
export function levelNeeded(chain) {
  let pairs = 0
  let wantL = true
  for (const c of chain) {
    if (wantL && c === 'L') wantL = false
    else if (!wantL && c === 'U') { pairs++; wantL = true }
  }
  return pairs
}

// True when the selector classifies a NEW chain correctly. Legacy chains are
// never misclassified at any depth: the exclusions only ever remove matches.
export function isSupportedChain(chain, levels = NEW_SCOPE_LEVELS) {
  return !isNewChain(chain) || levelNeeded(chain) <= levels - 1
}
