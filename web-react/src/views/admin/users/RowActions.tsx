import type { User } from '@/api/types'
import { IconButton } from '@/components/common/IconButton'
import { MoreMenu, type MoreMenuItem } from '@/components/common/MoreMenu'
import { IconCopy, IconDelete, IconEdit, IconPaused, IconRetry, IconSync } from '@/lib/icons'
import { accountEnabledForEdit } from '@/utils/userAccess'
import { useUsers } from './context'
import { canManageUser, canQuickRenew, canResumeService, canSuspendService } from './model'
import { useAdminT } from './useAdminT'

/**
 * A row's actions: one button (edit, which opens the user's page on its Settings tab) and a short menu of what makes
 * sense without opening the user: copy the subscription link, renew, enable or disable the account, suspend or resume
 * the service, delete. Everything else (personal rules, security, the risk lookup, resets) lives on the user's page.
 * An operator may only act on role=user accounts (the backend refuses the rest), so for an admin or operator account
 * the edit button is marked unavailable WITH its reason (the page still opens, read-only) and the menu offers only what
 * is read-only, rather than showing actions that would answer 403. The subscription link of such an account is
 * hidden from an operator, so it has no copy.
 */
export function RowActions({ user, menuOnly }: { user: User; menuOnly?: boolean }) {
  const t = useAdminT()
  const { act, canElevate, selfId, openUser } = useUsers()
  const manageable = canManageUser(user, canElevate)
  const accountOn = accountEnabledForEdit(user)
  const self = user.id === selfId

  const items: MoreMenuItem[] = []
  if (user.sub_url) items.push({ id: 'copy', label: t('users.more_menu.copy_sub'), icon: IconCopy, onSelect: () => void act.copySub(user) })
  if (manageable) {
    items.push({ id: 'renew', label: t('users.action.renew'), icon: IconRetry, disabled: !canQuickRenew(user), onSelect: () => void act.renew(user) })
    items.push(accountOn
      ? { id: 'toggle', label: t('users.action.disable'), icon: IconPaused, disabled: self, onSelect: () => act.askToggleAccount(user, false) }
      : { id: 'toggle', label: t('users.action.enable'), icon: IconSync, onSelect: () => act.askToggleAccount(user, true) })
    if (canResumeService(user)) items.push({ id: 'resume', label: t('users.more_menu.resume_service'), icon: IconSync, onSelect: () => void act.resume(user) })
    if (canSuspendService(user)) items.push({ id: 'suspend', label: t('users.more_menu.suspend_service'), icon: IconPaused, onSelect: () => void act.suspend(user) })
  }
  if (manageable) items.push({ id: 'delete', label: t('users.action.delete'), icon: IconDelete, destructive: true, onSelect: () => void act.remove(user) })

  return (
    <div className="flex items-center justify-end gap-0.5">
      {!menuOnly && (
        <IconButton label={t('users.action.edit')} size="icon-xs" aria-disabled={!manageable || undefined}
          tooltip={manageable ? undefined : t('users.readonly_operator')} onClick={() => openUser(user.id, 'settings')}>
          <IconEdit />
        </IconButton>
      )}
      {items.length > 0 && <MoreMenu items={items} size="icon-xs" label={t('users.action.row_menu', { upn: user.upn })} />}
    </div>
  )
}
