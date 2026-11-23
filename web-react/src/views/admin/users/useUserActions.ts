import { useCallback, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import {
  deleteUser, reset2FA, resetCredentials, resetEmergencyUsage, setServiceStatus, unlinkSSO, updateUser,
} from '@/api/users'
import type { User } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/feedback/toasts'
import { riskCenterKeys, trafficKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { copyToClipboard } from '@/utils/clipboard'
import type { DialogState } from './dialogs'
import { canQuickRenew, renewedExpireAt } from './model'
import { useAdminT } from './useAdminT'

/**
 * Every single-user action, with its confirmation and its toast, shared by the row menu and the sheet so the
 * two cannot say different things. Each returns when the request settles; a failure is already toasted by the
 * request layer, so it only ends the wait. `busy` holds the ids with a request in flight: a row shows it, and
 * a second click on the same account is ignored instead of sending a second request.
 */
export function useUserActions({ reload, setDialog, afterDelete }: {
  /** Re-reads the list (and the opened user) after a write. */
  reload: () => Promise<unknown>
  setDialog: (d: DialogState) => void
  /** The account is gone: the sheet that was open on it closes. */
  afterDelete: (id: number) => void
}) {
  const t = useAdminT()
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const [busy, setBusy] = useState<ReadonlySet<number>>(new Set())
  const running = useRef(new Set<number>())

  /** Drop the usage leaderboard so the next observer re-reads it (a write that changes usage). */
  const invalidateUsage = useCallback(() => { void queryClient.invalidateQueries({ queryKey: trafficKeys.all(scope) }) }, [queryClient, scope])
  /**
   * Drop what the risk center shows for the accounts after this page moves one's service switch: a location
   * hold is listed there as a chip, and /levels has no interval, so nothing else would re-read it and a
   * lifted hold's chip would outlive the hold beside a row that already says active.
   */
  const invalidateRisk = useCallback(() => { void queryClient.invalidateQueries({ queryKey: riskCenterKeys.all(scope) }) }, [queryClient, scope])

  /** Runs one account's action with the busy mark; a rejection only ends the wait. */
  const guarded = useCallback(async (u: User, fn: () => Promise<unknown>) => {
    if (running.current.has(u.id)) return
    running.current.add(u.id)
    setBusy(new Set(running.current))
    try { await fn() } catch { /* reported by the request layer */ } finally {
      running.current.delete(u.id)
      setBusy(new Set(running.current))
    }
  }, [])

  return {
    busy,
    invalidateUsage,

    copySub: (u: User) => copyToClipboard(u.sub_url),

    renew: (u: User) => guarded(u, async () => {
      if (!canQuickRenew(u)) { pushSnack(t('users.toast.renew_permanent_skip'), 'info'); return }
      await updateUser(u.id, { expire_at: renewedExpireAt(u, 30) })
      pushSnack(t('users.toast.renewed', { days: 30 }), 'success')
      await reload()
    }),

    /** Deleting says what goes with the account and that it cannot be undone, with focus on Cancel. */
    remove: (u: User) => guarded(u, async () => {
      const ok = await confirm({
        title: t('users.confirm.delete_title'),
        message: t('users.confirm.delete_message', { upn: u.upn }),
        destructive: true,
        confirmText: t('users.action.delete'),
        initialFocus: 'cancel',
      })
      if (!ok) return
      await deleteUser(u.id)
      pushSnack(t('users.toast.deleted'), 'success')
      afterDelete(u.id)
      await reload()
    }),

    /** Enable or disable the ACCOUNT: asks for an optional reason in a dialog that stays open until the request settles. */
    askToggleAccount: (u: User, enable: boolean) => setDialog({ kind: 'reason', users: [u], enable, batch: false }),

    resume: (u: User) => guarded(u, async () => {
      await setServiceStatus(u.id, true)
      pushSnack(t('users.toast.service_resumed'), 'success')
      invalidateRisk()
      await reload()
    }),

    suspend: (u: User) => guarded(u, async () => {
      await setServiceStatus(u.id, false, 'service_manual')
      pushSnack(t('users.toast.service_suspended'), 'success')
      invalidateRisk()
      await reload()
    }),

    resetCredentials: (u: User) => guarded(u, async () => {
      const ok = await confirm({
        title: t('users.confirm.reset_credentials_title'),
        message: t('users.confirm.reset_credentials_message', { upn: u.upn }),
        destructive: true,
        confirmText: t('users.more_menu.reset_credentials'),
        initialFocus: 'cancel',
      })
      if (!ok) return
      const res = await resetCredentials(u.id)
      pushSnack(t('users.credentials.result', { uuid: res.uuid }), 'success')
      await reload()
    }),

    unlinkSso: (u: User) => guarded(u, async () => {
      const ok = await confirm({
        title: t('users.confirm.unlink_sso_title'),
        message: t('users.confirm.unlink_sso_message', { upn: u.upn, provider: u.sso_provider }),
        destructive: true,
        confirmText: t('users.more_menu.unlink_sso'),
        initialFocus: 'cancel',
      })
      if (!ok) return
      await unlinkSSO(u.id)
      pushSnack(t('users.toast.sso_unlinked'), 'success')
      await reload()
    }),

    reset2fa: (u: User) => guarded(u, async () => {
      const ok = await confirm({
        title: t('users.confirm.reset_2fa_title'),
        message: t('users.confirm.reset_2fa_message', { upn: u.upn }),
        destructive: true,
        confirmText: t('users.more_menu.reset_2fa'),
        initialFocus: 'cancel',
      })
      if (!ok) return
      await reset2FA(u.id)
      pushSnack(t('users.credentials.twofa_reset'), 'success')
      await reload()
    }),

    resetEmergency: (u: User) => guarded(u, async () => {
      await resetEmergencyUsage(u.id)
      pushSnack(t('users.credentials.emergency_reset'), 'success')
      invalidateUsage()
      await reload()
    }),

    resetPassword: (u: User) => setDialog({ kind: 'password', user: u }),
    managePasskeys: (u: User) => setDialog({ kind: 'passkeys', user: u }),
    openRules: (u: User) => setDialog({ kind: 'rules', user: u }),
  }
}

export type UserActions = ReturnType<typeof useUserActions>
