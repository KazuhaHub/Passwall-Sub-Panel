import { useEffect, useState } from 'react'
import { claimClient } from '@/api/nodes'
import type { UnmanagedInbound, User } from '@/api/types'
import { listUsers } from '@/api/users'
import { Button } from '@/components/ui/button'
import { Combobox, ComboboxContent, ComboboxEmpty, ComboboxInput, ComboboxItem, ComboboxList } from '@/components/ui/combobox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Field } from '@/components/form/Field'
import { pushToast } from '@/components/feedback/toasts'
import { validateEmail } from '@/utils/validators'
import { useNodesT } from '../shared/useNodesT'

const userLabel = (u: User) => (u.display_name ? `${u.display_name} (${u.upn})` : u.upn)

/**
 * Claim an existing upstream client for a PSP user. A three-field task, so a dialog and not a page. While the
 * user list loads the picker says so; an empty list and an unloaded one must not look alike.
 */
export function ClaimDialog({ inbound, onClose, onClaimed }: { inbound: UnmanagedInbound; onClose: () => void; onClaimed: () => void }) {
  const t = useNodesT()
  const [users, setUsers] = useState<User[]>([])
  const [loading, setLoading] = useState(true)
  const [user, setUser] = useState<User | null>(null)
  const [email, setEmail] = useState('')
  const [uuid, setUuid] = useState('')
  const [touched, setTouched] = useState(false)

  useEffect(() => {
    let live = true
    listUsers({ page: 1, page_size: 200 })
      .then(res => { if (live) setUsers(res.items) })
      .catch(() => { /* the request layer already surfaced it */ })
      .finally(() => { if (live) setLoading(false) })
    return () => { live = false }
  }, [])

  // 3X-UI keys a client by this "email", which is no real mailbox but must still look like one.
  const emailError = touched ? validateEmail(email, { required: true }) : ''
  const userError = touched && !user ? 'validation.required' : ''
  const submit = async () => {
    setTouched(true)
    if (!user || validateEmail(email, { required: true })) return
    try {
      await claimClient({ user_id: user.id, panel_id: inbound.PanelID, inbound_id: inbound.InboundID, client_email: email, client_uuid: uuid || undefined })
      pushToast({ message: t('nodes.claim_dialog.claimed'), severity: 'success' })
      onClaimed()
      onClose()
    } catch { /* the request layer already surfaced it */ }
  }

  return (
    <Dialog open onOpenChange={o => { if (!o) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('nodes.claim_dialog.title')}</DialogTitle>
          <DialogDescription>{t('nodes.claim_dialog.subtitle', { id: inbound.InboundID })}</DialogDescription>
        </DialogHeader>
        <Field label={t('nodes.claim_dialog.user')} required error={userError ? t(userError) : undefined}>
          {p => (
            <Combobox items={users} value={user} onValueChange={v => setUser((v as User | null) ?? null)} disabled={loading}
              itemToStringLabel={(u: User) => userLabel(u)} isItemEqualToValue={(a: User, b: User) => a.id === b.id}>
              <ComboboxInput id={p.id} aria-describedby={p['aria-describedby']} aria-invalid={p['aria-invalid']} disabled={loading}
                placeholder={loading ? t('nodes.claim_dialog.users_loading') : t('nodes.claim_dialog.user')} />
              <ComboboxContent>
                <ComboboxEmpty>{t('nodes.order.move_none')}</ComboboxEmpty>
                <ComboboxList>{(u: User) => <ComboboxItem key={u.id} value={u}>{userLabel(u)}</ComboboxItem>}</ComboboxList>
              </ComboboxContent>
            </Combobox>
          )}
        </Field>
        <Field label={t('nodes.claim_dialog.client_email')} required error={emailError ? t(emailError) : undefined}>
          <Input value={email} onChange={e => setEmail(e.target.value)} onBlur={() => setTouched(true)} />
        </Field>
        <Field label={t('nodes.claim_dialog.client_uuid')}>
          <Input value={uuid} onChange={e => setUuid(e.target.value)} />
        </Field>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t('common:actions.cancel')}</Button>
          <AsyncButton onClick={submit}>{t('nodes.claim_dialog.submit')}</AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
