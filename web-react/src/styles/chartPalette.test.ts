import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const read = (name: string) => readFileSync(fileURLToPath(new URL(`./${name}`, import.meta.url)), 'utf8')
const data = read('data-tokens.css')
const ui = read('ui.css')

// The categorical chart palette is a data palette: five series must be readable against the card they are
// drawn on (WCAG 3:1 for graphical objects) and must not collapse into one another. The values are read out
// of the stylesheets themselves so the test cannot drift from what ships.

function oklab(css: string): [number, number, number] {
  const m = /oklch\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*\)/.exec(css)
  if (!m) throw new Error(`not an oklch colour: ${css}`)
  const [L, C, h] = [Number(m[1]), Number(m[2]), (Number(m[3]) * Math.PI) / 180]
  return [L, C * Math.cos(h), C * Math.sin(h)]
}
function luminance([L, a, b]: [number, number, number]): number {
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3
  const lin = [4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s, -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s, -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s]
    .map(v => Math.min(1, Math.max(0, v)))
  return 0.2126 * lin[0] + 0.7152 * lin[1] + 0.0722 * lin[2]
}
const ratio = (a: number, b: number) => (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
function hexLum(hex: string): number {
  const c = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255).map(v => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4))
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2]
}
/** The declarations of one block (`:root {` or `.dark {`) of a stylesheet. */
function block(css: string, head: string): string {
  const start = css.indexOf(`${head} {`)
  return css.slice(start, css.indexOf('\n}', start))
}
function series(css: string, head: string, n: number): string {
  const m = new RegExp(`--series-${n}:\\s*([^;]+);`).exec(block(css, head))
  if (!m) throw new Error(`--series-${n} missing in ${head}`)
  return m[1].trim()
}
const brandOf = (head: string) => new RegExp('--brand:\\s*([^;]+);').exec(block(ui, head))![1].trim()

const MODES = [
  { name: 'light', head: ':root', surface: 1 },
  // The dark card is the lighter of the two dark surfaces, so it is the harder one to clear.
  { name: 'dark', head: '.dark', surface: luminance(oklab('oklch(0.205 0 0)')) },
]

describe('chart palette', () => {
  for (const mode of MODES) {
    const colours = [1, 2, 3, 4, 5].map(n => oklab(n === 1 ? brandOf(mode.head) : series(data, mode.head, n)))

    it(`${mode.name}: five series each meet 3:1 against the card`, () => {
      colours.forEach((c, i) => expect(ratio(luminance(c), mode.surface), `series ${i + 1}`).toBeGreaterThanOrEqual(3))
    })

    it(`${mode.name}: no two of the five sit closer than 0.1 in OKLab`, () => {
      for (let i = 0; i < 5; i++) for (let j = i + 1; j < 5; j++) {
        expect(Math.hypot(...colours[i].map((v, k) => v - colours[j][k])), `${i + 1} vs ${j + 1}`).toBeGreaterThan(0.1)
      }
    })
  }

  it('series 1 follows the brand, so every brand preset must itself clear 3:1 on the card', () => {
    for (const m of ui.matchAll(/^(:root|\.dark)\[data-brand="(\w+)"\]\s*\{\s*--brand:\s*(#[0-9a-f]{6})/gim)) {
      const surface = m[1] === ':root' ? 1 : luminance(oklab('oklch(0.205 0 0)'))
      expect(ratio(hexLum(m[3]), surface), `${m[1]} ${m[2]}`).toBeGreaterThanOrEqual(3)
    }
  })

  it('chart-1 to chart-5 are aliases of the series, in one place', () => {
    for (let n = 1; n <= 5; n++) expect(new RegExp(`--chart-${n}:\\s*var\\(--series-${n}\\)`).test(ui)).toBe(true)
    expect((ui.match(/--chart-1:/g) ?? []).length).toBe(1)
  })
})
