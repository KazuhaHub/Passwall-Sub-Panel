/**
 * A line diff for "review changes" (design 3.12 item 9): common head and tail are cut off, the middle is anchored on
 * lines that occur once on each side (patience), and what is left between anchors is compared exactly when small.
 * No dependency: a rule set is thousands of lines, mostly unique, and a plain Myers diff would need memory
 * proportional to the square of a moved group.
 */
export interface DiffOp {
  kind: 'same' | 'add' | 'del'
  text: string
  /** 1-based line numbers on each side where the line exists. */
  oldLine?: number
  newLine?: number
}

const EXACT_LIMIT = 90_000

export function diffLines(a: readonly string[], b: readonly string[]): DiffOp[] {
  const out: DiffOp[] = []
  range(a, 0, a.length, b, 0, b.length, out)
  return out
}

const same = (a: readonly string[], i: number, j: number): DiffOp => ({ kind: 'same', text: a[i], oldLine: i + 1, newLine: j + 1 })
const del = (a: readonly string[], i: number): DiffOp => ({ kind: 'del', text: a[i], oldLine: i + 1 })
const add = (b: readonly string[], j: number): DiffOp => ({ kind: 'add', text: b[j], newLine: j + 1 })

function range(a: readonly string[], a0: number, a1: number, b: readonly string[], b0: number, b1: number, out: DiffOp[]) {
  while (a0 < a1 && b0 < b1 && a[a0] === b[b0]) { out.push(same(a, a0, b0)); a0++; b0++ }
  let ea = a1
  let eb = b1
  while (a0 < ea && b0 < eb && a[ea - 1] === b[eb - 1]) { ea--; eb-- }
  middle(a, a0, ea, b, b0, eb, out)
  for (let i = ea, j = eb; i < a1; i++, j++) out.push(same(a, i, j))
}

function middle(a: readonly string[], a0: number, a1: number, b: readonly string[], b0: number, b1: number, out: DiffOp[]) {
  if (a0 === a1) { for (let j = b0; j < b1; j++) out.push(add(b, j)); return }
  if (b0 === b1) { for (let i = a0; i < a1; i++) out.push(del(a, i)); return }
  const anchors = patienceAnchors(a, a0, a1, b, b0, b1)
  if (anchors.length > 0) {
    let pa = a0
    let pb = b0
    for (const [i, j] of anchors) {
      range(a, pa, i, b, pb, j, out)
      out.push(same(a, i, j))
      pa = i + 1
      pb = j + 1
    }
    range(a, pa, a1, b, pb, b1, out)
    return
  }
  if ((a1 - a0) * (b1 - b0) <= EXACT_LIMIT) { exact(a, a0, a1, b, b0, b1, out); return }
  for (let i = a0; i < a1; i++) out.push(del(a, i))
  for (let j = b0; j < b1; j++) out.push(add(b, j))
}

/** Pairs (i, j) of lines unique on both sides, in an order that is increasing on both (longest such chain). */
function patienceAnchors(a: readonly string[], a0: number, a1: number, b: readonly string[], b0: number, b1: number): [number, number][] {
  const countA = new Map<string, number>()
  for (let i = a0; i < a1; i++) countA.set(a[i], (countA.get(a[i]) ?? 0) + 1)
  const posB = new Map<string, number>()
  const countB = new Map<string, number>()
  for (let j = b0; j < b1; j++) { countB.set(b[j], (countB.get(b[j]) ?? 0) + 1); posB.set(b[j], j) }
  const pairs: [number, number][] = []
  for (let i = a0; i < a1; i++) {
    if (countA.get(a[i]) === 1 && countB.get(a[i]) === 1) pairs.push([i, posB.get(a[i])!])
  }
  if (pairs.length === 0) return []
  // Longest increasing subsequence on j (pairs are already increasing on i).
  const tails: number[] = []
  const prev = new Array<number>(pairs.length).fill(-1)
  for (let k = 0; k < pairs.length; k++) {
    let lo = 0
    let hi = tails.length
    while (lo < hi) { const mid = (lo + hi) >> 1; if (pairs[tails[mid]][1] < pairs[k][1]) lo = mid + 1; else hi = mid }
    if (lo > 0) prev[k] = tails[lo - 1]
    tails[lo] = k
  }
  const chain: [number, number][] = []
  for (let k = tails[tails.length - 1]; k >= 0; k = prev[k]) chain.push(pairs[k])
  return chain.reverse()
}

function exact(a: readonly string[], a0: number, a1: number, b: readonly string[], b0: number, b1: number, out: DiffOp[]) {
  const n = a1 - a0
  const m = b1 - b0
  const w = m + 1
  const t = new Uint32Array((n + 1) * w)
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      t[i * w + j] = a[a0 + i] === b[b0 + j] ? t[(i + 1) * w + j + 1] + 1 : Math.max(t[(i + 1) * w + j], t[i * w + j + 1])
    }
  }
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (a[a0 + i] === b[b0 + j]) { out.push(same(a, a0 + i, b0 + j)); i++; j++ }
    else if (t[(i + 1) * w + j] >= t[i * w + j + 1]) { out.push(del(a, a0 + i)); i++ }
    else { out.push(add(b, b0 + j)); j++ }
  }
  while (i < n) { out.push(del(a, a0 + i)); i++ }
  while (j < m) { out.push(add(b, b0 + j)); j++ }
}

export function diffStats(a: readonly string[], b: readonly string[]): { added: number; removed: number } {
  let added = 0
  let removed = 0
  for (const op of diffLines(a, b)) { if (op.kind === 'add') added++; else if (op.kind === 'del') removed++ }
  return { added, removed }
}

export interface Hunk {
  oldStart: number
  newStart: number
  lines: DiffOp[]
}

/** The changes with `context` unchanged lines around each; changes closer than that share one hunk. */
export function hunksOf(a: readonly string[], b: readonly string[], context = 2): Hunk[] {
  const ops = diffLines(a, b)
  const hunks: Hunk[] = []
  let i = 0
  while (i < ops.length) {
    if (ops[i].kind === 'same') { i++; continue }
    let from = i
    while (from > 0 && ops[from - 1].kind === 'same' && i - from < context) from--
    let to = i
    let lastChange = i
    while (to < ops.length) {
      if (ops[to].kind !== 'same') lastChange = to
      else if (to - lastChange > context * 2) break
      to++
    }
    const end = Math.min(lastChange + context + 1, ops.length)
    const slice = ops.slice(from, end)
    const first = slice[0]
    hunks.push({ oldStart: first.oldLine ?? firstLine(slice, 'oldLine'), newStart: first.newLine ?? firstLine(slice, 'newLine'), lines: slice })
    i = end
  }
  return hunks
}

function firstLine(ops: DiffOp[], key: 'oldLine' | 'newLine'): number {
  for (const o of ops) if (o[key] !== undefined) return o[key]!
  return 1
}
