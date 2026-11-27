// THE TONES A STATUS CAN HAVE, drawn from the tokens in styles/ui.css and nothing
// else. Colour is for the abnormal: a healthy or idle row gets a small dot and
// plain words, and only a warning, a failure or a notice gets a tinted badge with
// an icon (plan 3.2). The words of every badge stay in --foreground; the hue lives
// on the dot and the icon, because amber or red words on their own tint read at
// about 3:1 to 4.4:1 and --foreground on it reads at well over 4.5:1
// (StatusBadge.contrast.test.ts holds every tone to that, in both themes).
//
// The neutral dot is the full --muted-foreground, not a softened one: at 60% it read at 2.3:1
// on the light background, below the 3:1 a graphical object needs.
//
// The class strings are written out in full on purpose: Tailwind finds a utility
// by scanning for its literal text, so a class assembled from a tone name would
// never be generated. `tokens` names the same colours as CSS variables so the
// contrast test can read them back; a test also checks the two agree.

export type Tone = 'success' | 'warning' | 'danger' | 'info' | 'neutral'

/**
 * The shape each tone's dot is drawn in. Colour alone must not carry a state (WCAG 1.4.1):
 * in the light theme the warning, danger and neutral hues are close in lightness, so each
 * tone also has its own silhouette, readable at 8px. See ToneDot for the drawings.
 */
/**
 * Danger is the heaviest: a square filling the box (about 56 of 64 units), over the circle (44),
 * triangle and diamond (32 each) and ring (31). The most urgent tone must not be the faintest.
 */
export type ToneShape = 'circle' | 'triangle' | 'diamond' | 'square' | 'ring'

export const TONE_SHAPE: Record<Tone, ToneShape> = {
  success: 'circle',
  warning: 'triangle',
  danger: 'square',
  info: 'diamond',
  neutral: 'ring',
}

export const TONES: readonly Tone[] = ['success', 'warning', 'danger', 'info', 'neutral']

export interface ToneStyle {
  /** Dot fill. */
  dot: string
  /** Icon colour. */
  ink: string
  /** Badge fill when the tone is shown as a badge, not as a quiet dot. */
  tint: string
  /** Border colour for a line or a connector drawn in the tone. */
  line: string
  /** Badge ring. */
  ring: string
  /** Shown as a tinted badge unless the caller asks for the quiet form. */
  loud: boolean
  tokens: { ink: string; tint: string; tintAlpha: number; dot: string }
}

export const TONE_STYLE: Record<Tone, ToneStyle> = {
  success: {
    dot: 'bg-success', ink: 'text-success', tint: 'bg-success/12', ring: 'ring-success/25', line: 'border-success', loud: false,
    tokens: { ink: '--success', tint: '--success', tintAlpha: 0.12, dot: '--success' },
  },
  warning: {
    dot: 'bg-warning-ink', ink: 'text-warning-ink', tint: 'bg-warning/12', ring: 'ring-warning/30', line: 'border-warning-ink', loud: true,
    tokens: { ink: '--warning-ink', tint: '--warning', tintAlpha: 0.12, dot: '--warning-ink' },
  },
  danger: {
    dot: 'bg-destructive', ink: 'text-destructive', tint: 'bg-destructive/12', ring: 'ring-destructive/25', line: 'border-destructive', loud: true,
    tokens: { ink: '--destructive', tint: '--destructive', tintAlpha: 0.12, dot: '--destructive' },
  },
  info: {
    dot: 'bg-info', ink: 'text-info', tint: 'bg-info/12', ring: 'ring-info/25', line: 'border-info', loud: true,
    tokens: { ink: '--info', tint: '--info', tintAlpha: 0.12, dot: '--info' },
  },
  neutral: {
    dot: 'bg-muted-foreground', ink: 'text-muted-foreground', tint: 'bg-muted', ring: 'ring-border', line: 'border-muted-foreground/50', loud: false,
    tokens: { ink: '--muted-foreground', tint: '--muted', tintAlpha: 1, dot: '--muted-foreground' },
  },
}
