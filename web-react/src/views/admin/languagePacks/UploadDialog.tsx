import { useMemo, useRef, useState, type ChangeEvent, type DragEvent } from 'react'
import { IconUploadFile } from '@/lib/icons'
import { saveLocale, MAX_LOCALE_PACK_BYTES } from '@/api/locales'
import { AsyncButton } from '@/components/common/AsyncButton'
import { describeError } from '@/components/feedback/ErrorState'
import { pushSnack } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Textarea } from '@/components/ui/textarea'
import { cn } from '@/lib/utils'
import { useAdminT } from '../useAdminT'
import { checkPack } from './packCheck'

/**
 * Upload a language pack: pick or drop the file, or paste its text. Every problem the text has is
 * listed under it before anything is sent; what the server still refuses is shown in the dialog, in
 * the server's own words, with the text kept so it can be fixed in place. A short task, so a dialog.
 */
export function UploadDialog({ onClose, onUploaded }: { onClose: () => void; onUploaded: () => Promise<unknown> }) {
  const t = useAdminT()
  const [text, setText] = useState('')
  const [serverError, setServerError] = useState('')
  const [dragging, setDragging] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const check = useMemo(() => checkPack(text), [text])
  const blocked = text.trim() === '' || check.problems.length > 0

  async function readFile(file: File | undefined) {
    if (!file) return
    try {
      setText(await file.text())
      setServerError('')
    } catch {
      pushSnack(t('admin:languagePacks.toast.read_failed'), 'error')
    }
  }
  function onPick(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    // Reset the input so choosing the same file again re-fires change.
    e.target.value = ''
    void readFile(file)
  }
  // Dropping is an addition to the button, never the only way in.
  function onDrop(e: DragEvent) {
    e.preventDefault()
    setDragging(false)
    void readFile(e.dataTransfer.files?.[0])
  }

  async function submit() {
    if (!check.pack || check.problems.length > 0) return
    setServerError('')
    try {
      await saveLocale(check.pack, { quiet: true })
      await onUploaded()
      pushSnack(t('admin:languagePacks.toast.saved'), 'success')
      // The switcher's language list is built at boot, so a freshly uploaded pack only appears after a reload.
      pushSnack(t('admin:languagePacks.toast.reload_hint'), 'info')
      onClose()
    } catch (err) {
      setServerError(describeError(err) ?? t('admin:languagePacks.upload_failed'))
    }
  }

  return (
    <Dialog open onOpenChange={o => { if (!o) onClose() }}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t('admin:languagePacks.upload_title')}</DialogTitle>
          <DialogDescription>{t('admin:languagePacks.upload_help')}</DialogDescription>
        </DialogHeader>
        <div
          onDragOver={e => { e.preventDefault(); setDragging(true) }}
          onDragLeave={() => setDragging(false)}
          onDrop={onDrop}
          className={cn('flex flex-wrap items-center gap-3 rounded-lg border border-dashed p-3', dragging && 'border-ring bg-muted')}
        >
          <Button variant="outline" onClick={() => input.current?.click()}><IconUploadFile />{t('admin:languagePacks.choose_file')}</Button>
          <span className="text-sm text-muted-foreground">{t('admin:languagePacks.drop_hint')}</span>
          <input ref={input} type="file" accept="application/json,.json" hidden tabIndex={-1} aria-hidden onChange={onPick} data-testid="pack-file" />
        </div>
        <Field label={t('admin:languagePacks.paste_label')} hint={t('admin:languagePacks.size_hint', { max: Math.round(MAX_LOCALE_PACK_BYTES / 1024) })}>
          <Textarea rows={10} spellCheck={false} className="max-h-[40dvh] overflow-auto font-mono text-xs md:text-xs"
            placeholder={t('admin:languagePacks.paste_placeholder')} value={text}
            onChange={e => { setText(e.target.value); setServerError('') }} />
        </Field>
        {check.pack && check.problems.length === 0 && (
          <p className="text-sm" data-testid="pack-summary">
            {t('admin:languagePacks.summary', { name: check.pack.name, code: check.pack.code, count: Object.keys(check.pack.namespaces).length })}
          </p>
        )}
        {check.problems.length > 0 && (
          <Alert variant="destructive">
            <AlertTitle>{t('admin:languagePacks.problems_title', { count: check.problems.length })}</AlertTitle>
            <AlertDescription>
              <ul className="list-disc pl-4">
                {check.problems.map((p, i) => <li key={i}>{t(`admin:languagePacks.problem.${p.key}`, p.values)}</li>)}
              </ul>
            </AlertDescription>
          </Alert>
        )}
        {serverError && (
          <Alert variant="destructive">
            <AlertTitle>{t('admin:languagePacks.server_refused')}</AlertTitle>
            <AlertDescription className="font-mono text-xs break-words">{serverError}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t('common:actions.cancel')}</Button>
          <AsyncButton disabled={blocked} onClick={submit}>{t('admin:languagePacks.upload')}</AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
