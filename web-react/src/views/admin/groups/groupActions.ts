import type { TFunction } from 'i18next'
import type { NavigateFunction } from 'react-router'

import type { Group } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'

/**
 * The question before a group is deleted, shared by the list's row menu and the group's own page so they cannot
 * say different things. Resolves true only when the group may go and the admin said so; the caller deletes.
 *
 * The server refuses to delete a group that still has members (409), so asking is pointless for those: say how many
 * users are in it and what to do (move them on the Users page), with a button that goes there. An empty group
 * confirms with what goes with it.
 */
export async function confirmDeleteGroup(g: Group, t: TFunction, navigate: NavigateFunction): Promise<boolean> {
  if (g.members > 0) {
    const toUsers = await confirm({
      title: t('admin:groups.delete.blocked_title', { name: g.name }),
      message: t('admin:groups.delete.blocked_message', { count: g.members }),
      confirmText: t('admin:groups.delete.blocked_action'),
      cancelText: t('common:actions.close'),
    })
    if (toUsers) void navigate('/admin/users')
    return false
  }
  return confirm({
    title: t('admin:groups.delete.title', { name: g.name }),
    message: t('admin:groups.delete.message'),
    confirmText: t('common:actions.delete'),
    destructive: true,
    initialFocus: 'cancel',
  })
}
