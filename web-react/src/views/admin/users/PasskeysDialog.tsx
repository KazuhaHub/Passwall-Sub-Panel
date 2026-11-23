import { useEffect, useState } from 'react'
import type { AxiosError } from 'axios'
import { listUserPasskeys, revokeAllUserPasskeys, revokeUserPasskey } from '@/api/users'
import type { PasskeyCredential, User } from '@/api/types'
import { AsyncButton, AsyncIconButton } from '@/components/common/AsyncButton'
import { confirm } from '@/components/ConfirmHost'
import { EmptyState } from '@/components/feedback/EmptyState'
import { LoadingState } from '@/components/feedback/LoadingState'
import { pushSnack } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { IconDelete } from '@/lib/icons'
import { useSiteStore } from '@/stores/site'
import { formatDualDate } from '@/utils/datetime'
import { useAdminT } from './useAdminT'

/**
 * Lists a user's registered passkeys and revokes them (break-glass for a lost or compromised device). An admin
 * cannot enroll one on the user's behalf (that needs their own authenticator), so it is view and revoke only. The
 * list is read when the dialog opens. Each revoke names the account and the key, with focus on Cancel.
 */
export function PasskeysDialog({ user, onClose }: { user: User; onClose: () => void }) {
  const t = useAdminT()
  const panelTz = useSiteStore(s => s.timezone)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [creds, setCreds] = useState<PasskeyCredential[]>([])
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let live = true
    setCreds([]); setLoading(true); setFailed(false)
    listUserPasskeys(user.id)
      .then(v => { if (live) setCreds(v) })
      .catch((e: AxiosError<{ error?: string }>) => {
        if (!live) return
        setFailed(true)
        pushSnack(e.response?.data?.error || t('users.passkeys.load_failed'), 'error')
      })
      .finally(() => { if (live) setLoading(false) })
    return () => { live = false }
  }, [user.id, t])

  const errSnack = (e: unknown) => pushSnack((e as AxiosError<{ error?: string }>).response?.data?.error || t('users.passkeys.revoke_failed'), 'error')

  async function revokeOne(c: PasskeyCredential) {
    const ok = await confirm({
      title: t('users.passkeys.revoke_title'),
      message: t('users.passkeys.revoke_message', { name: c.name, upn: user.upn }),
      destructive: true,
      confirmText: t('users.passkeys.revoke'),
      initialFocus: 'cancel',
    })
    if (!ok) return
    setBusy(true)
    try {
      await revokeUserPasskey(user.id, c.id)
      setCreds(prev => prev.filter(x => x.id !== c.id))
      pushSnack(t('users.passkeys.revoked'), 'success')
    } catch (e) { errSnack(e) } finally { setBusy(false) }
  }

  async function revokeAll() {
    if (creds.length === 0) return
    const ok = await confirm({
      title: t('users.passkeys.revoke_all_title'),
      message: t('users.passkeys.revoke_all_message', { upn: user.upn, count: creds.length }),
      destructive: true,
      confirmText: t('users.passkeys.revoke_all'),
      initialFocus: 'cancel',
    })
    if (!ok) return
    setBusy(true)
    try {
      const n = await revokeAllUserPasskeys(user.id)
      setCreds([])
      pushSnack(t('users.passkeys.revoked_all', { count: n }), 'success')
    } catch (e) { errSnack(e) } finally { setBusy(false) }
  }

  return (
    <Dialog open onOpenChange={open => { if (!open && !busy) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('users.passkeys.title')} — {user.upn}</DialogTitle>
          <DialogDescription>{t('users.passkeys.intro')}</DialogDescription>
        </DialogHeader>
        {loading ? <LoadingState /> : failed ? (
          <p className="py-4 text-center text-sm text-muted-foreground">{t('users.passkeys.load_failed')}</p>
        ) : creds.length === 0 ? (
          <EmptyState compact title={t('users.passkeys.empty')} />
        ) : (
          <ul className="divide-y rounded-lg border">
            {creds.map(c => (
              <li key={c.id} className="flex items-center gap-2 px-3 py-2">
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-medium" title={c.name}>{c.name}</div>
                  <div className="text-xs text-muted-foreground">
                    {t('users.passkeys.added_at', { date: formatDualDate(c.created_at, panelTz) })}
                    {c.last_used_at ? ` · ${t('users.passkeys.last_used', { date: formatDualDate(c.last_used_at, panelTz) })}` : ''}
                  </div>
                </div>
                <AsyncIconButton label={t('users.passkeys.revoke_named', { name: c.name })} tooltip={t('users.passkeys.revoke')} size="icon-sm" disabled={busy} onClick={() => revokeOne(c)}><IconDelete /></AsyncIconButton>
              </li>
            ))}
          </ul>
        )}
        <DialogFooter>
          {creds.length > 0 && <AsyncButton variant="outline" className="text-destructive sm:mr-auto" disabled={busy} onClick={revokeAll}>{t('users.passkeys.revoke_all')}</AsyncButton>}
          <Button variant="outline" disabled={busy} onClick={onClose}>{t('users.passkeys.close')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
