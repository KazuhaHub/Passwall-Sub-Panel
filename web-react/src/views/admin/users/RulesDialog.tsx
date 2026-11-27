import { useEffect, useState } from 'react'
import type { User } from '@/api/types'
import { getUserRules, updateUserRules } from '@/api/users'
import { AsyncButton } from '@/components/common/AsyncButton'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/feedback/toasts'
import { LoadingState } from '@/components/feedback/LoadingState'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Textarea } from '@/components/ui/textarea'
import { discardChangesConfirm } from '../accessControl/confirmCopy'
import { useAdminT } from './useAdminT'

/**
 * One user's personal rules, appended to their subscription: one rule per line. Read when the dialog opens; Save
 * is off until the text differs from what was stored, and closing with edits asks first.
 */
export function RulesDialog({ user, onClose }: { user: User; onClose: () => void }) {
  const t = useAdminT()
  const [text, setText] = useState('')
  const [saved, setSaved] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const dirty = text.trim() !== saved.trim()

  useEffect(() => {
    let live = true
    getUserRules(user.id)
      .then(v => { if (live) { setText(v); setSaved(v) } })
      .catch(() => { /* reported by the request layer; the empty box would read as "no rules", so close */ if (live) onClose() })
      .finally(() => { if (live) setLoading(false) })
    return () => { live = false }
    // The dialog is mounted per user; onClose is the page's and changes identity each render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [user.id])

  async function close() {
    if (saving) return
    if (dirty && !(await confirm(discardChangesConfirm(t)))) return
    onClose()
  }

  async function save() {
    setSaving(true)
    try {
      const v = text.trim()
      await updateUserRules(user.id, v)
      pushSnack(t('users.rules.saved'), 'success')
      onClose()
    } catch { /* reported by the request layer; the text is kept */ } finally { setSaving(false) }
  }

  return (
    <Dialog open onOpenChange={open => { if (!open) void close() }}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{t('users.rules.title', { upn: user.upn })}</DialogTitle>
          <DialogDescription>{t('users.rules.hint')}</DialogDescription>
        </DialogHeader>
        {loading
          ? <LoadingState />
          : <Textarea rows={12} className="font-mono text-[0.8125rem]" aria-label={t('users.rules.title', { upn: user.upn })} value={text}
              onChange={e => setText(e.target.value)} placeholder={t('users.rules.placeholder')} />}
        <DialogFooter>
          <Button variant="outline" disabled={loading || saving || !dirty} onClick={() => setText(saved)}>{t('users.rules.reset')}</Button>
          <Button variant="outline" disabled={saving} onClick={() => void close()}>{t('common:actions.cancel')}</Button>
          <AsyncButton pending={saving} disabled={loading || !dirty} onClick={save}>{t('users.rules.save')}</AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
