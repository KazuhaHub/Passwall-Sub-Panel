import { useEffect, useState } from 'react'
import { getMyRules, updateMyRules } from '@/api/me'
import { AsyncButton } from '@/components/common/AsyncButton'
import { pushSnack } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import { confirm } from '@/components/ConfirmHost'
import { useTranslation } from 'react-i18next'
import { useMeT } from './useMeT'

/**
 * The person's own routing rules, one per line. They are read each time the dialog opens, so it never shows a copy that is
 * older than the visit. When the admin has turned self-service editing off, the same text is shown read-only and the only
 * button is OK; a save the server refuses with 403 says so in the admin's terms instead of "failed". Closing with edits
 * asks first, since the text is the one thing in this page that is typed at length.
 */
export function RulesDialog({ open, canEdit, onClose }: { open: boolean; canEdit: boolean; onClose: () => void }) {
  const { t } = useMeT()
  const { t: tc } = useTranslation('common')
  const [text, setText] = useState('')
  const [saved, setSaved] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!open) return
    let live = true
    setBusy(true)
    getMyRules()
      .then(r => { if (live) { setText(r); setSaved(r) } })
      .catch(() => { /* the request layer already told the person; the box stays empty */ })
      .finally(() => { if (live) setBusy(false) })
    return () => { live = false }
  }, [open])

  const dirty = text.trim() !== saved.trim()

  async function requestClose() {
    if (busy) return
    if (canEdit && dirty) {
      const leave = await confirm({
        title: tc('ui.form.unsaved'), message: tc('ui.form.unsaved_leave'), confirmText: tc('ui.form.discard'),
        destructive: true, initialFocus: 'cancel',
      })
      if (!leave) return
    }
    onClose()
  }

  async function save() {
    setBusy(true)
    try {
      const next = text.trim()
      await updateMyRules(next)
      setText(next); setSaved(next)
      pushSnack(t('rules.saved'), 'success')
      onClose()
    } catch (e) {
      // 403 = the admin turned self-service editing off. The backend's own text is English only, so it is said here.
      const status = (e as { response?: { status?: number } })?.response?.status
      pushSnack(status === 403 ? t('rules.disabled_toast') : t('rules.save_failed'), 'error')
    } finally { setBusy(false) }
  }

  return (
    <Dialog open={open} onOpenChange={o => { if (!o) void requestClose() }}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t('rules.title')}</DialogTitle>
          <DialogDescription>{canEdit ? t('rules.hint') : t('rules.readonly_hint')}</DialogDescription>
        </DialogHeader>
        {busy && !text
          ? <Skeleton className="h-60 w-full" />
          : (
            <Textarea
              aria-label={t('rules.title')} className="min-h-60 font-mono text-[0.8125rem]" spellCheck={false}
              value={text} onChange={e => setText(e.target.value)} placeholder={t('rules.placeholder')}
              readOnly={!canEdit} disabled={busy}
            />
          )}
        <DialogFooter>
          {/* One click to a starting point that can then be changed, instead of staring at the placeholder. */}
          {canEdit && !text.trim() && !busy && (
            <Button type="button" variant="ghost" className="sm:mr-auto" onClick={() => setText(t('rules.placeholder'))}>{t('rules.insert_example')}</Button>
          )}
          <Button type="button" variant="outline" disabled={busy} onClick={() => void requestClose()}>{canEdit ? t('common.cancel') : t('common.ok')}</Button>
          {canEdit && <AsyncButton disabled={!dirty} pending={busy} onClick={save}>{t('common.ok')}</AsyncButton>}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
