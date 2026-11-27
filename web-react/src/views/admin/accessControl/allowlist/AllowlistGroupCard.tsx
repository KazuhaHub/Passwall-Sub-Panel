import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import type { DestGroup } from '@/api/accessControl'
import { MoreMenu } from '@/components/common/MoreMenu'
import { FieldHint } from '@/components/common/HelpTip'
import { Button } from '@/components/ui/button'
import { IconError, IconPending, IconWarning } from '@/lib/icons'
import { cn } from '@/lib/utils'
import StateBadge from './StateBadge'
import { stateDay } from './allowlistState'

const C = 'admin:access_control.allowlist.card.'

export interface AllowlistGroupCardProps {
  group: DestGroup
  now: number
  highlighted?: boolean
  /** Names of the nodes that left this group out for weak sniffing. */
  droppedOn: string[]
  onOpenEntries: (listId: number) => void
  onOpenNodes: () => void
  onReport: () => void
  onViewDenied: () => void
  onTurnOn: () => void
  onBackToReport: () => void
  onTurnOff: () => void
  onEditLists: () => void
  onOpenGroup: () => void
}

/** A line of the card that is a note (a warning or a progress), icon first. */
function Note({ testId, icon, children }: { testId: string; icon: ReactNode; children: ReactNode }) {
  return <p data-testid={testId} className="flex items-start gap-1.5 text-sm">{icon}<span>{children}</span></p>
}

/**
 * ONE ALLOWLIST GROUP (S8): its state, what its list is made of, how many nodes
 * enforce it and what it did or would have done. A group that is off keeps its
 * lists and is a quiet card with one way back. The card is a summary; every
 * change is a button that opens the thing that explains it. The primary action
 * is the one the state calls for (turn on, review); the rest sit in the menu.
 */
export default function AllowlistGroupCard(p: AllowlistGroupCardProps) {
  const { group: g, now } = p
  const { t, i18n } = useTranslation(['admin'])
  const num = new Intl.NumberFormat(i18n.language)
  const off = g.state === 'off'
  const noNodes = !off && g.eligible_nodes === 0
  const sep = t(`${C}list_separator`)
  const link = (label: string, onClick: () => void) => (
    <button type="button" onClick={onClick} className="rounded-sm text-left align-baseline underline-offset-2 hover:underline focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none">{label}</button>
  )
  const listLinks = g.lists.map((l, i) => (
    <span key={l.id}>{i > 0 && sep}{link(t(`${C}list_item`, { name: l.name, entries: num.format(l.entry_count) }), () => p.onOpenEntries(l.id))}</span>
  ))
  if (g.extra_list_id > 0) {
    listLinks.push(<span key="extra">{listLinks.length > 0 && sep}{link(t(`${C}extra_list`), () => p.onOpenEntries(g.extra_list_id))}</span>)
  }
  const warnIcon = <IconWarning aria-hidden className="mt-0.5 size-4 shrink-0 text-warning-ink" />

  // What the menu offers beyond the card's one primary button: every state change stays reachable from here.
  const menu = [
    { id: 'edit', label: t(`${C}edit_lists`), onSelect: p.onEditLists },
    ...(g.state === 'report' ? [{ id: 'on', label: t(`${C}turn_on`), onSelect: p.onTurnOn }] : []),
    ...(g.state === 'on' ? [{ id: 'report', label: t(`${C}back_to_report`), onSelect: p.onBackToReport }] : []),
    { id: 'group', label: t(`${C}open_group`), onSelect: p.onOpenGroup },
    ...(!off ? [{ id: 'off', label: t(`${C}turn_off`), onSelect: p.onTurnOff, destructive: true }] : []),
  ]

  return (
    <section
      data-testid="allowlist-card"
      data-group={g.group_id}
      aria-label={g.group_name}
      className={cn('min-w-0 overflow-hidden rounded-xl bg-card ring-1', p.highlighted ? 'ring-2 ring-brand' : 'ring-foreground/10', off && 'bg-muted/40')}
    >
      {noNodes && (
        <div data-testid="allowlist-no-nodes" className="flex flex-wrap items-center gap-2 bg-destructive/10 px-4 py-2 text-sm">
          <IconError aria-hidden className="size-4 shrink-0 text-destructive" />
          <p className="min-w-0 flex-1">{t(`${C}no_nodes`)}</p>
          <Button size="sm" variant="outline" onClick={p.onOpenNodes}>{t(`${C}no_nodes_action`)}</Button>
        </div>
      )}
      <div className="grid gap-2 p-4">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <h2 className="min-w-0 flex-1 text-base font-semibold [overflow-wrap:anywhere] sm:flex-none">{g.group_name}</h2>
          <div className="order-3 basis-full sm:order-2 sm:basis-auto">
            <StateBadge state={g.state} day={stateDay(g.state_changed_at, now)} />
          </div>
          <div className="order-2 sm:order-3 sm:ml-auto">
            <MoreMenu label={t(`${C}menu`, { name: g.group_name })} items={menu} />
          </div>
        </div>

        <p className="text-sm text-muted-foreground [overflow-wrap:anywhere]">
          {t(`${C}members`, { members: num.format(g.members) })}{' · '}{t(`${C}lists_label`)}{listLinks.length > 0 ? listLinks : t(`${C}lists_none`)}{' · '}{t(`${C}base`, { count: g.base_count })}
        </p>

        {!off && (
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
            <span>{t(`${C}nodes`, { ok: g.eligible_nodes, total: g.total_nodes })}</span>
            {g.ineligible.length > 0 && (
              <FieldHint tone="muted" summary={t(`${C}not_open`, { count: g.ineligible.length })}
                detail={g.ineligible.map(n => t(`${C}not_open_item`, { name: n.name, reason: t(`admin:access_control.allowlist.reason.${n.reason}`) })).join(t(`${C}detail_separator`))} />
            )}
          </div>
        )}
        {!off && g.pending_nodes.length > 0 && (
          <Note testId="allowlist-applying" icon={<IconPending aria-hidden className="mt-0.5 size-4 shrink-0 text-info" />}>
            {t(`${C}applying`, { count: g.pending_nodes.length })}
          </Note>
        )}
        {!off && g.not_ready_lists.length > 0 && (
          <Note testId="allowlist-list-not-ready" icon={warnIcon}>{t(`${C}list_not_ready`, { list: g.not_ready_lists.map(l => l.name).join(sep) })}</Note>
        )}
        {p.droppedOn.length > 0 && (
          <Note testId="allowlist-sniffing-dropped" icon={warnIcon}>{t(`${C}sniffing_dropped`, { nodes: p.droppedOn.join(sep) })}</Note>
        )}
        {g.state === 'report' && g.would_deny_recent && (
          <p className="text-sm">{t(`${C}would_deny`, {
            days: g.would_deny_recent.days, sites: num.format(g.would_deny_recent.sites), times: num.format(g.would_deny_recent.count),
          })}</p>
        )}
        {g.state === 'on' && g.denied_24h !== null && (
          <p className="text-sm">{t(`${C}denied_24h`, { times: num.format(g.denied_24h) })}</p>
        )}

        <div className="flex flex-wrap gap-2 sm:justify-end [&>button]:flex-1 sm:[&>button]:flex-none">
          {g.state === 'on' && <Button size="sm" variant="outline" onClick={p.onViewDenied}>{t(`${C}view_denied`)}</Button>}
          {g.state === 'report' && <Button size="sm" variant="outline" onClick={p.onReport}>{t(`${C}view_report`)}</Button>}
          {g.state === 'report' && <Button size="sm" onClick={p.onTurnOn}>{t(`${C}turn_on`)}</Button>}
          {g.state === 'on' && <Button size="sm" variant="outline" onClick={p.onBackToReport}>{t(`${C}back_to_report`)}</Button>}
          {off && <Button size="sm" variant="outline" onClick={p.onEditLists}>{t(`${C}reenable`)}</Button>}
        </div>
      </div>
    </section>
  )
}
