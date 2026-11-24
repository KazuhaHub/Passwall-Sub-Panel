import { useEffect, useState } from 'react'
import { startRegistration } from '@simplewebauthn/browser'
import type { AxiosError } from 'axios'

import { beginPasskeyEnroll, deletePasskey, finishPasskeyEnroll, renamePasskey } from '@/api/me'
import type { PasskeyCredential } from '@/api/types'
import { IconAdd, IconCheck, IconClose, IconDelete, IconEdit } from '@/lib/icons'
import { AsyncButton } from '@/components/common/AsyncButton'
import { IconButton } from '@/components/common/IconButton'
import { confirm } from '@/components/ConfirmHost'
import { EmptyState } from '@/components/feedback/EmptyState'
import { pushSnack } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { RecoveryCodeList } from './RecoveryCodeList'
import { useMeT } from './me/useMeT'
import { ceremonyCancelled, webAuthnSupported } from './webauthn'

interface Props {
  open: boolean
  available: boolean
  credentials: PasskeyCredential[]
  onClose: () => void
  onChanged: () => void
}

/**
 * A person's passkeys: list, rename, delete, and add one (which runs the browser's WebAuthn create() ceremony).
 *
 * The FIRST passkey also mints one-time recovery codes, because a passkey is a second factor and its holder needs a
 * fallback. They are returned once and stored hashed, so they take over the dialog until acknowledged, and nothing but
 * that button closes it. The page behind must not unmount this dialog while it holds them (MeView only shows a spinner
 * for its FIRST load for that reason), or the codes are lost between the server returning them and the person seeing them.
 */
export default function PasskeyDialog({ open, available, credentials, onClose, onChanged }: Props) {
  const { t } = useMeT()
  const [busy, setBusy] = useState(false)
  const [adding, setAdding] = useState(false)
  const [newName, setNewName] = useState('')
  // What went wrong with the add ceremony that is not a request failure: an unsupported browser, a cancelled prompt.
  const [addNote, setAddNote] = useState('')
  const [editId, setEditId] = useState<number | null>(null)
  const [editName, setEditName] = useState('')
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null)

  useEffect(() => {
    if (open) { setAdding(false); setNewName(''); setAddNote(''); setEditId(null); setRecoveryCodes(null) }
  }, [open])

  function failed(err: unknown, fallbackKey: string) {
    pushSnack((err as AxiosError<{ error?: string }>).response?.data?.error || t(fallbackKey), 'error')
  }

  async function addPasskey() {
    const name = newName.trim() || t('passkey.default_name')
    setAddNote('')
    // Said before anything is sent: asking the server to begin a ceremony the browser cannot finish only leaves a pending session.
    if (!webAuthnSupported()) { setAddNote(t('passkey.unsupported')); return }
    setBusy(true)
    try {
      const { session_id, publicKey } = await beginPasskeyEnroll()
      const attResp = await startRegistration({ optionsJSON: publicKey })
      const { recovery_codes } = await finishPasskeyEnroll(session_id, name, attResp)
      pushSnack(t('passkey.added'), 'success')
      setAdding(false)
      setNewName('')
      // The first passkey: show the one-time recovery codes before anything else, and tell the parent only when they are
      // acknowledged. A parent may react to onChanged by closing this dialog and leaving the page (the forced-enrolment page
      // does), which would take the codes, shown exactly once, away unseen.
      if (recovery_codes && recovery_codes.length > 0) setRecoveryCodes(recovery_codes)
      else onChanged()
    } catch (err) {
      if (ceremonyCancelled(err)) setAddNote(t('passkey.cancelled'))
      else failed(err, 'passkey.add_failed')
    } finally { setBusy(false) }
  }

  async function saveRename(id: number) {
    const name = editName.trim()
    if (!name) { setEditId(null); return }
    setBusy(true)
    try {
      await renamePasskey(id, name)
      setEditId(null)
      onChanged()
    } catch (err) {
      failed(err, 'passkey.rename_failed')
    } finally { setBusy(false) }
  }

  async function removePasskey(c: PasskeyCredential) {
    const ok = await confirm({
      title: t('passkey.confirm_delete_title'),
      message: t('passkey.confirm_delete_message', { name: c.name }),
      destructive: true,
      confirmText: t('passkey.delete'),
      initialFocus: 'cancel',
    })
    if (!ok) return
    setBusy(true)
    try {
      await deletePasskey(c.id)
      pushSnack(t('passkey.deleted'), 'success')
      onChanged()
    } catch (err) {
      failed(err, 'passkey.delete_failed')
    } finally { setBusy(false) }
  }

  const showingCodes = recoveryCodes !== null
  return (
    <Dialog open={open} onOpenChange={o => { if (!o && !busy && !showingCodes) onClose() }}>
      <DialogContent showCloseButton={!showingCodes}>
        {showingCodes ? (
          <>
            <DialogHeader>
              <DialogTitle>{t('passkey.recovery_title')}</DialogTitle>
            </DialogHeader>
            <RecoveryCodeList codes={recoveryCodes} warning={t('passkey.recovery_warning')} />
            <DialogFooter><Button onClick={() => { setRecoveryCodes(null); onChanged() }}>{t('passkey.recovery_done')}</Button></DialogFooter>
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>{t('passkey.title')}</DialogTitle>
              <DialogDescription>{t('passkey.intro')}</DialogDescription>
            </DialogHeader>

            {credentials.length === 0 && !adding && <EmptyState compact title={t('passkey.list_empty')} />}
            {credentials.length > 0 && (
              <ul className="divide-y">
                {credentials.map(c => (
                  <li key={c.id} className="flex items-center justify-between gap-2 py-2 first:pt-0 last:pb-0">
                    {editId === c.id ? (
                      <>
                        <Input
                          autoFocus aria-label={t('passkey.rename')} value={editName} onChange={e => setEditName(e.target.value)}
                          onKeyDown={e => {
                            if (e.key === 'Enter') { e.preventDefault(); void saveRename(c.id) }
                            // Escape ends the edit, not the dialog.
                            if (e.key === 'Escape') { e.stopPropagation(); setEditId(null) }
                          }}
                        />
                        <div className="flex shrink-0">
                          <IconButton label={t('passkey.rename')} disabled={busy} onClick={() => void saveRename(c.id)}><IconCheck /></IconButton>
                          <IconButton label={t('passkey.cancel')} onClick={() => setEditId(null)}><IconClose /></IconButton>
                        </div>
                      </>
                    ) : (
                      <>
                        <div className="min-w-0">
                          <div className="truncate font-medium" title={c.name}>{c.name}</div>
                          <div className="text-xs text-muted-foreground">{t('passkey.added_at', { date: new Date(c.created_at).toLocaleDateString() })}</div>
                        </div>
                        <div className="flex shrink-0">
                          <IconButton label={t('passkey.rename')} disabled={busy} onClick={() => { setEditId(c.id); setEditName(c.name) }}><IconEdit /></IconButton>
                          <IconButton label={t('passkey.delete')} disabled={busy} onClick={() => void removePasskey(c)}><IconDelete /></IconButton>
                        </div>
                      </>
                    )}
                  </li>
                ))}
              </ul>
            )}

            {adding && (
              <form className="grid gap-3" onSubmit={e => { e.preventDefault(); void addPasskey() }}>
                <Field label={t('passkey.name_label')}>
                  <Input autoFocus placeholder={t('passkey.default_name')} value={newName} onChange={e => setNewName(e.target.value)} />
                </Field>
                {addNote && <Alert><AlertDescription>{addNote}</AlertDescription></Alert>}
                <div className="flex justify-end gap-2">
                  <Button type="button" variant="outline" disabled={busy} onClick={() => { setAdding(false); setNewName(''); setAddNote('') }}>{t('passkey.cancel')}</Button>
                  <AsyncButton type="submit" pending={busy}>{t('passkey.continue')}</AsyncButton>
                </div>
              </form>
            )}

            <DialogFooter>
              {available && !adding && <Button variant="outline" className="sm:mr-auto" disabled={busy} onClick={() => setAdding(true)}><IconAdd />{t('passkey.add')}</Button>}
              <Button variant="outline" disabled={busy} onClick={onClose}>{t('passkey.close')}</Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
