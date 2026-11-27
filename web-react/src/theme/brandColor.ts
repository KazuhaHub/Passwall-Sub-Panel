// The brand colour a user (or the site admin) picked, turned into the two CSS values the
// stylesheet needs: the fill (`--brand`) and the text drawn on it (`--brand-foreground`).
//
// The six presets do not come through here: their light and dark values are written out
// in styles/ui.css. This is only for a colour that is NOT a preset (the colour picker, or a
// hex the site admin configured), which has no stylesheet block and gets inline values.
//
// The old Material 3 palette generator is gone, so the tones are derived directly in OKLCH:
// keep the colour's hue and (bounded) chroma, and pick the lightness for the mode so the fill
// reads on the page and the label on it stays legible. The lightness targets are the ones the
// presets in the stylesheet sit at (about 0.50 in light mode, about 0.82 in dark mode), and
// brandColor.test.ts measures how far a preset re-derived from its own hex lands from the
// stylesheet value, so that calibration cannot silently rot.

export interface BrandTones {
  /** Fill for primary buttons, the active nav item, focus accents. */
  primary: string
  /** Text and icons drawn on `primary`. */
  onPrimary: string
}

type Vec3 = [number, number, number]

const clamp01 = (n: number) => Math.min(1, Math.max(0, n))

function srgbToLinear(c: number): number {
  return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
}
function linearToSrgb(c: number): number {
  return c <= 0.0031308 ? 12.92 * c : 1.055 * c ** (1 / 2.4) - 0.055
}

// Björn Ottosson's OKLab, https://bottosson.github.io/posts/oklab/
function linearToOklab([r, g, b]: Vec3): Vec3 {
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b)
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b)
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b)
  return [
    0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
  ]
}
function oklabToLinear([L, a, b]: Vec3): Vec3 {
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3
  return [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ]
}

function hexToLinear(hex: string): Vec3 {
  const n = parseInt(hex.slice(1), 16)
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255].map(v => srgbToLinear(v / 255)) as Vec3
}

const inGamut = (rgb: Vec3) => rgb.every(c => c >= -1e-4 && c <= 1 + 1e-4)

// An OKLCH colour as #rrggbb. A colour outside sRGB keeps its lightness and hue and loses
// chroma until it fits; clipping channels instead would shift the hue.
export function oklchToHex(L: number, C: number, hueRad: number): string {
  let lo = 0
  let hi = C
  let rgb = oklabToLinear([L, C * Math.cos(hueRad), C * Math.sin(hueRad)])
  if (!inGamut(rgb)) {
    for (let i = 0; i < 24; i++) {
      const mid = (lo + hi) / 2
      const cand = oklabToLinear([L, mid * Math.cos(hueRad), mid * Math.sin(hueRad)])
      if (inGamut(cand)) lo = mid; else hi = mid
    }
    rgb = oklabToLinear([L, lo * Math.cos(hueRad), lo * Math.sin(hueRad)])
  }
  const byte = (c: number) => Math.round(clamp01(linearToSrgb(clamp01(c))) * 255).toString(16).padStart(2, '0')
  return `#${byte(rgb[0])}${byte(rgb[1])}${byte(rgb[2])}`
}

/** WCAG contrast ratio of two #rrggbb colours. */
export function contrastRatio(a: string, b: string): number {
  const y = (hex: string) => {
    const [r, g, bl] = hexToLinear(hex)
    return 0.2126 * r + 0.7152 * g + 0.0722 * bl
  }
  const [hi, lo] = [y(a), y(b)].sort((p, q) => q - p)
  return (hi + 0.05) / (lo + 0.05)
}

const LIGHT_L = 0.5
const DARK_L = 0.82
// Chroma is kept from the source colour but bounded: a pale colour still has to read as a
// brand colour on a button, and a neon one must not vibrate. Near-grey sources stay grey
// (their hue is meaningless and would otherwise tint the whole UI red).
const MIN_CHROMA = 0.1
const MAX_CHROMA = 0.17
const GREY_CHROMA = 0.02

export function brandTones(hex: string, mode: 'light' | 'dark'): BrandTones {
  const [, a, b] = linearToOklab(hexToLinear(hex))
  const chroma = Math.hypot(a, b)
  const hue = Math.atan2(b, a)
  const grey = chroma < GREY_CHROMA
  const c = grey ? 0 : Math.min(MAX_CHROMA, Math.max(MIN_CHROMA, chroma))

  if (mode === 'light') {
    // The label is white, so the fill is what gives way: darken it until 4.5:1 holds, as dark mode does for its
    // label. LIGHT_L already clears it for every hue measured (brandColor.test.ts sweeps them); the loop is the
    // guarantee, not the usual path.
    let primary = oklchToHex(LIGHT_L, c, hue)
    for (let l = LIGHT_L; contrastRatio(primary, '#ffffff') < 4.5 && l > 0.05; l -= 0.02) primary = oklchToHex(l, c, hue)
    return { primary, onPrimary: '#ffffff' }
  }
  const primary = oklchToHex(DARK_L, c, hue)
  // A deep tone of the same hue, as the presets do, but never less legible than 4.5:1.
  let on = oklchToHex(0.3, grey ? 0 : 0.08, hue)
  for (let l = 0.3; contrastRatio(primary, on) < 4.5 && l > 0.05; l -= 0.03) on = oklchToHex(l, grey ? 0 : 0.06, hue)
  return { primary, onPrimary: on }
}
