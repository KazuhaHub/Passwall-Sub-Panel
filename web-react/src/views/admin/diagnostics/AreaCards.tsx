import type { ReactNode } from 'react'
import { Box, Stack, Tooltip, Typography, useTheme } from '@mui/material'
import type { DiagnosticsSnapshot, MetricsSnapshot } from '@/api/diagnostics'
import {
  cardSentenceKey,
  counter,
  counterChildren,
  counterFamilyTotal,
  formatBreakdownList,
  gauge,
  histogram,
  lifecycleCardBreakdown,
  nodeSaveFailures,
  panelOpRows,
  quantileReading,
  ratePerHour,
  ratePerPoll,
  stageGroups,
  writeReasonGroups,
  type CardSummary,
  type PanelFacts,
  type RawFamily,
  type WindowMode,
} from '@/utils/diagnostics'
import type { CardId } from '@/utils/diagnosticsCatalog'
import KpiTile, { KpiGrid } from './KpiTile'
import PanelOpTable from './PanelOpTable'
import StageBar from './StageBar'
import SubsystemCard from './SubsystemCard'
import SystemInfo from './SystemInfo'
import type { DiagFormat } from './useDiagFormat'

// THE SEVEN AREAS, IN A FIXED ORDER. Each card says one plain sentence about
// its state and shows two to four figures with what they are out of. The
// figures are withheld in the blackout (a zero on screen gets read, however
// grey) and on a card proven not in use; an inhibited card keeps its figures,
// uncoloured, because they are still what was recorded.

const WIDE: ReadonlySet<CardId> = new Set(['poll', 'lifecycle'])

interface Ctx {
  m: MetricsSnapshot
  mode: WindowMode
  intervalMs: number
  expected: number | null
  facts: PanelFacts
  fmt: DiagFormat
}

const val = (m: MetricsSnapshot, name: string) => counter(m, name)?.value ?? 0

function Line({ children }: { children: ReactNode }) {
  const md = useTheme().palette.md
  return <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mt: 0.5 }}>{children}</Typography>
}

/** The values each card's sentence interpolates. */
function sentenceValues(id: CardId, c: Ctx): Record<string, string> {
  const { m, fmt } = c
  const polls = val(m, 'psp_poll_total')
  const common = {
    remaining: fmt.duration(Math.max(0, 3 * c.intervalMs - m.window_ms)),
  }
  switch (id) {
    case 'poll':
      return { ...common, interval: fmt.duration(c.intervalMs), polls: fmt.count(polls) }
    case 'lifecycle': {
      const total = val(m, 'psp_lifecycle_sync_total')
      return { ...common, checks: fmt.count(total), skipped: fmt.pct(val(m, 'psp_lifecycle_sync_skipped_total'), total) }
    }
    case 'floor': {
      const pushes = val(m, 'psp_push_client_config_total')
      const perPoll = ratePerPoll(pushes, polls)
      return { ...common, count: fmt.count(pushes), perPoll: perPoll === null ? '—' : fmt.rate(perPoll) }
    }
    case 'panel_api': {
      const ops = counterFamilyTotal(m, 'psp_panel_op_total')
      return { ...common, count: fmt.count(ops), pct: fmt.pct(counterFamilyTotal(m, 'psp_panel_op_error_total'), ops) }
    }
    case 'liveip': {
      const judged = ratePerPoll(histogram(m, 'psp_user_live_ips')?.count ?? 0, polls)
      return { ...common, judged: judged === null ? '—' : fmt.rate(judged) }
    }
    case 'node':
      return { ...common, count: fmt.count(val(m, 'psp_node_host_report_total{outcome=accepted}')) }
    case 'sso':
      return { ...common, count: fmt.count(counterFamilyTotal(m, 'psp_saml_acs_failure_total')) }
  }
}

function PollBody({ c }: { c: Ctx }) {
  const { m, fmt } = c
  const { t } = fmt
  const polls = val(m, 'psp_poll_total')
  const pollMs = histogram(m, 'psp_poll_ms')
  const reading = quantileReading(pollMs)
  const q = fmt.quantile(reading, 'ms')
  // The p95's share of the interval says how close a poll comes to running
  // into the next one; withheld with the p95 itself when it is beyond the
  // buckets.
  const durationCaption = reading.kind === 'ok' && !reading.over && c.intervalMs > 0
    ? `${q.caption} · ${t('admin:diagnostics.fmt.pct_of_interval', { pct: fmt.pct(reading.p95, c.intervalMs) })}`
    : q.caption
  const users = histogram(m, 'psp_poll_users')
  const panels = histogram(m, 'psp_poll_panels')
  const active = histogram(m, 'psp_poll_active_users')
  const stages = stageGroups(m)
  return (
    <>
      <KpiGrid>
        <KpiTile label={t('admin:diagnostics.cards.poll.kpi.polls')}
          value={t('admin:diagnostics.fmt.polls', { count: fmt.count(polls) })}
          caption={c.expected !== null && c.expected > 0
            ? t('admin:diagnostics.cards.poll.kpi.polls_expected', { expected: fmt.count(c.expected) })
            : undefined} />
        <KpiTile label={t('admin:diagnostics.cards.poll.kpi.duration')} value={q.value} caption={durationCaption} />
        {users && users.count > 0 && (
          <KpiTile label={t('admin:diagnostics.cards.poll.kpi.scope')}
            value={t('admin:diagnostics.cards.poll.kpi.scope_value', {
              users: fmt.count(Math.round(users.mean)), panels: fmt.count(Math.round(panels?.mean ?? 0)),
            })}
            caption={active && active.count > 0
              ? t('admin:diagnostics.cards.poll.kpi.scope_active', { active: fmt.count(Math.round(active.mean)) })
              : undefined} />
        )}
      </KpiGrid>
      {stages && <StageBar groups={stages} fmt={fmt} />}
    </>
  )
}

function LifecycleBody({ c }: { c: Ctx }) {
  const theme = useTheme()
  const md = theme.palette.md
  const { m, fmt } = c
  const { t } = fmt
  const total = val(m, 'psp_lifecycle_sync_total')
  const errors = val(m, 'psp_lifecycle_sync_error_total')
  const perHour = ratePerHour(total, m.window_ms)
  const notProvisioned = val(m, 'psp_lifecycle_sync_not_provisioned_total')
  const ip = val(m, 'psp_capability_gap_total{capability=client.iplimit}')
  const device = val(m, 'psp_capability_gap_total{capability=client.devicelimit}')
  return (
    <>
      <KpiGrid>
        <KpiTile label={t('admin:diagnostics.cards.lifecycle.kpi.checks')}
          value={t('admin:diagnostics.fmt.times', { count: fmt.count(total) })}
          caption={perHour === null ? undefined : t('admin:diagnostics.fmt.per_hour_times', { rate: fmt.rate(perHour) })} />
        <KpiTile label={t('admin:diagnostics.cards.lifecycle.kpi.skipped')}
          value={fmt.pct(val(m, 'psp_lifecycle_sync_skipped_total'), total)}
          caption={t('admin:diagnostics.cards.lifecycle.kpi.skipped_caption')} />
        <KpiTile label={t('admin:diagnostics.cards.lifecycle.kpi.writes')}
          value={t('admin:diagnostics.fmt.times', { count: fmt.count(val(m, 'psp_lifecycle_sync_write_total')) })}
          caption={t('admin:diagnostics.cards.lifecycle.kpi.writes_caption')} />
        <KpiTile label={t('admin:diagnostics.cards.lifecycle.kpi.errors')}
          value={t('admin:diagnostics.fmt.times', { count: fmt.count(errors) })}
          color={errors > 0 ? md.error : undefined}
          caption={t('admin:diagnostics.cards.lifecycle.kpi.errors_caption', { pct: fmt.pct(errors, total) })} />
      </KpiGrid>
      {notProvisioned > 0 && <Line>{t('admin:diagnostics.cards.lifecycle.not_provisioned', { count: fmt.count(notProvisioned) })}</Line>}
      {lifecycleCardBreakdown(m).map(line => (
        <Line key={line.key}>{t(`admin:diagnostics.${line.key}`, { list: formatBreakdownList(line.items, line.group, fmt.label, fmt.count) })}</Line>
      ))}
      <Box sx={{ mt: 1.5 }}>
        <Typography variant="caption" sx={{ display: 'block', fontWeight: 600 }}>{t('admin:diagnostics.cards.lifecycle.reasons_title')}</Typography>
        <Box component="ul" sx={{ listStyle: 'none', p: 0, m: 0, display: 'flex', flexWrap: 'wrap', columnGap: 2, rowGap: 0.25 }}>
          {writeReasonGroups(m).map(g => (
            <Tooltip key={g.group}
              title={g.reasons.map(r => `${fmt.label('write_reason', r.value)} (${r.value}) ${fmt.count(r.count)}`).join(' · ')}>
              <Typography component="li" variant="body2" sx={{ fontVariantNumeric: 'tabular-nums' }}>
                {`${t(`admin:diagnostics.labels.write_reason_group.${g.group}`)} ${fmt.count(g.count)}`}
              </Typography>
            </Tooltip>
          ))}
        </Box>
      </Box>
      <Box sx={{ mt: 1.5 }}>
        <Typography variant="caption" sx={{ display: 'block', fontWeight: 600 }}>{t('admin:diagnostics.cards.lifecycle.limits.title')}</Typography>
        <Typography variant="body2">
          {ip > 0
            ? t('admin:diagnostics.cards.lifecycle.limits.ip_gaps', { count: fmt.count(ip) })
            : t('admin:diagnostics.cards.lifecycle.limits.ip_none')}
        </Typography>
        <Typography variant="body2">
          {t('admin:diagnostics.cards.lifecycle.limits.device')}
          {device > 0 ? ` ${t('admin:diagnostics.cards.lifecycle.limits.device_gaps', { count: fmt.count(device) })}` : ''}
        </Typography>
        <Typography variant="caption" sx={{ color: md.onSurfaceVariant }}>{t('admin:diagnostics.cards.lifecycle.limits.servers_note')}</Typography>
      </Box>
    </>
  )
}

function FloorBody({ c }: { c: Ctx }) {
  const { m, fmt } = c
  const { t } = fmt
  const polls = val(m, 'psp_poll_total')
  const pushes = val(m, 'psp_push_client_config_total')
  const errors = val(m, 'psp_push_client_config_error_total')
  const perPoll = ratePerPoll(pushes, polls)
  // The queue wait is read by its 95th percentile alone: a typical wait of
  // zero is the normal case and says nothing.
  const wait = fmt.quantile(quantileReading(histogram(m, 'psp_push_sem_wait_ms')), 'ms')
  return (
    <KpiGrid>
      <KpiTile label={t('admin:diagnostics.cards.floor.kpi.pushes')}
        value={t('admin:diagnostics.fmt.times', { count: fmt.count(pushes) })}
        caption={perPoll === null ? undefined : t('admin:diagnostics.fmt.per_poll', { rate: fmt.rate(perPoll) })} />
      <KpiTile label={t('admin:diagnostics.cards.floor.kpi.errors')}
        value={t('admin:diagnostics.fmt.times', { count: fmt.count(errors) })}
        caption={t('admin:diagnostics.cards.floor.kpi.errors_caption', { pct: fmt.pct(errors, pushes) })} />
      <KpiTile label={t('admin:diagnostics.cards.floor.kpi.concurrency')}
        value={t('admin:diagnostics.cards.floor.kpi.concurrency_value', {
          peak: fmt.count(gauge(m, 'psp_push_sem_inflight')?.peak ?? 0),
          capacity: fmt.count(gauge(m, 'psp_push_sem_capacity')?.value ?? 0),
        })}
        caption={t('admin:diagnostics.cards.floor.kpi.concurrency_caption', { waiting: fmt.count(gauge(m, 'psp_push_sem_waiting')?.peak ?? 0) })} />
      <KpiTile label={t('admin:diagnostics.cards.floor.kpi.wait')}
        value={wait.caption ?? wait.value} />
      <KpiTile label={t('admin:diagnostics.cards.floor.kpi.backlog')}
        value={t('admin:diagnostics.fmt.polls', { count: fmt.count(val(m, 'psp_push_sem_carryover_total')) })}
        caption={t('admin:diagnostics.cards.floor.kpi.backlog_caption', { suppressed: fmt.count(val(m, 'psp_push_suppressed_total')) })} />
    </KpiGrid>
  )
}

function LiveIPBody({ c }: { c: Ctx }) {
  const md = useTheme().palette.md
  const { m, fmt } = c
  const { t } = fmt
  const judged = ratePerPoll(histogram(m, 'psp_user_live_ips')?.count ?? 0, val(m, 'psp_poll_total'))
  const risk = counterChildren(m, 'psp_risk_refresh_total')
  const riskTotal = risk.reduce((n, r) => n + r.count, 0)
  const riskOk = risk.find(r => r.value === 'ok')?.count ?? 0
  return (
    <>
      <KpiGrid>
        <KpiTile label={t('admin:diagnostics.cards.liveip.kpi.judged')}
          value={judged === null ? '—' : t('admin:diagnostics.cards.liveip.kpi.judged_value', { count: fmt.rate(judged) })} />
        <KpiTile label={t('admin:diagnostics.cards.liveip.kpi.incomplete')}
          value={t('admin:diagnostics.fmt.occurrences', { count: fmt.count(val(m, 'psp_live_ip_users_incomplete_total')) })}
          caption={t('admin:diagnostics.cards.liveip.kpi.incomplete_caption')} />
        <KpiTile label={t('admin:diagnostics.cards.liveip.kpi.risk')}
          value={riskTotal > 0
            ? t('admin:diagnostics.cards.liveip.kpi.risk_value', { ok: fmt.count(riskOk), total: fmt.count(riskTotal) })
            : t('admin:diagnostics.cards.liveip.kpi.risk_none')} />
        <KpiTile label={t('admin:diagnostics.cards.liveip.kpi.records')}
          value={t('admin:diagnostics.cards.liveip.kpi.records_value', {
            history: fmt.count(val(m, 'psp_connection_history_write_errors_total')),
            flags: fmt.count(val(m, 'psp_flag_record_write_errors_total')),
          })} />
        <KpiTile label={t('admin:diagnostics.cards.liveip.kpi.auto')}
          value={t('admin:diagnostics.cards.liveip.kpi.auto_value', {
            suspend: fmt.count(val(m, 'psp_geo_auto_suspension_total{outcome=suspend_error}')),
            lift: fmt.count(val(m, 'psp_geo_auto_suspension_total{outcome=lift_error}')),
          })} />
      </KpiGrid>
      {c.facts.complete && c.facts.types.has('sui') && (
        <Typography variant="caption" sx={{ display: 'block', color: md.onSurfaceVariant }}>{t('admin:diagnostics.cards.liveip.sui_note')}</Typography>
      )}
    </>
  )
}

function NodeBody({ c }: { c: Ctx }) {
  const { m, fmt } = c
  const { t } = fmt
  const reports = counterChildren(m, 'psp_node_host_report_total')
  const accepted = reports.find(r => r.value === 'accepted')?.count ?? 0
  const other = reports.reduce((n, r) => n + (r.value === 'accepted' ? 0 : r.count), 0)
  const storage = nodeSaveFailures(m)
  const persist = fmt.quantile(quantileReading(histogram(m, 'psp_node_host_persist_ms')), 'ms')
  return (
    <KpiGrid>
      <KpiTile label={t('admin:diagnostics.cards.node.kpi.accepted')}
        value={t('admin:diagnostics.cards.node.kpi.accepted_value', { count: fmt.count(accepted) })}
        caption={t('admin:diagnostics.cards.node.kpi.accepted_caption', { other: fmt.count(other) })} />
      <KpiTile label={t('admin:diagnostics.cards.node.kpi.refused')}
        value={t('admin:diagnostics.fmt.times', { count: fmt.count(counterFamilyTotal(m, 'psp_node_sync_refused_total')) })}
        caption={t('admin:diagnostics.cards.node.kpi.refused_caption', {
          proto: fmt.count(val(m, 'psp_node_sync_refused_total{reason=protocol_generation}')),
          invalid: fmt.count(val(m, 'psp_node_sync_refused_total{reason=report_invalid}')),
        })} />
      <KpiTile label={t('admin:diagnostics.cards.node.kpi.storage')}
        value={t('admin:diagnostics.fmt.times', { count: fmt.count(storage.failures) })}
        caption={storage.withHistory > 0
          ? t('admin:diagnostics.cards.node.kpi.storage_caption', { history: fmt.count(storage.withHistory) })
          : undefined} />
      <KpiTile label={t('admin:diagnostics.cards.node.kpi.persist')}
        value={persist.caption ?? persist.value}
        caption={t('admin:diagnostics.cards.node.kpi.persist_caption')} />
    </KpiGrid>
  )
}

function SsoBody({ c }: { c: Ctx }) {
  const { m, fmt } = c
  const { t } = fmt
  const saml = counterChildren(m, 'psp_saml_acs_failure_total')
  return (
    <KpiGrid>
      <KpiTile label={t('admin:diagnostics.cards.sso.kpi.saml')}
        value={t('admin:diagnostics.fmt.times', { count: fmt.count(saml.reduce((n, r) => n + r.count, 0)) })}
        caption={saml.length > 0 ? (
          <Stack component="span" sx={{ display: 'flex' }}>
            {saml.map(r => <span key={r.value}>{`${fmt.label('saml', r.value)} ${fmt.count(r.count)}`}</span>)}
          </Stack>
        ) : undefined} />
      <KpiTile label={t('admin:diagnostics.cards.sso.kpi.claims')}
        value={t('admin:diagnostics.cards.sso.kpi.claims_value', {
          role: fmt.count(val(m, 'psp_sso_claim_silent_total{kind=role}')),
          group: fmt.count(val(m, 'psp_sso_claim_silent_total{kind=group}')),
        })} />
    </KpiGrid>
  )
}

function body(id: CardId, c: Ctx): ReactNode {
  switch (id) {
    case 'poll': return <PollBody c={c} />
    case 'lifecycle': return <LifecycleBody c={c} />
    case 'floor': return <FloorBody c={c} />
    case 'panel_api': return <PanelOpTable rows={panelOpRows(c.m)} fmt={c.fmt} />
    case 'liveip': return <LiveIPBody c={c} />
    case 'node': return <NodeBody c={c} />
    case 'sso': return <SsoBody c={c} />
  }
}

/** Whether a card shows its figures at all. */
function showsFigures(card: CardSummary, mode: WindowMode): boolean {
  if (mode === 'blackout') return false
  return card.state !== 'not_applicable' && card.state !== 'none'
}

export default function AreaCards({
  snap, cards, mode, intervalMs, settingsIntervalMs, expected, facts, families, fmt, panelTz, expanded, onToggle,
}: {
  snap: DiagnosticsSnapshot
  cards: CardSummary[]
  mode: WindowMode
  intervalMs: number
  settingsIntervalMs: number
  expected: number | null
  facts: PanelFacts
  families: RawFamily[]
  fmt: DiagFormat
  panelTz: string
  expanded: ReadonlySet<CardId>
  onToggle: (id: CardId) => void
}) {
  const { t } = fmt
  const byId = Object.fromEntries(cards.map(card => [card.id, card])) as Record<CardId, CardSummary>
  const c: Ctx = { m: snap.metrics, mode, intervalMs, expected, facts, fmt }
  const pollState = t(`admin:diagnostics.state.${byId.poll.state}`)

  return (
    <Box component="section" sx={{ mt: 1 }}>
      <Typography variant="h6" component="h2" sx={{ mb: 1 }}>{t('admin:diagnostics.cards.section_title')}</Typography>
      <Box sx={{ display: 'grid', gap: 2, gridTemplateColumns: { xs: 'minmax(0, 1fr)', md: 'repeat(2, minmax(0, 1fr))' } }}>
        {cards.map(card => {
          const key = cardSentenceKey(card, mode, intervalMs > 0)
          const depends = card.id === 'floor' || card.id === 'liveip'
          return (
            <SubsystemCard key={card.id} card={card} wide={WIDE.has(card.id)}
              sentence={t(`admin:diagnostics.${key}`, sentenceValues(card.id, c))}
              extra={depends ? <Line>{t('admin:diagnostics.cards.common.depends', { state: pollState })}</Line> : undefined}
              families={families.filter(f => f.card === card.id)} windowMs={snap.metrics.window_ms}
              fmt={fmt} expanded={expanded.has(card.id)} onToggle={() => onToggle(card.id)}>
              {showsFigures(card, mode) ? body(card.id, c) : null}
            </SubsystemCard>
          )
        })}
        <SystemInfo snap={snap} settingsIntervalMs={settingsIntervalMs} panelTz={panelTz} fmt={fmt} />
      </Box>
    </Box>
  )
}
