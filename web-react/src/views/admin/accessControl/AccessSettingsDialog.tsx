import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { destErrorOf, isDestUnavailable, type DestSettings } from '@/api/accessControl'
import PolicyField, { outOfRange, type FieldSpec } from '@/components/PolicyField'
import { confirm } from '@/components/ConfirmHost'
import { AsyncButton } from '@/components/common/AsyncButton'
import { DetailSheet } from '@/components/data/DetailSheet'
import { FormValidityProvider, useFormValidity } from '@/components/form/FormValidity'
import { ErrorState } from '@/components/feedback/ErrorState'
import { pushToast } from '@/components/feedback/toasts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { IconError, IconInfo } from '@/lib/icons'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useDestSettings, useSaveDestSettings } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import type { SettingsFocus } from './accessParams'
import { discardChangesConfirm, shortenRetentionConfirm } from './confirmCopy'
import { destErrorText } from './destErrors'
import RuleDataPanel from './RuleDataPanel'

const S = 'admin:access_control.settings.'

// The accepted ranges are the server's (internal/ports/access_control_settings.go:
// DestPolicyApplyMinSeconds 30–3600, DestListRefreshHours 6–168). The server
// refuses a value outside them, and the defaults come from the served
// `defaults`, never from here.
const FIELDS: (FieldSpec & { key: 'dest_policy_apply_min_seconds' | 'dest_list_refresh_hours' | 'dest_hit_retention_days' | 'dest_trial_retention_days' | 'dest_usage_retention_days'; focus: SettingsFocus })[] = [
  { key: 'dest_policy_apply_min_seconds', kind: 'number', label: `${S}apply_seconds`, hint: `${S}apply_seconds_hint`, min: 30, max: 3600, tail: true, focus: 'apply' },
  { key: 'dest_list_refresh_hours', kind: 'number', label: `${S}refresh_hours`, hint: `${S}refresh_hours_hint`, min: 6, max: 168, tail: true, focus: 'refresh' },
  // DestHitRetentionDays 1–365; the allowlist report-only data's own retention is 1–30.
  { key: 'dest_hit_retention_days', kind: 'number', label: `${S}hit_retention_days`, hint: `${S}hit_retention_days_hint`, min: 1, max: 365, tail: true, focus: 'retention' },
  { key: 'dest_trial_retention_days', kind: 'number', label: `${S}trial_retention_days`, hint: `${S}trial_retention_days_hint`, min: 1, max: 30, tail: true, focus: 'trial' },
  // DestUsageRetentionDays (per-site usage, tier B) 1–30.
  { key: 'dest_usage_retention_days', kind: 'number', label: `${S}usage_retention_days`, hint: `${S}usage_retention_days_hint`, min: 1, max: 30, tail: true, focus: 'usage' },
]

export interface AccessSettingsDialogProps {
  open: boolean
  focus?: SettingsFocus
  onClose: () => void
}

/**
 * DATA AND APPLY SETTINGS (S18, batch 1): the apply wait, the remote-list
 * refresh interval and the rule-data update mode (with the rule-data version
 * block). Only keys that changed are sent. A blank number is the default and is
 * never shown as 0.
 */
export default function AccessSettingsDialog(props: AccessSettingsDialogProps) {
  // The number boxes report an unusable draft ("1.5" in a whole-number box, a value outside its range) to
  // the provider, which keeps Save off while one is open: the sheet would otherwise store the cut value.
  return <FormValidityProvider><AccessSettings {...props} /></FormValidityProvider>
}

function AccessSettings({ open, focus, onClose }: AccessSettingsDialogProps) {
  const { blocked } = useFormValidity()
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const read = useDestSettings(scope, { enabled: open })
  const save = useSaveDestSettings(scope)
  const [draft, setDraft] = useState<DestSettings | null>(null)
  const [seeded, setSeeded] = useState(false)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const body = useRef<HTMLDivElement>(null)

  const stored = read.data?.settings
  const storedMode = (stored?.dest_rules_update_mode || read.data?.effective.dest_rules_update_mode || 'manual') as 'manual' | 'auto'

  // Seed the draft from the stored settings each time the dialog opens; a
  // refetch while it is open never overwrites what is being typed.
  useEffect(() => {
    if (!open) { setSeeded(false); setDraft(null); setErrors({}); return }
    if (stored && !seeded) {
      setDraft({ ...stored, dest_rules_update_mode: storedMode })
      setSeeded(true)
    }
  }, [open, stored, seeded, storedMode])

  useEffect(() => {
    if (!open || !draft || !focus) return
    const id = window.setTimeout(() => {
      const root = body.current
      if (!root) return
      if (focus === 'rules') root.querySelector('[data-focus="rules"]')?.scrollIntoView?.({ block: 'center' })
      else root.querySelector<HTMLInputElement>(`[data-focus="${focus}"] input`)?.focus()
    }, 0)
    return () => window.clearTimeout(id)
    // Once per open and focus request: seeding the draft is what makes the field exist.
  }, [open, focus, draft !== null]) // eslint-disable-line react-hooks/exhaustive-deps

  const changed = useMemo(() => {
    const out: Partial<DestSettings> = {}
    if (!draft || !stored) return out
    for (const f of FIELDS) {
      if ((draft[f.key] ?? 0) !== (stored[f.key] ?? 0)) out[f.key] = draft[f.key] ?? 0
    }
    if (draft.dest_rules_update_mode !== storedMode) out.dest_rules_update_mode = draft.dest_rules_update_mode
    return out
  }, [draft, stored, storedMode])
  const count = Object.keys(changed).length
  const invalid = !!draft && FIELDS.some(f => outOfRange(f, draft[f.key]))

  const guard = useDirtyClose(count > 0, discardChangesConfirm(t))
  const close = () => guard(onClose)

  const submit = async () => {
    setErrors({})
    // A shorter retention deletes records within the hour, for good: asked first (S20).
    // Each retention is asked about on its own, so a save that shortens two asks twice.
    for (const key of ['dest_hit_retention_days', 'dest_usage_retention_days'] as const) {
      if (!read.data || !(key in changed)) continue
      const next = changed[key] || read.data.defaults[key]
      const now = read.data.effective[key] ?? read.data.defaults[key]
      if (next && now && next < now && !(await confirm(shortenRetentionConfirm(t, next)))) return
    }
    try {
      await save.mutateAsync(changed)
      pushToast({ message: t(`${S}saved`), severity: 'success' })
      onClose()
    } catch (err) {
      const e = destErrorOf(err)
      if (e?.status === 400 && e.field) setErrors({ [e.field]: t(`${S}out_of_range`) })
      else setErrors({ _: destErrorText(t, err) })
    }
  }

  const err = (key: string) => errors[key]
    ? <p role="alert" className="mt-1 text-xs text-destructive">{errors[key]}</p>
    : null
  const group = (title: string, children: ReactNode) => (
    <section className="grid gap-3">
      <h3 className="text-sm font-semibold">{title}</h3>
      {children}
    </section>
  )

  return (
    <DetailSheet
      open={open}
      onClose={close}
      title={t(`${S}title`)}
      className="data-[side=right]:sm:w-[34rem] data-[side=right]:sm:max-w-[34rem]"
      footer={(
        <>
          <span className="mr-auto self-center text-sm text-muted-foreground">{count > 0 ? t(`${S}changed`, { count }) : ''}</span>
          <Button variant="outline" disabled={count === 0 || save.isPending} onClick={() => stored && setDraft({ ...stored, dest_rules_update_mode: storedMode })}>{t(`${S}discard`)}</Button>
          <AsyncButton disabled={count === 0 || invalid || blocked} onClick={submit}>{t(`${S}save`)}</AsyncButton>
        </>
      )}
    >
      <div ref={body} className="grid gap-6">
        {read.isPending && open && <Skeleton className="h-40 w-full" />}
        {read.isError && (isDestUnavailable(read.error)
          ? <Alert><IconInfo aria-hidden /><AlertDescription>{t(`${S}unwired`)}</AlertDescription></Alert>
          : <ErrorState compact title={t(`${S}read_failed`)} retry={() => void read.refetch()} />)}
        {draft && read.data && (
          <>
            {errors._ && <Alert variant="destructive"><IconError aria-hidden /><AlertDescription>{errors._}</AlertDescription></Alert>}
            {group(t(`${S}group_retention`), (
              <>
                <div data-focus="retention">
                  <PolicyField spec={FIELDS[2]} value={draft.dest_hit_retention_days} defaults={read.data.defaults}
                    effective={read.data.effective.dest_hit_retention_days}
                    onChange={v => setDraft({ ...draft, dest_hit_retention_days: v as number })} />
                  {err('dest_hit_retention_days')}
                </div>
                <div data-focus="usage">
                  <PolicyField spec={FIELDS[4]} value={draft.dest_usage_retention_days ?? 0} defaults={read.data.defaults}
                    effective={read.data.effective.dest_usage_retention_days ?? read.data.defaults.dest_usage_retention_days}
                    onChange={v => setDraft({ ...draft, dest_usage_retention_days: v as number })} />
                  {err('dest_usage_retention_days')}
                </div>
                <div data-focus="trial">
                  <PolicyField spec={FIELDS[3]} value={draft.dest_trial_retention_days} defaults={read.data.defaults}
                    effective={read.data.effective.dest_trial_retention_days}
                    onChange={v => setDraft({ ...draft, dest_trial_retention_days: v as number })} />
                  {err('dest_trial_retention_days')}
                </div>
              </>
            ))}
            {group(t(`${S}group_refresh`), (
              <div data-focus="refresh">
                <PolicyField spec={FIELDS[1]} value={draft.dest_list_refresh_hours} defaults={read.data.defaults}
                  effective={read.data.effective.dest_list_refresh_hours}
                  onChange={v => setDraft({ ...draft, dest_list_refresh_hours: v as number })} />
                {err('dest_list_refresh_hours')}
              </div>
            ))}
            {group(t(`${S}group_apply`), (
              <div data-focus="apply">
                <PolicyField spec={FIELDS[0]} value={draft.dest_policy_apply_min_seconds} defaults={read.data.defaults}
                  effective={read.data.effective.dest_policy_apply_min_seconds}
                  onChange={v => setDraft({ ...draft, dest_policy_apply_min_seconds: v as number })} />
                {err('dest_policy_apply_min_seconds')}
              </div>
            ))}
            <div data-focus="rules">
              {group(t(`${S}group_rules`), (
                <RuleDataPanel mode={draft.dest_rules_update_mode === 'auto' ? 'auto' : 'manual'}
                  onModeChange={m => setDraft({ ...draft, dest_rules_update_mode: m })} disabled={save.isPending} />
              ))}
            </div>
          </>
        )}
      </div>
    </DetailSheet>
  )
}
