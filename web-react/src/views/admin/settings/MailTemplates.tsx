import { useEffect, useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { previewMailTemplate, putMailTemplate, resetMailTemplate, type MailReminderKind, type MailSettings, type MailTemplate } from '@/api/settings'
import { AsyncButton } from '@/components/common/AsyncButton'
import { pushSnack } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { confirm } from '@/components/ConfirmHost'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { IconHelp } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { useTabParam } from '@/hooks/useTabParam'
import { settingsKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { Notice } from '../Notice'
import { useAdminT } from '../useAdminT'
import { MAIL_VARS } from './mailVars'
import { settingId } from './model'
import { Group } from './parts'

type Saved = { settings: MailSettings; templates: MailTemplate[] }

const KINDS: MailReminderKind[] = ['expire_before', 'expired', 'traffic_low', 'traffic_exhausted', 'account_disabled', 'account_enabled', 'announcement', 'blocked_client', 'password_reset', 'email_verify', 'login_2fa']
// Grouped by purpose so the list reads as two short lists rather than eleven names.
const KIND_GROUPS: { label: string; kinds: MailReminderKind[] }[] = [
  { label: 'group_ops', kinds: KINDS.slice(0, 8) },
  { label: 'group_account', kinds: KINDS.slice(8) },
]

const blank = (kind: MailReminderKind): MailTemplate => ({ kind, subject: '', body: '', enabled: false })
const same = (a: MailTemplate, b: MailTemplate) => a.subject === b.subject && a.body === b.body && a.enabled === b.enabled

/** The reminder templates: one record per kind on the server, so each is edited and saved on its own. */
export function MailTemplates({ templates, onDirtyChange }: { templates: MailTemplate[]; onDirtyChange: (dirty: boolean) => void }) {
  const t = useAdminT()
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const [active, setActive] = useTabParam<MailReminderKind>('tpl', 'expire_before', KINDS)
  const [edits, setEdits] = useState<Partial<Record<MailReminderKind, MailTemplate>>>({})
  const [busy, setBusy] = useState(false)
  const [previewing, setPreviewing] = useState(false)
  const [preview, setPreview] = useState<{ subject: string; body: string; kind: MailReminderKind } | null>(null)

  // A server that does not know a kind yet (an older binary) answers nothing for it: the editor starts from an empty
  // template rather than rendering nothing, and "Reset to default" pulls the real one.
  const savedOf = (kind: MailReminderKind) => templates.find(x => x.kind === kind) ?? blank(kind)
  const current = edits[active] ?? savedOf(active)
  const missing = !templates.some(x => x.kind === active)
  const dirtyKinds = useMemo(() => KINDS.filter(k => edits[k] && !same(edits[k]!, templates.find(x => x.kind === k) ?? blank(k))), [edits, templates])
  const dirty = dirtyKinds.length > 0
  useEffect(() => { onDirtyChange(dirty) }, [dirty, onDirtyChange])
  useEffect(() => () => onDirtyChange(false), [onDirtyChange])

  const patch = (p: Partial<MailTemplate>) => setEdits(e => ({ ...e, [active]: { ...current, ...p } }))
  const adopt = (saved: MailTemplate) => {
    queryClient.setQueryData<Saved>(settingsKeys.mail(scope), d => d && ({ ...d, templates: d.templates.some(x => x.kind === saved.kind) ? d.templates.map(x => (x.kind === saved.kind ? saved : x)) : [...d.templates, saved] }))
    setEdits(e => { const next = { ...e }; delete next[saved.kind]; return next })
  }

  async function save() {
    setBusy(true)
    try { adopt(await putMailTemplate(current)); pushSnack(t('settings.mail.saved'), 'success') } catch { /* reported by the request layer */ } finally { setBusy(false) }
  }
  async function reset() {
    if (!(await confirm({
      title: t('settings.mail.reset_confirm_title'), message: t('settings.mail.reset_confirm_body'),
      confirmText: t('settings.mail.reset_confirm_ok'), destructive: true, initialFocus: 'cancel',
    }))) return
    setBusy(true)
    try { adopt(await resetMailTemplate(active)); pushSnack(t('settings.mail.reset_done'), 'success') } catch { /* reported by the request layer */ } finally { setBusy(false) }
  }
  async function showPreview() {
    setPreviewing(true)
    try { setPreview({ ...(await previewMailTemplate(current)), kind: active }) } catch { /* reported by the request layer */ } finally { setPreviewing(false) }
  }

  const label = (k: MailReminderKind) => t(`settings.mail.kind.${k}`) + (dirtyKinds.includes(k) ? ` (${t('settings.unsaved_short')})` : '')
  return (
    <Group id="setting-mail_templates" title={t('settings.mail.section_templates')}>
      <div className="grid gap-4 md:grid-cols-[14rem_minmax(0,1fr)]">
        {/* A list on a wide screen (it scales as the set grows), a select on a phone. */}
        <nav aria-label={t('settings.mail.section_templates')} className="hidden md:block">
          {KIND_GROUPS.map(g => (
            <div key={g.label} className="mb-3">
              <p className="px-2 pb-1 text-xs font-medium text-muted-foreground">{t(`settings.mail.${g.label}`)}</p>
              <ul className="grid gap-0.5">
                {g.kinds.map(k => (
                  <li key={k}>
                    <button type="button" aria-current={active === k ? 'true' : undefined} onClick={() => setActive(k)}
                      className={cn('w-full rounded-md px-2 py-1.5 text-left text-sm outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring/50', active === k && 'bg-accent font-medium')}>
                      {label(k)}
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </nav>
        <div className="md:hidden">
          <Field label={t('settings.mail.template_kind')}>
            <NativeSelect value={active} onChange={e => setActive(e.target.value as MailReminderKind)}>
              {KIND_GROUPS.map(g => (
                <optgroup key={g.label} label={t(`settings.mail.${g.label}`)}>
                  {g.kinds.map(k => <option key={k} value={k}>{label(k)}</option>)}
                </optgroup>
              ))}
            </NativeSelect>
          </Field>
        </div>

        <div className="grid min-w-0 gap-4">
          {missing && !edits[active] && <Notice tone="info">{t('settings.mail.tpl_missing_hint')}</Notice>}
          <div className="flex flex-wrap items-start justify-between gap-2">
            <Field orientation="horizontal" className="gap-4" label={t('settings.mail.tpl_enabled')}>
              <Switch id={settingId(`mail_template_${active}_enabled`)} checked={current.enabled} onCheckedChange={c => patch({ enabled: c })} />
            </Field>
            <VariablesButton />
          </div>
          <Field label={t('settings.mail.tpl_subject')}>
            <Input id={settingId(`mail_template_${active}_subject`)} value={current.subject} onChange={e => patch({ subject: e.target.value })} />
          </Field>
          <Field label={t('settings.mail.tpl_body')}>
            <Textarea id={settingId(`mail_template_${active}_body`)} rows={12} className="font-mono text-[13px]" value={current.body} onChange={e => patch({ body: e.target.value })} />
          </Field>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <Button type="button" variant="ghost" disabled={busy} onClick={() => void reset()}>{t('settings.mail.tpl_reset')}</Button>
            <div className="flex flex-wrap gap-2">
              <AsyncButton type="button" variant="outline" pending={previewing} onClick={showPreview}>{t('settings.mail.tpl_preview')}</AsyncButton>
              <AsyncButton type="button" pending={busy} disabled={!edits[active]} onClick={save}>{t('settings.mail.tpl_save')}</AsyncButton>
            </div>
          </div>
        </div>
      </div>

      <Dialog open={!!preview} onOpenChange={o => { if (!o) setPreview(null) }}>
        <DialogContent className="sm:max-w-3xl">
          <DialogHeader><DialogTitle>{preview && t('settings.mail.preview_title', { kind: t(`settings.mail.kind.${preview.kind}`) })}</DialogTitle></DialogHeader>
          <div className="grid gap-3">
            <div>
              <p className="mb-1 text-xs text-muted-foreground">{t('settings.mail.preview_subject')}</p>
              <p className="rounded-lg border bg-muted/30 px-3 py-2 text-sm break-words">{preview?.subject}</p>
            </div>
            <div>
              <p className="mb-1 text-xs text-muted-foreground">{t('settings.mail.preview_body')}</p>
              {/* A mail is HTML written by an admin: shown with scripts off. */}
              <iframe title={t('settings.mail.preview_body')} sandbox="" srcDoc={preview?.body || ''} className="h-[28rem] w-full rounded-lg border bg-white" />
            </div>
          </div>
          <DialogFooter><Button variant="outline" onClick={() => setPreview(null)}>{t('settings.mail.preview_close')}</Button></DialogFooter>
        </DialogContent>
      </Dialog>
    </Group>
  )
}

/** The variable cheat sheet, beside the editor it is for. */
function VariablesButton() {
  const t = useAdminT()
  return (
    <Popover>
      <PopoverTrigger render={<Button type="button" variant="ghost" />}><IconHelp />{t('settings.mail.tpl_vars_button')}</PopoverTrigger>
      <PopoverContent align="end" className="max-h-[min(32rem,70vh)] w-[min(30rem,92vw)] overflow-y-auto">
        <p className="mb-1 text-sm font-medium">{t('settings.mail.tpl_vars_title')}</p>
        <p className="mb-3 text-xs text-muted-foreground">{t('settings.mail.tpl_vars_hint')}</p>
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-xs">
          {MAIL_VARS.map(([code, key]) => (
            <div key={code} className="contents">
              <dt><code className="rounded bg-muted px-1.5 py-0.5 whitespace-nowrap">{code}</code></dt>
              <dd className="self-center text-muted-foreground">{t(`settings.mail.tpl_vars.${key}`)}</dd>
            </div>
          ))}
        </dl>
      </PopoverContent>
    </Popover>
  )
}
