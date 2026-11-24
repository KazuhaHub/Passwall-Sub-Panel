import { useRef } from 'react'
import { Navigate, useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'

import { deleteGroup } from '@/api/groups'
import type { Group } from '@/api/types'
import { ObjectPage, objectErrorKind, type ObjectPageState } from '@/components/common/ObjectPage'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { useBackTarget } from '@/components/common/objectReturn'
import { StatusDot } from '@/components/common/StatusDot'
import { pushToast } from '@/components/feedback/toasts'
import { IconDelete, IconUsers } from '@/lib/icons'
import { groupDetailQuery } from '@/query/groups'
import { useQueryScope } from '@/query/useQueryScope'
import { DestBadge } from '../columns'
import { confirmDeleteGroup } from '../groupActions'
import { useGroupCache } from '../groupCache'
import { GROUPS_PATH } from '../groupRoutes'
import { useGroupsT } from '../useGroupsT'
import { GroupEditor } from './GroupEditor'

/**
 * `/admin/groups/:id`: one group as a page. The group is read by id (the list's row is not needed), so a link opens it
 * cold. Everything the load can end in (loading, no such group, not allowed, failed) is drawn in the frame with the
 * back link still there. A delete made here leaves for the list without asking about a draft: it is moot.
 */
export default function GroupDetailPage({ id }: { id: number }) {
  const t = useGroupsT()
  const scope = useQueryScope()
  const navigate = useNavigate()
  const cache = useGroupCache()
  const back = { to: useBackTarget(GROUPS_PATH), label: t('admin:groups.title') }
  const query = useQuery(groupDetailQuery(scope, id))
  const group = query.data
  // Set when this page deleted the group: the page then leaves for the list instead of saying "no such group".
  const leaving = useRef(false)
  const deleting = useRef(false)

  async function remove(g: Group) {
    if (deleting.current || !(await confirmDeleteGroup(g, t, navigate))) return
    deleting.current = true
    try {
      await deleteGroup(g.id)
      leaving.current = true
      pushToast({ message: t('admin:groups.toast.deleted'), severity: 'success' })
      cache.forget(g.id)
      cache.refreshLists()
      void navigate(back.to, { replace: true })
    } catch { /* The request layer already surfaced the failure. */ } finally { deleting.current = false }
  }

  const kind = query.isError && !group ? objectErrorKind(query.error) : null
  if (kind === 'not_found' && leaving.current) return <Navigate to={back.to} replace />
  const state: ObjectPageState | undefined = kind === 'not_found' ? { kind, title: t('admin:groups.detail.not_found') }
    : kind === 'forbidden' ? { kind, title: t('admin:groups.detail.forbidden') }
    : kind === 'error' ? { kind, reason: query.error, retry: () => query.refetch() }
    : !group ? { kind: 'loading' } : undefined
  if (state || !group) return <ObjectPage back={back} state={state} />

  const more: MoreMenuItem[] = [
    { id: 'members', label: t('admin:groups.action.members'), icon: IconUsers, to: '/admin/users' },
    { id: 'delete', label: t('admin:groups.action.delete'), icon: IconDelete, destructive: true, onSelect: () => void remove(group) },
  ]
  const frame = {
    title: group.name,
    subtitle: <span className="font-mono">{group.slug}</span>,
    status: (
      <>
        <span className="text-sm">{t('admin:groups.members.count', { count: group.members })}</span>
        {group.require_2fa && <StatusDot tone="info" label={`${t('admin:groups.table.two_factor')}: ${t('admin:groups.list.two_factor_required')}`} />}
        <DestBadge state={group.dest_state} t={t} />
      </>
    ),
    more,
  }
  // Keyed by group, so another group starts from clean forms.
  return (
    <GroupEditor key={group.id} group={group} back={back} frame={frame} bypass={leaving}
      onSaved={saved => { cache.remember(saved, cache.members(saved.id) ?? group.members); cache.refreshLists() }} />
  )
}
