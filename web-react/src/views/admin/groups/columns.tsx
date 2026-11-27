import type { TFunction } from 'i18next'
import type { ColumnDef } from '@tanstack/react-table'

import type { Group } from '@/api/types'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { StatusBadge } from '@/components/common/StatusBadge'
import { StatusDot } from '@/components/common/StatusDot'
import { Badge } from '@/components/ui/badge'
import { RowActions } from '../subscription/RowActions'
import { useMatchCount, useOverrideCount } from './useGroupStats'

export interface GroupColumnCtx {
  t: TFunction
  canConfig: boolean
  busy: ReadonlySet<number>
  onEdit: (g: Group) => void
  menuOf: (g: Group) => MoreMenuItem[]
}

const FIT = 'max-w-0 overflow-hidden text-ellipsis'
const DASH = <span className="text-muted-foreground">—</span>

/** "100 GB", "unlimited", or nothing at all for a value the group does not state (null). */
function limitText(t: TFunction, value: number | null | undefined, gb = false): string | null {
  if (value === null || value === undefined) return null
  if (value === 0) return t('admin:groups.list.unlimited')
  return gb ? t('admin:groups.list.gb', { gb: Number(value.toFixed(2)) }) : String(value)
}

/** What a group's members inherit when they set nothing themselves, as one line; every limit the group does not state is left out. */
export function quotaSummary(t: TFunction, g: Pick<Group, 'traffic_limit_gb' | 'ip_limit' | 'device_limit'>): string | null {
  const parts = [
    ['quota_traffic', limitText(t, g.traffic_limit_gb, true)],
    ['quota_ip', limitText(t, g.ip_limit)],
    ['quota_devices', limitText(t, g.device_limit)],
  ] as const
  const out = parts.filter(([, v]) => v !== null).map(([k, v]) => t(`admin:groups.list.${k}`, { value: v }))
  return out.length > 0 ? out.join(' · ') : null
}

/** The allowlist badge on a group's name: only when the group has one that is recording or enforcing. */
export function DestBadge({ state, t }: { state: Group['dest_state']; t: TFunction }) {
  if (!state || state === 'off') return null
  return <StatusBadge tone="info" quiet={false} testId="allowlist-state" label={t(`admin:groups.destination.${state === 'report' ? 'badge_report' : 'badge_on'}`)} />
}

/** Drawn only for rows on screen, which is what keeps the per-group reads few (see useGroupStats). */
function MatchCell({ g, t }: { g: Group; t: TFunction }) {
  const m = useMatchCount(g)
  if (!m) return DASH
  // Nothing matching is the one result worth words: those members get an empty subscription.
  if (m.nodes === 0) return <StatusBadge tone="warning" label={t('admin:groups.preview.none_short')} />
  return <span title={t('admin:groups.list.matches_title', { visible: m.visibleLines, total: m.totalLines })}>{t('admin:groups.list.matches', { nodes: m.nodes, lines: m.visibleLines })}</span>
}

function OverridesCell({ g }: { g: Group }) {
  const n = useOverrideCount(g)
  return n === undefined || n === 0 ? DASH : <>{n}</>
}

/** The conditions of a filter as small badges: the first three, then "+n". */
function Conditions({ g, t }: { g: Group; t: TFunction }) {
  if (g.tag_filter.all) return <Badge variant="secondary">{t('admin:groups.tag.all')}</Badge>
  const tags = g.tag_filter.tags ?? []
  if (tags.length === 0) return DASH
  const isAny = g.tag_filter.mode === 'any'
  const full = tags.join(isAny ? ' OR ' : ' AND ')
  return (
    <span className="flex min-w-0 flex-nowrap items-center gap-1 overflow-hidden" title={full}>
      {/* The conjunction first: whether the conditions all have to match or any one will is the whole meaning of the list. */}
      <Badge variant="outline" className="shrink-0">{isAny ? t('admin:groups.mode.any_badge') : t('admin:groups.mode.all_badge')}</Badge>
      {tags.slice(0, 2).map(tag => <Badge key={tag} variant="secondary" className="min-w-0 max-w-32 justify-start truncate">{tag}</Badge>)}
      {tags.length > 2 && <span className="shrink-0 text-xs text-muted-foreground">{t('admin:groups.list.conditions_more', { count: tags.length - 2 })}</span>}
    </span>
  )
}

/** One stable array per ctx (DataTable memoises its rows on the columns' identity). */
export function groupColumns(c: GroupColumnCtx): ColumnDef<Group, any>[] {
  const { t } = c
  const cols: ColumnDef<Group, any>[] = [
    { id: 'name', header: t('admin:groups.table.name'), accessorFn: r => r.name, meta: { hideable: false, label: t('admin:groups.table.name'), className: `${FIT} w-[24%]` },
      cell: ctx => {
        const g = ctx.row.original
        return (
          <div className="flex min-w-0 flex-col gap-0.5" title={g.remark ? `${g.name}\n${g.remark}` : g.name}>
            <span className="min-w-0 truncate font-medium">{g.name}</span>
            {(g.remark || (g.dest_state && g.dest_state !== 'off')) && (
              <span className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
                <DestBadge state={g.dest_state} t={t} />
                {g.remark && <span className="min-w-0 truncate">{g.remark}</span>}
              </span>
            )}
          </div>
        )
      } },
    { id: 'slug', header: t('admin:groups.table.slug'), accessorFn: r => r.slug, meta: { label: t('admin:groups.table.slug'), className: 'font-mono text-xs whitespace-nowrap text-muted-foreground' },
      cell: ctx => <span title={ctx.row.original.slug} className="inline-block max-w-56 truncate align-bottom">{ctx.row.original.slug}</span> },
    { id: 'tag_filter', header: t('admin:groups.table.tag_filter'), enableSorting: false, meta: { label: t('admin:groups.table.tag_filter'), className: `${FIT} w-[20%]` },
      cell: ctx => <Conditions g={ctx.row.original} t={t} /> },
  ]
  // The preview behind this column is admin-only on the server: an operator reads the list without it.
  if (c.canConfig) {
    cols.push({ id: 'nodes', header: t('admin:groups.table.nodes'), enableSorting: false, meta: { label: t('admin:groups.table.nodes'), className: 'w-40 whitespace-nowrap' },
      cell: ctx => <MatchCell g={ctx.row.original} t={t} /> })
  }
  cols.push(
    { id: 'members', header: t('admin:groups.table.members'), accessorFn: r => r.members, meta: { label: t('admin:groups.table.members'), align: 'right', className: 'w-24 tabular-nums' } },
    { id: 'quota', header: t('admin:groups.table.quota'), enableSorting: false, meta: { label: t('admin:groups.table.quota'), className: `${FIT} w-[18%]` },
      cell: ctx => {
        const text = quotaSummary(t, ctx.row.original)
        return text ? <span title={text}>{text}</span> : <span className="text-muted-foreground">{t('admin:groups.list.quota_none')}</span>
      } },
    { id: 'two_factor', header: t('admin:groups.table.two_factor'), accessorFn: r => (r.require_2fa ? 1 : 0), meta: { label: t('admin:groups.table.two_factor'), className: 'w-28 whitespace-nowrap' },
      cell: ctx => (ctx.row.original.require_2fa ? <StatusDot tone="info" label={t('admin:groups.list.two_factor_required')} /> : DASH) },
  )
  if (c.canConfig) {
    cols.push({ id: 'overrides', header: t('admin:groups.table.overrides'), enableSorting: false, meta: { label: t('admin:groups.table.overrides'), align: 'right', className: 'w-24 tabular-nums' },
      cell: ctx => <OverridesCell g={ctx.row.original} /> })
    cols.push({ id: 'actions', header: '', enableSorting: false, meta: { hideable: false, align: 'right', label: t('admin:groups.table.actions'), className: 'w-20' },
      cell: ctx => {
        const g = ctx.row.original
        return <RowActions name={g.name} editLabel={t('admin:groups.edit_title')} menuLabel={t('admin:groups.row_menu', { name: g.name })}
          busy={c.busy.has(g.id)} onEdit={() => c.onEdit(g)} menu={c.menuOf(g)} />
      } })
  }
  return cols
}
