import { Box, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { DestTestResult, DestTestStep } from '@/api/accessControl'
import ToneBadge from '@/components/ToneBadge'
import { accessTone, type AccessToneKind } from '@/utils/accessControl'

const T = 'admin:access_control.test.'

/** The step that ended the evaluation, if a step did. */
export function terminatingStep(r: DestTestResult): DestTestStep | undefined {
  return r.steps.find(s => s.result === 'hit')
}

export function verdictTone(r: DestTestResult): AccessToneKind {
  if (r.verdict === 'block') return 'block'
  if (r.verdict === 'observe') return 'observe'
  if (r.verdict === 'allow') return r.terminating_step === 'exempt' ? 'exempt' : 'allow'
  return 'none'
}

/** The verdict's badge and the sentence beside it. */
export function VerdictLine({ result }: { result: DestTestResult }) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const kind = verdictTone(result)
  const hit = terminatingStep(result)
  const name = hit?.policy_id ? (hit.name || t(`${T}deleted_policy`)) : (hit?.name ?? '')
  const entry = hit?.entry ?? ''
  let sentence: string
  if (result.terminating_step === 'exempt') sentence = t(`${T}verdict.exempt`)
  else if (hit?.kind === 'builtin') sentence = t(`${T}verdict.builtin`)
  else if (hit?.kind === 'source') sentence = t(`${T}verdict.source`, { entry })
  else if (result.verdict === 'block') sentence = t(`${T}verdict.block`, { step: hit?.step ?? 0, name, entry })
  else if (result.verdict === 'allow') sentence = t(`${T}verdict.allow`, { step: hit?.step ?? 0, name })
  else sentence = t(`${T}verdict.default`)
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
      <ToneBadge tone={accessTone(theme, kind)} label={t(`${T}badge.${kind}`)} testId="test-verdict" data={kind} />
      <Typography sx={{ minWidth: 0, overflowWrap: 'anywhere' }}>{sentence}</Typography>
    </Box>
  )
}

/** The trace, step by step, on the same line-and-dot pattern as the policy rail. */
export default function EvalTrace({ result }: { result: DestTestResult }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  return (
    <Box component="ol" data-testid="eval-trace" sx={{ listStyle: 'none', m: 0, p: 0, pl: 0.5, borderLeft: `2px solid ${md.outlineVariant}`, ml: 1.5 }}>
      {result.steps.map(s => {
        const label = s.kind === 'builtin' ? t(`${T}kind.builtin`)
          : s.kind === 'source' ? t(`${T}kind.source`)
          : s.kind === 'exempt' ? t(`${T}kind.exempt`)
          : `${t(`${T}kind.${s.kind}`)}${s.policy_id ? ` · ${s.name || t(`${T}deleted_policy`)}` : ''}`
        const hit = s.result === 'hit'
        return (
          <Box component="li" key={s.step} data-result={s.result} sx={{ display: 'flex', gap: 1.5, py: 0.75, ml: -2.5, alignItems: 'flex-start' }}>
            <Box aria-hidden sx={{
              flex: '0 0 auto', width: 24, height: 24, borderRadius: '50%', display: 'grid', placeItems: 'center', fontWeight: 600, fontSize: 12,
              bgcolor: md.surfaceContainerHighest, color: md.onSurface,
            }}>{s.step}</Box>
            <Box sx={{ minWidth: 0 }}>
              <Typography sx={{ fontSize: 14, fontWeight: hit ? 600 : 400, overflowWrap: 'anywhere' }}>{label}</Typography>
              <Typography variant="body2" sx={{ color: s.result === 'shadowed' ? md.onSurface : md.onSurfaceVariant, fontWeight: s.result === 'shadowed' ? 600 : 400 }}>
                {t(`${T}result.${s.result}`)}
                {s.entry && <Box component="span" sx={{ fontFamily: 'ui-monospace, monospace', fontSize: 13, ml: 0.75 }}>{s.entry}</Box>}
              </Typography>
            </Box>
          </Box>
        )
      })}
    </Box>
  )
}
