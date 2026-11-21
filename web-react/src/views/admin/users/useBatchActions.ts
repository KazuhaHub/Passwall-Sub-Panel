import { useState } from 'react'
import { deleteUser, resetEmergencyUsage, setEnabled, unlinkSSO, updateUser } from '@/api/users'
import type { User } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/feedback/toasts'
import { allSettledLimited } from '@/utils/promises'
import type { DialogState } from './dialogs'
import { canQuickRenew, namesOf, renewedExpireAt } from './model'
import { useAdminT } from './useAdminT'

export type BatchKind = 'enable' | 'disable' | 'renew' | 'delete' | 'emergency' | 'unlink_sso' | ''

/**
 * The bulk bar's actions, each over the rows that were selected when it was pressed. Requests run a few at a
 * time and report one summary: how many worked, how many failed. What failed is not retried silently.
 */
export function useBatchActions({ reload, invalidateUsage, setDialog, clearSelection }: {
  reload: () => Promise<unknown>
  invalidateUsage: () => void
  setDialog: (d: DialogState) => void
  clearSelection: () => void
}) {
  const t = useAdminT()
  const [busy, setBusy] = useState<BatchKind>('')

  async function run(kind: BatchKind, fn: () => Promise<void>) {
    setBusy(kind)
    try { await fn() } finally { setBusy('') }
  }

  return {
    busy,

    askToggleAccount: (rows: User[], enable: boolean) => { if (rows.length) setDialog({ kind: 'reason', users: rows, enable, batch: true }) },

    /** Runs after the reason dialog; the dialog closes first and the toolbar shows the wait. */
    toggleAccount: (rows: User[], enable: boolean, reason: string) => run(enable ? 'enable' : 'disable', async () => {
      if (rows.length === 0) return
      const results = await allSettledLimited(rows, r => setEnabled(r.id, enable, reason.trim() || undefined))
      const failed = results.filter(r => r.status === 'rejected').length
      if (failed > 0) pushSnack(t(enable ? 'users.batch.enable_partial' : 'users.batch.disable_partial', { ok: rows.length - failed, fail: failed }), 'warning')
      else pushSnack(t(enable ? 'users.batch.enabled_count' : 'users.batch.disabled_count', { count: rows.length }), 'success')
      await reload()
    }),

    renew: (rows: User[]) => run('renew', async () => {
      const renewable = rows.filter(canQuickRenew)
      if (renewable.length === 0) { pushSnack(t('users.batch.no_renewable'), 'info'); return }
      const results = await allSettledLimited(renewable, r => updateUser(r.id, { expire_at: renewedExpireAt(r, 30) }))
      const failed = results.filter(r => r.status === 'rejected').length
      const skipped = rows.length - renewable.length
      if (failed > 0) pushSnack(t('users.batch.renewed_partial', { ok: renewable.length - failed, fail: failed }), 'warning')
      else if (skipped > 0) pushSnack(t('users.batch.renewed_with_skip', { count: renewable.length, skipped }), 'success')
      else pushSnack(t('users.toast.renewed', { days: 30 }), 'success')
      await reload()
    }),

    /** Only SSO-bound rows are eligible, so the count in the confirmation is what will actually be touched. */
    unlinkSso: async (rows: User[]) => {
      const eligible = rows.filter(r => r.sso_provider && r.sso_provider !== 'local')
      if (eligible.length === 0) { pushSnack(t('users.batch.unlink_sso_none_eligible'), 'info'); return }
      const { names, suffix } = namesOf(eligible)
      const ok = await confirm({
        title: t('users.confirm.batch_unlink_sso_title'),
        message: t('users.confirm.batch_unlink_sso_message', { names, suffix, count: eligible.length }),
        destructive: true,
        confirmText: t('users.batch.unlink_sso'),
        initialFocus: 'cancel',
      })
      if (!ok) return
      await run('unlink_sso', async () => {
        const results = await allSettledLimited(eligible, r => unlinkSSO(r.id))
        const failed = results.filter(r => r.status === 'rejected').length
        await reload()
        clearSelection()
        if (failed > 0) pushSnack(t('users.batch.unlink_sso_partial', { ok: eligible.length - failed, fail: failed }), 'warning')
        else pushSnack(t('users.batch.unlink_sso_done', { count: eligible.length }), 'success')
      })
    },

    emergency: (rows: User[]) => run('emergency', async () => {
      if (rows.length === 0) return
      const results = await allSettledLimited(rows, r => resetEmergencyUsage(r.id))
      const failed = results.filter(r => r.status === 'rejected').length
      if (failed > 0) pushSnack(t('users.batch.emergency_partial', { ok: rows.length - failed, fail: failed }), 'warning')
      else pushSnack(t('users.batch.emergency_count', { count: rows.length }), 'success')
      invalidateUsage()
      await reload()
    }),

    /** Names the people and what goes with them, with focus on Cancel; the selection is cleared whatever the outcome. */
    remove: async (rows: User[]) => {
      if (rows.length === 0) return
      const { names, suffix } = namesOf(rows)
      const ok = await confirm({
        title: t('users.confirm.batch_delete_title'),
        message: t('users.confirm.batch_delete_message', { names, suffix, count: rows.length }),
        destructive: true,
        confirmText: t('users.action.delete'),
        initialFocus: 'cancel',
      })
      if (!ok) return
      await run('delete', async () => {
        const results = await allSettledLimited(rows, r => deleteUser(r.id))
        const okCount = results.filter(r => r.status === 'fulfilled').length
        const failed = rows.length - okCount
        clearSelection()
        if (failed > 0) pushSnack(t('users.batch.delete_partial', { ok: okCount, fail: failed }), 'warning')
        else pushSnack(t('users.batch.deleted_count', { count: okCount }), 'success')
        await reload()
      })
    },
  }
}
