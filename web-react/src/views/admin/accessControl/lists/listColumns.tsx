import type { ColumnDef } from '@tanstack/react-table'
import type { TFunction } from 'i18next'

import type { DestList, DestPolicyRef } from '@/api/accessControl'
import { AccessBadge } from '@/components/access/AccessBadge'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { MoreMenu } from '@/components/common/MoreMenu'
import { cn } from '@/lib/utils'
import { formatMsDualTz } from '@/utils/datetime'
import { formatRelativeTimeShort } from '@/utils/relativeTime'
import { listErrorKey } from './listError'
import { listStatus } from './listStatus'
import UsedByPopover from './UsedByPopover'

const L = 'admin:access_control.lists.'

/** What a row's handlers and lookups need; the page keeps the handlers in a ref so the columns stay one stable array. */
export interface ListColumnCtx {
  t: TFunction
  /** The language the name column sorts by. */
  lang: string
  usedEnabled: (l: DestList) => boolean
  onOpenEntries: (id: number) => void
  onEdit: (id: number) => void
  onRefresh: (l: DestList) => void
  onDelete: (l: DestList) => void
  onOpenPolicy: (id: number) => void
  onOpenAllowlist: (groupId: number) => void
}

function safeHost(url?: string): string {
  if (!url) return ''
  try { return new URL(url).host } catch { return url }
}

/** "Remote · host", "Community category · name@attr" or "Custom": what kind of list it is, in one line. */
export function kindText(t: TFunction, l: DestList): string {
  if (l.kind === 'remote') return `${t(`${L}kind.remote`)} · ${safeHost(l.source_url)}`
  if (l.kind === 'geosite') return `${t(`${L}kind.geosite`)} · ${l.geosite_category}${l.geosite_attrs?.length ? `@${l.geosite_attrs.join('@')}` : ''}`
  return t(`${L}kind.custom`)
}

/**
 * A list's state in words, and under it why when it failed (the server's text is mapped to a known sentence, never
 * shown raw) or when it was last read. `data-state` and the test id are the hooks the tests and the page read.
 */
export function ListStatusCell({ list, usedByEnabled, t }: { list: DestList; usedByEnabled: boolean; t: TFunction }) {
  const st = listStatus(list, usedByEnabled)
  const ago = list.last_fetched_at ? formatRelativeTimeShort(Date.now() - list.last_fetched_at, t) : ''
  const abs = list.last_fetched_at ? formatMsDualTz(list.last_fetched_at, '') : ''
  const failed = (st.key === 'failed_old' || st.key === 'failed_never') && list.last_error
  let errWords = ''
  if (failed) { const e = listErrorKey(list.last_error); errWords = t(`${L}error.${e.key}`, e.params) }
  const second = failed
    ? <span title={errWords} className="block text-xs text-destructive [overflow-wrap:anywhere]">{errWords}{ago ? ` · ${ago}` : ''}</span>
    : ago ? <span title={abs} className="block text-xs text-muted-foreground">{ago}</span> : null
  return (
    <div className="flex min-w-0 flex-col items-start gap-0.5">
      <span data-testid="list-state" data-state={st.key}><AccessBadge kind={st.tone} label={t(`${L}state.${st.key}`)} /></span>
      {second}
    </div>
  )
}

/** The row menu. A list something uses cannot be deleted: the item says by whom instead of failing later. */
export function listMenu(c: ListColumnCtx, l: DestList): MoreMenuItem[] {
  const { t } = c
  const used = l.used_by.length
  const items: MoreMenuItem[] = [{ id: 'view', label: t(`${L}menu.view`), onSelect: () => c.onOpenEntries(l.id) }]
  if (l.kind !== 'geosite') items.push({ id: 'edit', label: t(`${L}menu.edit`), onSelect: () => c.onEdit(l.id) })
  // Only a remote list refreshes on its own say; a community category moves with the rule data.
  if (l.kind === 'remote') items.push({ id: 'refresh', label: t(`${L}menu.refresh`), onSelect: () => c.onRefresh(l) })
  items.push({
    id: 'delete', destructive: true, disabled: used > 0, onSelect: () => c.onDelete(l),
    label: used > 0 ? t(`${L}menu.delete_blocked`, { reason: t(`${L}menu.delete_in_use`, { count: used }) }) : t(`${L}menu.delete`),
  })
  return items
}

const stop = (e: { stopPropagation: () => void }) => e.stopPropagation()

export function listColumns(c: ListColumnCtx): ColumnDef<DestList, any>[] {
  const { t } = c
  const collator = new Intl.Collator(c.lang)
  return [
    {
      id: 'name', header: t(`${L}cols.name`), meta: { hideable: false, label: t(`${L}cols.name`), className: 'max-w-0 w-[34%]' },
      accessorFn: l => l.name,
      sortingFn: (a, b) => collator.compare(a.original.name, b.original.name),
      cell: ctx => {
        const l = ctx.row.original
        return (
          <div className="flex min-w-0 flex-col gap-0.5" title={l.name}>
            <span className="min-w-0 truncate font-medium">{l.name}</span>
            <span className="min-w-0 truncate text-xs text-muted-foreground">{kindText(t, l)}</span>
          </div>
        )
      },
    },
    {
      id: 'entries', header: t(`${L}cols.entries`), meta: { label: t(`${L}cols.entries`), align: 'right', className: 'w-28 tabular-nums' },
      accessorFn: l => l.entry_count,
      cell: ctx => (ctx.row.original.state === 'pending' ? '—' : new Intl.NumberFormat(c.lang).format(ctx.row.original.entry_count)),
    },
    {
      id: 'status', header: t(`${L}cols.status`), meta: { label: t(`${L}cols.status`), className: 'w-[26%]' },
      accessorFn: l => l.last_fetched_at ?? 0,
      cell: ctx => <ListStatusCell list={ctx.row.original} usedByEnabled={c.usedEnabled(ctx.row.original)} t={t} />,
    },
    {
      id: 'usage', header: t(`${L}cols.usage`), enableSorting: false, meta: { label: t(`${L}cols.usage`), className: 'w-[20%]' },
      // A click on the usage summary opens its popover, not the list.
      cell: ctx => <span data-no-row-click onClick={stop}><UsedByPopover usedBy={ctx.row.original.used_by as DestPolicyRef[]} onOpenPolicy={c.onOpenPolicy} onOpenAllowlist={c.onOpenAllowlist} /></span>,
    },
    {
      id: 'actions', header: '', enableSorting: false, meta: { hideable: false, align: 'right', label: t(`${L}cols.actions`), className: 'w-14' },
      cell: ctx => <MoreMenu items={listMenu(c, ctx.row.original)} size="icon-xs" label={t(`${L}menu.label`, { name: ctx.row.original.name })} />,
    },
  ]
}

/** One list at phone width. */
export function ListCard({ list, ctx, open }: { list: DestList; ctx: ListColumnCtx; open?: () => void }) {
  const { t } = ctx
  const num = new Intl.NumberFormat(ctx.lang)
  return (
    <div data-testid="list-block" className="flex min-w-0 flex-col gap-1.5">
      <div className="flex min-w-0 items-center gap-2">
        <button type="button" onClick={open} title={list.name} className="min-w-0 flex-1 truncate text-left text-sm font-medium outline-none focus-visible:underline">{list.name}</button>
        <MoreMenu items={listMenu(ctx, list)} size="icon-xs" label={t(`${L}menu.label`, { name: list.name })} />
      </div>
      <ListStatusCell list={list} usedByEnabled={ctx.usedEnabled(list)} t={t} />
      <p className={cn('min-w-0 text-xs text-muted-foreground [overflow-wrap:anywhere]')}>
        {kindText(t, list)} · <span className="sr-only">{t(`${L}label.entries`)} </span>
        {list.state === 'pending' ? '—' : num.format(list.entry_count)} · <span className="sr-only">{t(`${L}label.usage`)} </span>
        <span data-no-row-click onClick={stop}><UsedByPopover usedBy={list.used_by} onOpenPolicy={ctx.onOpenPolicy} onOpenAllowlist={ctx.onOpenAllowlist} /></span>
      </p>
    </div>
  )
}
