import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import type { DestPolicy } from '@/api/accessControl'
import { AccessBadge } from '@/components/access/AccessBadge'
import { MoreMenu, type MoreMenuItem } from '@/components/common/MoreMenu'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { IconChevronRight } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { policyHitsView, type PolicyHitsView } from '@/utils/accessControl'
import { formatRelativeTimeShort } from '@/utils/relativeTime'
import { matchSummary, summaryText } from './matchSummary'

export type RowMove = 'up' | 'down' | 'top' | 'bottom'

const R = 'admin:access_control.policies.row.'

/**
 * One policy: two lines. The first is the name and its badges, with the
 * switch and the menu at the right; the second says what it matches and for
 * whom. A click anywhere else opens the editor. A policy that is off is
 * drawn dimmed and says "paused" in words as well.
 *
 * The order is shown, not only changed: the menu's up / down / top / bottom
 * are disabled at the ends, and the rail names the step the row sits in.
 */
export default function PolicyRow({
  policy, index, count, listNames, scopeText, busy, moving, highlighted, hitDays, onOpen, onToggle, onMove, onDuplicate, onDelete,
  onReport, onConvert, onViewRecords, now, notDeployed = 0,
}: {
  policy: DestPolicy
  /** 0-based, within its action's section. */
  index: number
  count: number
  listNames: Record<number, string>
  scopeText: string
  /** This row's switch is saving. */
  busy: boolean
  /** A reorder is in flight: no other move may start. */
  moving: boolean
  highlighted: boolean
  /** The window the hit counters are over (`hit_window_days`, already held to the retention). */
  hitDays: number
  onOpen: () => void
  onToggle: () => void
  onMove: (m: RowMove) => void
  onDuplicate: () => void
  onDelete: () => void
  /** "Change to report only" (a block policy that is not already). */
  onReport: () => void
  /** "Turn into a block…" (a report-only row). */
  onConvert: () => void
  /** Open the records filtered to this policy. */
  onViewRecords: () => void
  /** The instant the list was read at, so every row's "2 hours ago" is measured from the same one. */
  now?: number
  /** Nodes not running the report-only rules (`reportNotDeployedCount`). */
  notDeployed?: number
}) {
  const { t } = useTranslation(['admin'])
  const phone = useMediaQuery('(max-width: 639px)')
  // The switch means "is it deployed": on and report-only are lit, off is not.
  const off = policy.state === 'off'
  const report = policy.state === 'report'
  const hits = policyHitsView(policy, hitDays, notDeployed)

  const summary = summaryText(t, matchSummary({
    listNames: policy.list_ids.map(id => listNames[id] ?? t(`${R}deleted_list`)),
    cidrs: policy.inline.cidrs?.length ?? 0,
    private: policy.inline.private === true,
    bittorrent: (policy.inline.protocols ?? []).includes('bittorrent'),
    ports: policy.inline.ports ?? '',
    network: policy.inline.network ?? '',
  }))
  const notReady = policy.list_states.some(s => s.state === 'not_ready')
  const emptyList = policy.list_states.some(s => s.state === 'empty')
  const over = policy.note?.state === 'empty' && !notReady

  const menu: MoreMenuItem[] = [
    { id: 'edit', label: t(`${R}menu_edit`), onSelect: onOpen },
    { id: 'duplicate', label: t(`${R}menu_duplicate`), onSelect: onDuplicate },
    { id: 'up', label: t(`${R}menu_up`), onSelect: () => onMove('up'), disabled: moving || index === 0 },
    { id: 'down', label: t(`${R}menu_down`), onSelect: () => onMove('down'), disabled: moving || index === count - 1 },
    { id: 'top', label: t(`${R}menu_top`), onSelect: () => onMove('top'), disabled: moving || index === 0 },
    { id: 'bottom', label: t(`${R}menu_bottom`), onSelect: () => onMove('bottom'), disabled: moving || index === count - 1 },
    ...(policy.action === 'block' && !report ? [{ id: 'report', label: t(`${R}menu_report`), onSelect: onReport }] : []),
    ...(policy.action === 'block' ? [{ id: 'records', label: t(`${R}menu_records`), onSelect: onViewRecords }] : []),
    { id: 'delete', label: t(`${R}menu_delete`), onSelect: onDelete, destructive: true },
  ]
  const convert = (
    <Button size="sm" variant="outline" onClick={onConvert} className="whitespace-nowrap"
      aria-label={t(`${R}convert_aria`, { name: policy.name })}>
      {t(`${R}convert`)}
    </Button>
  )

  return (
    <div data-policy-id={policy.id} data-testid="policy-row" onClick={onOpen}
      className={cn(
        'min-w-0 cursor-pointer rounded-xl border bg-card p-3 transition-colors hover:bg-muted/40',
        highlighted && 'border-primary ring-2 ring-primary/30',
      )}>
      <div className="flex items-center gap-2">
        <div className={cn('flex min-w-0 flex-1 flex-wrap items-center gap-1.5', off && 'opacity-60')}>
          <span className="text-sm font-semibold [overflow-wrap:anywhere]">{policy.name}</span>
          {/* A report-only policy blocks nothing: its badge is its state, never the block badge. */}
          {report
            ? <span data-testid="policy-state-badge" data-state="report"><AccessBadge kind="would_deny" label={t('admin:access_control.policies.state.report')} /></span>
            : <AccessBadge kind={policy.action === 'block' ? 'block' : 'allow'} label={t(`admin:access_control.policies.action.${policy.action}`)} />}
          {/* Report-only hits never count: the badge shows only while the policy blocks. */}
          {policy.action === 'block' && !report && policy.counts_as_risk === true && (
            <span data-testid="policy-risk-badge"><AccessBadge kind="none" label={t(`${R}counts_as_risk`)} /></span>
          )}
          {off && <span data-testid="policy-state-badge" data-state="off"><AccessBadge kind="paused" label={t(`${R}paused`)} /></span>}
          {notReady && <span title={t(`${R}not_ready_hint`)}><AccessBadge kind="attention" label={t(`${R}not_ready`)} /></span>}
          {!notReady && emptyList && <AccessBadge kind="none" label={t(`${R}empty_list`)} />}
          {policy.scope_missing && <AccessBadge kind="failing" label={t(`${R}scope_missing_badge`)} />}
          {over && <AccessBadge kind="none" label={t(`${R}nothing_left`)} />}
        </div>
        <div onClick={e => e.stopPropagation()} className="flex shrink-0 items-center gap-2">
          {report && !phone && convert}
          {/* Disabled while saving, never swapped for a spinner: a swapped-out control drops keyboard focus. */}
          <Switch checked={!off} disabled={busy} onCheckedChange={onToggle} aria-busy={busy || undefined}
            aria-label={t(`${R}toggle`, { name: policy.name })} />
          <MoreMenu items={menu} label={t(`${R}menu`, { name: policy.name })} />
        </div>
      </div>
      <p className={cn('mt-1 text-sm text-muted-foreground [overflow-wrap:anywhere]', off && 'opacity-60')}>
        {[summary || t(`${R}no_condition`), scopeText].join(' · ')}
        {policy.scope_missing ? ` · ${t(`${R}scope_missing`)}` : ''}
        {notReady ? ` · ${t(`${R}not_ready_detail`)}` : ''}
      </p>
      {(hits.kind !== 'none' || (report && phone)) && (
        <div onClick={e => e.stopPropagation()} className={cn('mt-1 flex flex-wrap items-center gap-2', off && 'opacity-60')}>
          <HitsLine hits={hits} onViewRecords={onViewRecords} now={now} />
          {report && phone && convert}
        </div>
      )}
    </div>
  )
}

/**
 * What a row says of its hits (plan S2). A count is a link to the records
 * filtered to the policy; a report-only count is drawn in the "would be
 * denied" tone, the one row of the closed table that means "what it WOULD do".
 * No recording is a dash with its reason, never a 0.
 */
function HitsLine({ hits, onViewRecords, now }: { hits: PolicyHitsView; onViewRecords: () => void; now?: number }) {
  const { t, i18n } = useTranslation(['admin'])
  const num = new Intl.NumberFormat(i18n.language)
  const ago = (ms: number | null) => (ms === null ? '' : formatRelativeTimeShort((now ?? Date.now()) - ms, t))
  const wrap = (kind: string, children: ReactNode) => (
    <span data-testid="policy-hits" data-kind={kind} className="inline-flex min-w-0 flex-wrap items-center gap-1.5">{children}</span>
  )
  const link = (label: string, aria: string) => (
    <button type="button" onClick={onViewRecords} title={t(`${R}view_records`)} aria-label={`${t(`${R}view_records`)}: ${aria}`}
      className="inline-flex min-h-6 items-center rounded-sm text-left text-[13px] text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 pointer-coarse:min-h-8">
      {label}<IconChevronRight aria-hidden className="size-4" />
    </button>
  )
  const shedNote = (n: number | undefined) => (n ? t(`${R}not_deployed_nodes`, { count: n }) : '')
  const quiet = (text: string) => <span className="text-sm text-muted-foreground">{text}</span>
  switch (hits.kind) {
    case 'none': return null
    case 'unavailable':
      return wrap('unavailable', (
        <span tabIndex={0} title={t(`${R}hits_unavailable_tip`)} aria-label={t(`${R}hits_unavailable_tip`)} className="text-sm text-muted-foreground">{t(`${R}hits_unavailable`)}</span>
      ))
    case 'count': {
      const text = t(`${R}hits_count`, { days: hits.days, count: hits.count, n: num.format(hits.count) })
      return wrap('count', link([text, hits.last !== null ? ago(hits.last) : ''].filter(Boolean).join(' · '), text))
    }
    case 'zero':
      return wrap('zero', link([t(`${R}hits_zero`, { days: hits.days }), ago(hits.last)].filter(Boolean).join(' · '), t(`${R}hits_zero`, { days: hits.days })))
    case 'never':
      return wrap('never', <AccessBadge kind="none" label={t(`${R}hits_never`)} />)
    case 'would_block': {
      const text = t(`${R}would_block`, { days: hits.days, count: hits.count, n: num.format(hits.count) }) + shedNote(hits.notDeployed)
      return wrap('would_block', (
        <button type="button" onClick={onViewRecords} title={t(`${R}view_records`)} aria-label={`${t(`${R}view_records`)}: ${text}`} className="rounded-md outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
          <AccessBadge kind="would_deny" label={text} />
        </button>
      ))
    }
    case 'would_block_none':
      return wrap('would_block_none', quiet(t(`${R}would_block_none`, { days: hits.days }) + shedNote(hits.notDeployed)))
  }
}
