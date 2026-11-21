import { useRef, useState } from 'react'
import { MoreMenu } from '@/components/common/MoreMenu'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/feedback/toasts'
import { IconClean, IconDelete } from '@/lib/icons'
import { useCan } from '@/utils/permissions'
import { useAdminT } from './shared'

/**
 * Purge and clear for one log, admin only (config.write, as the routes are). Both delete records for
 * good, so each says what it deletes and starts with focus on Cancel. The lock matters more than the
 * disabled items: the menu closes on select, and the confirm and the request take seconds on a slow
 * link, during which a second pick must not send a second delete.
 */
export function MaintenanceMenu({ kind, clear, purge, onDone }: {
  kind: 'sub' | 'audit' | 'email'
  clear: () => Promise<unknown>
  /** Only the logs with a retention period can be purged. */
  purge?: () => Promise<{ deleted: number }>
  onDone: () => unknown
}) {
  const t = useAdminT()
  const admin = useCan('config.write')
  const lock = useRef(false)
  const [busy, setBusy] = useState(false)
  if (!admin) return null

  async function run(task: () => Promise<void>) {
    if (lock.current) return
    lock.current = true
    setBusy(true)
    try { await task() } catch { /* reported by the request layer */ } finally { lock.current = false; setBusy(false) }
  }

  const onPurge = () => run(async () => {
    const ok = await confirm({
      title: t(`logs.confirm.purge_${kind}_title`),
      message: t(`logs.confirm.purge_${kind}_message`),
      confirmText: t('logs.purge_old'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok || !purge) return
    const r = await purge()
    pushSnack(t('logs.toast.purged', { count: r.deleted }), 'success')
    await onDone()
  })

  const onClear = () => run(async () => {
    const ok = await confirm({
      title: t(`logs.confirm.clear_${kind}_title`),
      message: t(`logs.confirm.clear_${kind}_message`),
      confirmText: t('logs.clear_all'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok) return
    await clear()
    pushSnack(t('logs.toast.cleared'), 'success')
    await onDone()
  })

  return (
    <MoreMenu
      label={t('logs.maintenance')}
      items={[
        ...(purge ? [{ id: 'purge', label: t('logs.purge_old'), icon: IconClean, disabled: busy, onSelect: () => void onPurge() }] : []),
        { id: 'clear', label: t('logs.clear_all'), icon: IconDelete, destructive: true, disabled: busy, onSelect: () => void onClear() },
      ]}
    />
  )
}
