import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { destErrorOf, type DestExemption } from '@/api/accessControl'
import { AsyncButton } from '@/components/common/AsyncButton'
import { pushSnack } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import UserAutocomplete from '@/components/UserAutocomplete'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useDestStatus, useSaveDestExemption } from '@/query/accessControl'
import { useUserDetail } from '@/query/users'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes } from '@/utils/accessControl'
import { discardChangesConfirm } from '../confirmCopy'
import { reportDestError } from '../destErrors'

type Expiry = 'forever' | 'h24' | 'd7' | 'custom'
const HOUR = 3_600_000
const D = 'admin:access_control.exemptions.dialog.'
const MAX_REASON = 255

/** datetime-local wants local wall time without a zone. */
function toLocalInput(ms: number): string {
  const d = new Date(ms - new Date(ms).getTimezoneOffset() * 60_000)
  return d.toISOString().slice(0, 16)
}

/**
 * S5's add / edit dialog, shared by the exemptions sheet and the account
 * drawer's access tab. `lockedUserId` pins the account (the drawer); `existing`
 * turns it into an edit of that exemption's reason and expiry. No first-publish
 * confirmation: an exemption only ever loosens.
 */
export default function AddExemptionDialog({ open, onClose, lockedUserId, existing }: {
  open: boolean
  onClose: () => void
  lockedUserId?: number
  existing?: DestExemption
}) {
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const save = useSaveDestExemption(scope)
  const status = useDestStatus(scope, { enabled: open })
  const [userId, setUserId] = useState<number | null>(lockedUserId ?? existing?.user_id ?? null)
  const [reason, setReason] = useState(existing?.reason ?? '')
  const [expiry, setExpiry] = useState<Expiry>(existing?.expires_at ? 'custom' : 'forever')
  const [custom, setCustom] = useState(existing?.expires_at ? toLocalInput(existing.expires_at) : '')
  const [exists, setExists] = useState(false)
  const [touched, setTouched] = useState(false)
  const picked = useUserDetail(scope, open && !existing ? userId ?? 0 : 0)

  // A dialog that closes and reopens starts from what it is given.
  useEffect(() => {
    if (!open) return
    setUserId(lockedUserId ?? existing?.user_id ?? null)
    setReason(existing?.reason ?? '')
    setExpiry(existing?.expires_at ? 'custom' : 'forever')
    setCustom(existing?.expires_at ? toLocalInput(existing.expires_at) : '')
    setExists(false)
    setTouched(false)
  }, [open, lockedUserId, existing])

  const dirty = open && (reason !== (existing?.reason ?? '') || expiry !== (existing?.expires_at ? 'custom' : 'forever')
    || (!existing && lockedUserId === undefined && userId !== null))
  const guard = useDirtyClose(dirty, discardChangesConfirm(t))
  const close = () => guard(onClose)

  const customMs = custom ? new Date(custom).getTime() : NaN
  const customBad = expiry === 'custom' && (!Number.isFinite(customMs) || customMs <= Date.now())
  const reasonBad = reason.trim() === ''
  const userBad = userId === null
  const invalid = reasonBad || userBad || customBad

  const expiresAt = (): number | null => {
    switch (expiry) {
      case 'h24': return Date.now() + 24 * HOUR
      case 'd7': return Date.now() + 7 * 24 * HOUR
      case 'custom': return customMs
      default: return null
    }
  }

  const submit = async () => {
    setTouched(true)
    if (invalid || userId === null) return
    try {
      await save.mutateAsync({ userId, create: !existing, reason: reason.trim(), expires_at: expiresAt() })
      const upn = existing?.upn ?? picked.data?.upn ?? `#${userId}`
      pushSnack(t(existing ? `${D}updated_snack` : `${D}added_snack`, { upn }), 'success')
      onClose()
    } catch (err) {
      if (destErrorOf(err)?.code === 'dest_exemption_exists') setExists(true)
      else reportDestError(t, err)
    }
  }

  const minutes = applyMinutes(status.data)
  const minutesText = minutes === null ? t('admin:access_control.confirm.minutes_unknown') : t('admin:access_control.confirm.minutes', { count: minutes })

  const lockedName = existing?.upn ?? picked.data?.upn ?? `#${lockedUserId}`
  return (
    <Dialog open={open} onOpenChange={next => { if (!next) close() }}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
        <DialogHeader><DialogTitle>{t(existing ? `${D}title_edit` : `${D}title_add`)}</DialogTitle></DialogHeader>
        <div className="grid gap-3">
          {existing || lockedUserId !== undefined
            ? <p className="font-semibold">{lockedName}</p>
            : (
              <Field label={t(`${D}user`)} required error={exists ? t(`${D}exists`) : touched && userBad ? t(`${D}user_required`) : undefined}>
                {() => <UserAutocomplete value={userId} onChange={id => { setUserId(id); setExists(false) }} label={t(`${D}user`)} fullWidth />}
              </Field>
            )}
          {/* An account that is already exempt is also told on the pinned form, which has no picker to hang it on. */}
          {(existing || lockedUserId !== undefined) && exists && <p role="alert" className="text-xs text-destructive">{t(`${D}exists`)}</p>}
          <Field label={t(`${D}reason`)} required hint={`${reason.length} / ${MAX_REASON}`}
            error={touched && reasonBad ? t(`${D}reason_required`) : undefined}>
            <Input value={reason} onChange={e => setReason(e.target.value.slice(0, MAX_REASON))} />
          </Field>
          <Field label={t(`${D}expiry`)} hint={expiry === 'h24' ? t(`${D}h24_note`) : undefined}>
            {p => <SegmentedControl<Expiry> value={expiry} onChange={setExpiry} aria-labelledby={p.labelId} className="w-full [&>button]:min-w-0 [&>button]:flex-1 [&>button]:px-1.5"
              options={[
                { value: 'forever', label: t(`${D}forever`) }, { value: 'h24', label: t(`${D}h24`) },
                { value: 'd7', label: t(`${D}d7`) }, { value: 'custom', label: t(`${D}custom`) },
              ]} />}
          </Field>
          {expiry === 'custom' && (
            <Field label={t(`${D}custom_label`)} error={customBad && custom !== '' ? t(`${D}custom_past`) : undefined}>
              <Input type="datetime-local" value={custom} onChange={e => setCustom(e.target.value)} />
            </Field>
          )}
          <p className="text-sm text-muted-foreground">{t(`${D}apply_note`, { minutes: minutesText })}</p>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={close}>{t(`${D}cancel`)}</Button>
          <AsyncButton onClick={submit}>{t(existing ? `${D}save_edit` : `${D}save`)}</AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
