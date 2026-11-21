import type { ReactNode } from 'react'
import { useNavigate } from 'react-router'

import type { DestGroup, DestState } from '@/api/accessControl'
import { StatusBadge } from '@/components/common/StatusBadge'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Skeleton } from '@/components/ui/skeleton'
import { useDestGroups } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { groupHasAllowlist, stateDay } from '../accessControl/allowlist/allowlistState'
import { useGroupsT } from './useGroupsT'

const D = 'admin:groups.destination.'

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-1 items-baseline gap-x-4 gap-y-0.5 sm:grid-cols-[6rem_minmax(0,1fr)]">
      <span className="text-sm text-muted-foreground">{label}</span>
      <div className="min-w-0 break-words">{children}</div>
    </div>
  )
}

/** The group's allowlist state with what it is ("Allowlist · on"); the report-only state counts its day. */
function StateLabel({ state, day }: { state: Exclude<DestState, 'off'>; day: number | null }) {
  const t = useGroupsT()
  const label = state === 'report' && day !== null
    ? t(`${D}report`, { days: day })
    : t(`${D}${state === 'report' ? 'report_nodays' : 'on'}`)
  return <StatusBadge tone="info" quiet={false} testId="allowlist-state" label={label} />
}

/**
 * A group's destination access in the group sheet (S17): a summary and the way to the access-control page, never
 * an editor. The allowlist lives in one place (its tab on the access-control page), so the sheet's save cannot
 * overwrite it with a stale copy. A group with no allowlist is unrestricted and can be given one from here.
 * Going to that page is an ordinary navigation: the sheet's own guard asks before a draft is dropped.
 */
export default function GroupDestinationTab({ groupId }: {
  /** Null for a group that is not saved yet. */
  groupId: number | null
}) {
  const t = useGroupsT()
  const navigate = useNavigate()
  const scope = useQueryScope()
  const read = useDestGroups(scope, { enabled: groupId !== null })

  if (groupId === null) return <p className="text-sm text-muted-foreground">{t(`${D}save_first`)}</p>
  if (read.isPending) return <div className="grid gap-2">{[0, 1, 2].map(i => <Skeleton key={i} className="h-6 w-full" />)}</div>
  if (read.isError) {
    return (
      <div role="alert" className="flex flex-wrap items-center gap-3 text-sm">
        <span className="text-destructive">{t(`${D}read_failed`)}</span>
        <Button type="button" size="sm" variant="outline" onClick={() => void read.refetch()}>{t(`${D}retry`)}</Button>
      </div>
    )
  }

  const g: DestGroup | undefined = read.data?.find(x => x.group_id === groupId)
  const go = (search: string) => void navigate(`/admin/access-control?tab=allowlist&${search}`)
  const enforced = g !== undefined && g.state !== 'off'
  const kept = g !== undefined && g.state === 'off' && groupHasAllowlist(g)

  return (
    <div className="grid gap-4">
      <Row label={t(`${D}mode`)}>
        {enforced && g
          ? <StateLabel state={g.state as Exclude<DestState, 'off'>} day={stateDay(g.state_changed_at, Date.now())} />
          : <span className="font-medium">{t(`${D}open`)}</span>}
      </Row>
      {enforced && g && (
        <>
          <Row label={t(`${D}lists`)}>
            <span className="text-sm">{t(`${D}lists_line`, {
              lists: g.lists.length > 0 ? g.lists.map(l => t(`${D}list_item`, { name: l.name, entries: l.entry_count })).join(t(`${D}list_separator`)) : t(`${D}lists_none`),
              base: g.base_count,
            })}</span>
          </Row>
          <Row label={t(`${D}nodes`)}>
            <span className="text-sm">{t(`${D}nodes_line`, { ok: g.eligible_nodes, total: g.total_nodes })}</span>
            {g.ineligible.length > 0 && (
              <Popover>
                <PopoverTrigger render={<Button type="button" variant="link" size="sm" className="ml-3 h-auto p-0 text-sm" />}>
                  {t(`${D}not_open`, { count: g.ineligible.length })}
                </PopoverTrigger>
                <PopoverContent className="w-80 text-sm">
                  {g.ineligible.map(n => t(`${D}not_open_item`, { name: n.name, reason: t(`admin:access_control.allowlist.reason.${n.reason}`) })).join(t(`${D}detail_separator`))}
                </PopoverContent>
              </Popover>
            )}
          </Row>
          <p className="text-sm text-muted-foreground">{t(`${D}summary_note`)}</p>
          <div className="flex sm:justify-end">
            <Button type="button" variant="outline" onClick={() => go(`al_group=${groupId}`)}>{t(`${D}edit`)}</Button>
          </div>
        </>
      )}
      {!enforced && (
        <>
          <div className="text-sm text-muted-foreground">
            <p>{t(`${D}open_hint`)}</p>
            {kept && <p>{t(`${D}open_kept`)}</p>}
          </div>
          <div className="flex sm:justify-end">
            <Button type="button" variant="outline" onClick={() => go(`al_enable=${groupId}`)}>{t(`${D}enable`)}</Button>
          </div>
        </>
      )}
    </div>
  )
}
