import { StatusBadge } from '@/components/common/StatusBadge'
import type { Tone } from '@/components/common/tones'
import type { ProbeQuality, ProbeState } from '@/api/types'
import { useNodesT } from '../shared/useNodesT'

const TONE: Record<ProbeState, Tone> = { ok: 'success', degraded: 'warning', down: 'danger', unknown: 'neutral' }

/**
 * Link quality as one badge: the state in words, then the median round trip and the loss when there is a
 * measurement, and a plain "Withheld" when the line is taken out of subscriptions for it. No measurement draws
 * nothing. The title carries the longer explanation, so the badge stays one line.
 */
export function ProbeChip({ probe, withheld, testId = 'probe-quality' }: { probe?: ProbeQuality | null; withheld?: boolean; testId?: string }) {
  const t = useNodesT()
  if (!probe) return null
  const state: ProbeState = probe.state in TONE ? probe.state : 'unknown'
  const known = state !== 'unknown'
  const isWithheld = !!(withheld || probe.withheld)
  const label = [
    t(`nodes.probe.state_${state}`),
    known ? `${t('nodes.probe.rtt', { ms: Math.round(probe.median_rtt_ms) })} · ${t('nodes.probe.loss', { percent: Math.round(probe.loss_percent) })}` : '',
    isWithheld ? t('nodes.probe.withheld') : '',
  ].filter(Boolean).join(' · ')
  const title = [
    t('nodes.probe.tip_measured'),
    known ? '' : t('nodes.probe.tip_unknown'),
    probe.reported_at ? t('nodes.probe.tip_reported', { time: new Date(probe.reported_at).toLocaleString() }) : '',
    isWithheld ? t('nodes.probe.withheld_hint') : '',
  ].filter(Boolean).join('\n')
  return <span data-testid={testId} data-state={state} title={title} className="inline-flex w-fit"><StatusBadge tone={isWithheld ? 'danger' : TONE[state]} label={label} quiet={false} /></span>
}
