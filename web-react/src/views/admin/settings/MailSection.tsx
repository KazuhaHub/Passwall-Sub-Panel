import { memo, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { putMailSettings, sendTestMail, type MailSettings, type MailTemplate } from '@/api/settings'
import { AsyncButton } from '@/components/common/AsyncButton'
import { ErrorState } from '@/components/feedback/ErrorState'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { pushSnack } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { SecretField } from '@/components/form/SecretField'
import { NumberField } from '@/components/form/NumberField'
import { Input } from '@/components/ui/input'
import { settingsKeys } from '@/query/keys'
import { useMailSettings } from '@/query/settings'
import { useQueryScope } from '@/query/useQueryScope'
import { validateEmail, validateHost } from '@/utils/validators'
import { useAdminT } from '../useAdminT'
import { MailTemplates } from './MailTemplates'
import { settingId } from './model'
import { Group, Pair, SectionForm, SelectRow, SwitchField, TextRow } from './parts'
import { SubTabs, type SubPanel } from './SubTabs'

type Saved = { settings: MailSettings; templates: MailTemplate[] }

/**
 * Mail has its own read and its own writes (the settings record does not carry these), shared by its two sub-tabs.
 * The SMTP form saves with its own Save; each template saves on its own button, because they are separate records on
 * the server. The read happens when a sub-tab is first shown, as the old tab's did.
 */
function MailGate({ children }: { children: (data: Saved) => ReactNode }) {
  const t = useAdminT()
  const scope = useQueryScope()
  const query = useMailSettings(scope)
  useEffect(() => { void query.refetch() }, []) // eslint-disable-line react-hooks/exhaustive-deps

  if (query.isError && !query.data) return <ErrorState title={t('settings.mail_load_failed')} reason={query.error} retry={() => query.refetch()} />
  if (!query.data) return <CardSkeleton lines={6} />
  return <>{children(query.data)}</>
}

const PANELS = {
  smtp: ({ onDirtyChange }) => <MailGate>{data => <SmtpForm data={data} onDirtyChange={onDirtyChange} />}</MailGate>,
  templates: ({ onDirtyChange }) => <MailGate>{data => <MailTemplates templates={data.templates} onDirtyChange={onDirtyChange} />}</MailGate>,
} satisfies Record<string, SubPanel>

/** Mail: the sending server and sender with a test send, and the reminder templates. */
export const MailSection = memo(function MailSection({ active }: { active: boolean }) {
  return <SubTabs section="mail" active={active} panels={PANELS} />
})

type Field = 'smtp_host' | 'smtp_port' | 'from_email'

function SmtpForm({ data, onDirtyChange }: { data: Saved; onDirtyChange: (dirty: boolean) => void }) {
  const t = useAdminT()
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const saved = data.settings
  const [edits, setEdits] = useState<Partial<MailSettings>>({})
  const [touched, setTouched] = useState<ReadonlySet<Field>>(new Set())
  const [attempted, setAttempted] = useState(false)
  const [pending, setPending] = useState(false)
  const [testTo, setTestTo] = useState('')
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<{ ok: boolean; text: string } | null>(null)

  const v: MailSettings = { ...saved, ...edits }
  const patch = (p: Partial<MailSettings>) => setEdits(e => ({ ...e, ...p }))
  const dirty = useMemo(() => (Object.keys(edits) as (keyof MailSettings)[]).some(k => (k === 'smtp_password' ? !!edits[k] : edits[k] !== saved[k])), [edits, saved])
  useEffect(() => { onDirtyChange(dirty) }, [dirty, onDirtyChange])
  useEffect(() => () => onDirtyChange(false), [onDirtyChange])

  // Only nag about the required SMTP fields once mail is switched on: until then the placeholders are aspirational.
  const problems = useMemo<Partial<Record<Field, string>>>(() => {
    if (!v.enabled) return {}
    const out: Partial<Record<Field, string>> = {}
    const host = validateHost(v.smtp_host, { required: true }); if (host) out.smtp_host = t(host)
    const email = validateEmail(v.from_email, { required: true }); if (email) out.from_email = t(email)
    return out
  }, [v.enabled, v.smtp_host, v.from_email, t])
  const shown = (f: Field) => (attempted || touched.has(f) ? problems[f] : undefined)
  const blur = (f: Field) => setTouched(s => (s.has(f) ? s : new Set(s).add(f)))

  async function save() {
    const first = (Object.keys(problems) as Field[])[0]
    if (first) { setAttempted(true); setTimeout(() => document.getElementById(settingId(`mail_${first}`))?.focus(), 0); return }
    setPending(true)
    try {
      const payload: MailSettings = { ...v }
      // A password is sent only when one was typed. An empty one means "keep the stored one", and sending it would be a request to change it.
      if (!edits.smtp_password) delete payload.smtp_password
      const answer = await putMailSettings(payload)
      queryClient.setQueryData<Saved>(settingsKeys.mail(scope), d => (d ? { ...d, settings: answer } : d))
      setEdits({}); setTouched(new Set()); setAttempted(false)
      pushSnack(t('settings.mail.saved'), 'success')
    } catch { /* reported by the request layer */ } finally { setPending(false) }
  }

  async function test() {
    const err = validateEmail(testTo, { required: true })
    if (err) { setTestResult({ ok: false, text: t(err) }); return }
    setTesting(true); setTestResult(null)
    try {
      await sendTestMail(testTo)
      setTestResult({ ok: true, text: t('settings.mail.test_sent_to', { to: testTo }) })
      pushSnack(t('settings.mail.test_sent'), 'success')
    } catch (e) {
      const reason = (e as { response?: { data?: { error?: string } }; message?: string })
      setTestResult({ ok: false, text: t('settings.mail.test_failed', { reason: reason.response?.data?.error || reason.message || '' }) })
    } finally { setTesting(false) }
  }

  return (
    <SectionForm dirty={dirty} pending={pending} onSave={() => void save()} onDiscard={() => { setEdits({}); setTouched(new Set()); setAttempted(false) }}>
      <Group title={t('settings.mail.enabled')}>
        <SwitchField name="mail_enabled" label={t('settings.mail.enabled')} checked={v.enabled} onChange={c => patch({ enabled: c })} />
      </Group>

      <Group title={t('settings.mail.section_smtp')}>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-[minmax(0,1fr)_9rem]">
          <TextRow name="mail_smtp_host" label={t('settings.mail.smtp_host')} required={v.enabled} value={v.smtp_host}
            onChange={s => patch({ smtp_host: s })} onBlur={() => blur('smtp_host')} error={shown('smtp_host')} />
          <Field label={t('settings.mail.smtp_port')} required={v.enabled}>
            <NumberField id={settingId('mail_smtp_port')} value={v.smtp_port} integer min={v.enabled ? 1 : 0} max={65535} onChange={n => { if (n !== null) patch({ smtp_port: n }) }} />
          </Field>
        </div>
        <SelectRow<MailSettings['encryption']> name="mail_encryption" label={t('settings.mail.encryption')} value={v.encryption} onChange={e => patch({ encryption: e })}
          options={[
            { value: 'none', label: t('settings.mail.encryption_none') },
            { value: 'starttls', label: t('settings.mail.encryption_starttls') },
            { value: 'tls', label: t('settings.mail.encryption_tls') },
          ]} />
        <TextRow name="mail_smtp_username" label={t('settings.mail.smtp_username')} value={v.smtp_username} onChange={s => patch({ smtp_username: s })} autoComplete="off" />
        <Field label={t('settings.mail.smtp_password')} hint={saved.has_smtp_password ? t('settings.secret_hint') : undefined}>
          <SecretField id={settingId('mail_smtp_password')} value={v.smtp_password ?? ''} isSet={saved.has_smtp_password} onChange={p => patch({ smtp_password: p })} />
        </Field>
      </Group>

      <Group title={t('settings.mail.section_sender')}>
        <Pair>
          <TextRow name="mail_from_email" label={t('settings.mail.from_email')} required={v.enabled} value={v.from_email}
            onChange={s => patch({ from_email: s })} onBlur={() => blur('from_email')} error={shown('from_email')} />
          <TextRow name="mail_from_name" label={t('settings.mail.from_name')} value={v.from_name} onChange={s => patch({ from_name: s })} />
        </Pair>
      </Group>

      {/* The test uses what is SAVED on the server, not what is typed above: say so beside the action. */}
      <Group title={t('settings.mail.section_test')} description={t('settings.mail.test_hint')}>
        <div className="flex flex-wrap items-start gap-3">
          <div className="min-w-60 flex-1">
            <Field label={t('settings.mail.test_to')}>
              <Input id={settingId('mail_test_to')} type="email" value={testTo} onChange={e => setTestTo(e.target.value)} />
            </Field>
          </div>
          <AsyncButton type="button" variant="outline" className="mt-6" pending={testing} disabled={!testTo} onClick={test}>{t('settings.mail.test_send')}</AsyncButton>
        </div>
        <p role="status" className={testResult?.ok ? 'text-sm text-success-ink' : 'text-sm text-destructive'}>{testResult?.text}</p>
      </Group>
    </SectionForm>
  )
}
