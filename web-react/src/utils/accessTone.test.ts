import { expect, it } from 'vitest'
import { createAppTheme } from '@/theme'
import { accessTone, type DestinationVerdictToneKind } from './accessControl'
const kinds: DestinationVerdictToneKind[] = ['block', 'deny', 'observe', 'trial', 'allow', 'exempt', 'direct', 'untestable']
it.each(['light', 'dark'] as const)('uses distinct verdict triples and the final-plan action colors in %s mode', mode => {
  const theme = createAppTheme({ mode, sourceColor: '#6750a4', language: 'en-US' })
  const tones = kinds.map(kind => accessTone(theme, kind))
  for (let i = 0; i < tones.length; i++) for (let j = i + 1; j < tones.length; j++) {
    expect(tones[i].bg !== tones[j].bg || tones[i].fg !== tones[j].fg || tones[i].Icon !== tones[j].Icon, `${kinds[i]} / ${kinds[j]}`).toBe(true)
  }
  expect(tones[0]).toMatchObject({ bg: theme.palette.md.surfaceContainerHighest, fg: theme.palette.md.error })
  expect(tones[2]).toMatchObject({ bg: theme.palette.md.secondaryContainer, fg: theme.palette.md.onSecondaryContainer })
  expect(tones[5]).toMatchObject({ bg: theme.palette.md.surfaceContainerHigh, fg: theme.palette.md.onSurface })
})
