const PALETTE_SIZE = 8

/**
 * Which of the 8 landing colours (--landing-1..8, src/styles/data-tokens.css) an
 * id gets, from a hash of the id's text. The same landing therefore has the
 * same colour on every page and after every reload with no table to keep, and a
 * numeric id and its string form agree. Collisions are possible beyond 8
 * landings; the colour is a visual aid next to the name, never the only cue.
 */
export function landingColorIndex(id: string | number): number {
  let h = 0x811c9dc5 // FNV-1a, 32 bit
  for (const ch of String(id)) {
    h ^= ch.codePointAt(0)!
    h = Math.imul(h, 0x01000193) >>> 0
  }
  // The low bits of FNV are weak for short inputs; fold the high half in.
  return (((h ^ (h >>> 16)) >>> 0) % PALETTE_SIZE) + 1
}

export function landingColorVar(id: string | number): string {
  return `var(--landing-${landingColorIndex(id)})`
}
