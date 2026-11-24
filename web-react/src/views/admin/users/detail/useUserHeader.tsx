import { useId, type ReactNode } from 'react'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { AsyncButton } from '@/components/common/AsyncButton'
import { InfoTip } from '@/components/common/HelpTip'
import { Button } from '@/components/ui/button'
import { IconCopy, IconDelete, IconKey, IconLock, IconLockReset, IconRetry, IconShield } from '@/lib/icons'
import type { User } from '@/api/types'
import { accountEnabledForEdit } from '@/utils/userAccess'
import { AxisBadge } from '../cells'
import { useUsers } from '../context'
import { canQuickRenew, canResumeService, canSuspendService } from '../model'
import { accountAxis, serviceAxis, type Axis } from '../status'
import { useAdminT } from '../useAdminT'

/** The reason a button is switched off, said where a screen reader reads the button, not only in a hover. */
function Unavailable({ reason, children, ...rest }: { reason: string } & Omit<React.ComponentProps<typeof Button>, 'disabled'>) {
  const id = useId()
  // The reason is a sibling, not part of the button: inside it, it would become the button's NAME.
  return (
    <>
      <Button variant="outline" {...rest} aria-disabled="true" aria-describedby={id} title={reason} className="aria-disabled:cursor-not-allowed aria-disabled:opacity-50">{children}</Button>
      <span id={id} className="sr-only">{reason}</span>
    </>
  )
}

/** One switch of the user told in the header: its name, its state in words, and what the state means one tap away. */
export function StatusLine({ title, axis }: { title: string; axis: Axis }) {
  const t = useAdminT()
  const why = [axis.explain ? t(axis.explain, axis.vars) : '', axis.detail].filter(Boolean).join(' ')
  return (
    <span className="inline-flex items-center gap-1.5 text-sm">
      <span className="text-muted-foreground">{title}</span>
      <AxisBadge axis={axis} />
      {why && <InfoTip text={why} />}
    </span>
  )
}

/**
 * What the user's page offers in its header, by the same rules as the list's row menu and the old side sheet: an operator
 * may only act on role=user accounts, so on an admin or operator account the writes are switched off WITH the reason
 * (the page itself is still readable); nobody may disable their OWN account (it would lock them out). The two quick
 * buttons are the two state changes asked for most (the account switch, the service switch); the rest sits in More.
 * The confirmations and requests are the actions' own (`useUserActions`), so the header, the tabs and the list cannot
 * say different things.
 */
export function useUserHeader(user: User, manageable: boolean): { status: ReactNode; secondary: ReactNode; more: MoreMenuItem[] } {
  const t = useAdminT()
  const { act, selfId, canRisk, openRisk } = useUsers()
  const accountOn = accountEnabledForEdit(user)
  const self = user.id === selfId
  const readOnly = t('users.readonly_operator')

  const status = (
    <>
      <StatusLine title={t('users.axis.account_title')} axis={accountAxis(user)} />
      <StatusLine title={t('users.axis.service_title')} axis={serviceAxis(user)} />
    </>
  )

  const accountReason = !manageable ? readOnly : accountOn && self ? t('users.field.account_self_locked') : undefined
  const accountLabel = t(accountOn ? 'users.action.disable' : 'users.action.enable')
  const service = canResumeService(user) ? 'resume' : canSuspendService(user) ? 'suspend' : null
  const serviceLabel = t(service === 'resume' ? 'users.more_menu.resume_service' : 'users.more_menu.suspend_service')
  const secondary = (
    <>
      {accountReason
        ? <Unavailable reason={accountReason}>{accountLabel}</Unavailable>
        : <Button variant="outline" onClick={() => act.askToggleAccount(user, !accountOn)}>{accountLabel}</Button>}
      {service && (manageable
        ? <AsyncButton variant="outline" onClick={() => (service === 'resume' ? act.resume(user) : act.suspend(user))}>{serviceLabel}</AsyncButton>
        : <Unavailable reason={readOnly}>{serviceLabel}</Unavailable>)}
    </>
  )

  const more: MoreMenuItem[] = []
  if (user.sub_url) more.push({ id: 'copy', label: t('users.more_menu.copy_sub'), icon: IconCopy, onSelect: () => void act.copySub(user) })
  if (manageable) {
    more.push({ id: 'renew', label: t('users.action.renew'), icon: IconRetry, disabled: !canQuickRenew(user), disabledReason: t('users.toast.renew_permanent_skip'), onSelect: () => void act.renew(user) })
    more.push({ id: 'reset-password', label: t('users.more_menu.reset_password'), icon: IconLock, separatorBefore: true, onSelect: () => act.resetPassword(user) })
    more.push({ id: 'reset-credentials', label: t('users.more_menu.reset_credentials'), icon: IconKey, onSelect: () => void act.resetCredentials(user) })
    more.push({ id: 'reset-2fa', label: t('users.more_menu.reset_2fa'), icon: IconLockReset, disabled: !user.totp_enabled, disabledReason: t('users.header.no_totp'), onSelect: () => void act.reset2fa(user) })
  }
  // Any account, not only the ones the risk column lists: a quiet account's connections and devices are worth a look too.
  if (canRisk) more.push({ id: 'risk', label: t('users.more_menu.risk_details'), icon: IconShield, separatorBefore: true, onSelect: () => openRisk(user.id) })
  if (manageable) more.push({ id: 'delete', label: t('users.action.delete'), icon: IconDelete, destructive: true, onSelect: () => void act.remove(user) })

  return { status, secondary, more }
}
