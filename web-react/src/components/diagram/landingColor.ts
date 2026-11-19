import type { CSSProperties } from 'react'

const PALETTE_SIZE = 8

/** One landing's colour: a palette slot (--landing-1..8) and how many times the palette has been used up. */
export interface LandingColor { index: number; cycle: number }

/**
 * Colours for the landing nodes on one page, by FIRST APPEARANCE in the list the
 * caller passes. The first eight are always eight different colours; a hash of the
 * id could not promise that (ids 4 and 10 collided). The same list gives the same
 * colours on every render.
 *
 * PASS A STABLE LIST: the page's complete, unfiltered set of landing ids in a fixed
 * order (sorted by id), NOT the rows as currently displayed. A drag reorders the
 * rows and a filter hides some; if the list followed the display, every row's colour
 * would change with it. Past eight the
 * palette cycles and `cycle` goes up, which landingColorStyle draws as stripes: a
 * colour is a hint next to the landing's name, never the only way to tell two apart.
 */
export function assignLandingColors(ids: readonly (string | number)[]): Map<string, LandingColor> {
  const map = new Map<string, LandingColor>()
  for (const raw of ids) {
    const id = String(raw)
    if (map.has(id)) continue
    const n = map.size
    map.set(id, { index: (n % PALETTE_SIZE) + 1, cycle: Math.floor(n / PALETTE_SIZE) })
  }
  return map
}

export function landingColorVar(c: LandingColor): string {
  return `var(--landing-${c.index})`
}

/** Fill for a colour bar or ring: the colour, plus diagonal stripes once the palette has been reused. */
export function landingColorStyle(c: LandingColor): CSSProperties {
  return {
    backgroundColor: landingColorVar(c),
    ...(c.cycle > 0
      ? { backgroundImage: 'repeating-linear-gradient(135deg, transparent 0 2px, color-mix(in oklab, var(--card) 70%, transparent) 2px 4px)' }
      : {}),
  }
}
