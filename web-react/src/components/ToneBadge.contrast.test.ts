import { decomposeColor, getContrastRatio } from '@mui/material/styles'
import { describe, expect, it } from 'vitest'
import { createAppTheme } from '@/theme'
import { severityTone, stateTone } from './ToneBadge'
import type { CardState, Severity } from '@/utils/diagnostics'

// Alpha badges are painted on the card surface; checking the uncomposited
// warning colour would measure a different background from the rendered UI.
function onSurface(color: string, surface: string): string {
  const fg = decomposeColor(color)
  if (fg.type !== 'rgba') return color
  const bg = decomposeColor(surface)
  const alpha = fg.values[3] ?? 1
  return `rgb(${fg.values.slice(0, 3).map((v, i) => v * alpha + bg.values[i] * (1 - alpha)).join(',')})`
}

const states: CardState[] = ['failing', 'attention', 'ok', 'idle', 'measuring', 'inhibited', 'not_applicable', 'none', 'recorded']
const severities: Severity[] = ['critical', 'error', 'warn', 'notice']

describe.each(['light', 'dark'] as const)('%s badge text contrast', mode => {
  const theme = createAppTheme({ mode, sourceColor: '#6750a4', language: 'en-US' })
  for (const [label, tone] of [
    ...[...states, 'quiet' as const].map(state => [state, stateTone(theme, state)] as const),
    ...severities.map(severity => [severity, severityTone(theme, severity)] as const),
  ]) {
    it(`${label} reads at least 4.5:1 on every card surface`, () => {
      for (const surface of [theme.palette.md.surface, theme.palette.md.surfaceContainer, theme.palette.md.surfaceContainerHighest]) {
        expect(getContrastRatio(tone.fg, onSurface(tone.bg, surface))).toBeGreaterThanOrEqual(4.5)
      }
    })
  }
})
