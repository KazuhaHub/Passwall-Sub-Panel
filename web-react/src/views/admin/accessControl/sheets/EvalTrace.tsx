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
  // A report-only hit is "what it WOULD do": the closed table's would-be-denied row.
  if (r.verdict === 'observe') return 'would_deny'
  if (r.verdict === 'allow') return r.terminating_step === 'exempt' ? 'exempt' : 'allow'
  return 'none'
}

/** The inbound a source-rule step belongs to, by its node id (undefined while unknown). */
export type InboundName = (nodeId: number) => string | undefined

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
  // A report-only hit is decided by the flag, whatever the rule's kind: it recorded the connection and blocked nothing.
  else if (hit?.report_only || result.verdict === 'observe') {
    sentence = t(`${T}verdict.${hit?.kind === 'source' ? 'observe_source' : 'observe'}`, { entry: hit?.entry ?? result.steps.find(s => s.result === 'hit' && s.entry)?.entry ?? '' })
  }
  else if (hit?.kind === 'source') sentence = t(`${T}verdict.source`, { entry })
  else if (result.verdict === 'block') sentence = t(`${T}verdict.block`, { step: hit?.step ?? 0, name, entry })
  else if (result.verdict === 'allow') sentence = t(`${T}verdict.allow`, { step: hit?.step ?? 0, name })
  else sentence = t(`${T}verdict.default`)
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
      <ToneBadge tone={accessTone(theme, kind)} label={t(`${T}badge.${result.verdict === 'observe' ? 'observe' : kind}`)} testId="test-verdict" data={kind} />
      <Typography sx={{ minWidth: 0, overflowWrap: 'anywhere' }}>{sentence}</Typography>
    </Box>
  )
}

/** The trace, step by step, on the same line-and-dot pattern as the policy rail. */
export default function EvalTrace({ result, inboundName }: { result: DestTestResult; inboundName?: InboundName }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  return (
    <Box component="ol" data-testid="eval-trace" sx={{ listStyle: 'none', m: 0, p: 0, pl: 0.5, borderLeft: `2px solid ${md.outlineVariant}`, ml: 1.5 }}>
      {result.steps.map(s => {
        const report = s.report_only === true
        const inbound = s.kind === 'source' && s.node_id ? inboundName?.(s.node_id) : undefined
        const named = s.kind === 'source'
          ? [t(`${T}kind.source`), inbound].filter(Boolean).join(' · ')
          : s.kind === 'builtin' ? t(`${T}kind.builtin`)
            : s.kind === 'exempt' ? t(`${T}kind.exempt`)
              : `${t(`${T}kind.${report ? 'report' : s.kind}`)}${s.policy_id ? ` · ${s.name || t(`${T}deleted_policy`)}` : ''}`
        const label = report && s.kind === 'source' ? `${named} · ${t(`${T}kind.report`)}` : named
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
                {report && s.result === 'hit' ? t(`${T}result.hit_report`) : t(`${T}result.${s.result}`)}
                {s.entry && <Box component="span" sx={{ fontFamily: 'ui-monospace, monospace', fontSize: 13, ml: 0.75 }}>{s.entry}</Box>}
              </Typography>
            </Box>
          </Box>
        )
      })}
    </Box>
  )
}
