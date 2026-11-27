import { memo } from 'react'
import { Link } from 'react-router'
import type { UISettings } from '@/api/settings'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { useAdminT } from '../useAdminT'
import LinkProbeSettings from '../LinkProbeSettings'
import { GeoIpGroup } from './GeoIpGroup'
import { SUB_KEYS, settingId, type SubKeys } from './model'
import { Group, Hint, NumField, Pair, SectionForm, SuggestInput, SwitchField } from './parts'
import { SubTabs, type SubPanel } from './SubTabs'
import { useUiSection } from './useUiSection'

// The option set of the timezone box: the browser's own IANA database, which covers every zone Go's time.LoadLocation
// can resolve with no upkeep. Older browsers without the call get a short list; the box still takes any name typed.
const TIMEZONES: string[] = (() => {
  try {
    const fn = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf
    if (typeof fn === 'function') return fn('timeZone')
  } catch { /* fall through */ }
  return [
    'UTC', 'America/Los_Angeles', 'America/Denver', 'America/Chicago', 'America/New_York', 'Asia/Shanghai',
    'Asia/Hong_Kong', 'Asia/Taipei', 'Asia/Tokyo', 'Asia/Seoul', 'Asia/Singapore', 'Europe/London', 'Europe/Paris',
    'Europe/Berlin', 'Europe/Moscow', 'Australia/Sydney',
  ]
})()

type Draft = Pick<UISettings, SubKeys<'general', 'retention'>>

function retentionProblems(v: Draft, t: ReturnType<typeof useAdminT>): Record<string, string> {
  const out: Record<string, string> = {}
  // Retention must cover both windows: a result that expires before the task it belongs to is useless.
  if (v.node_task_result_retention_days < v.node_task_offline_reconcile_days
    || v.node_task_result_retention_days < v.node_task_backup_restore_days) {
    out.node_task_result_retention_days = t('settings.general.node_task_retention_minimum')
  }
  return out
}

const noProblems = () => ({})

/** The panels of the General section, one per sub-tab (see tree.ts for what each holds and why). */
const PANELS = { runtime: RuntimePanel, retention: RetentionPanel, reminders: RemindersPanel, network: NetworkPanel, probe: ProbePanel } satisfies Record<string, SubPanel>

export const GeneralSection = memo(function GeneralSection({ active }: { active: boolean }) {
  return <SubTabs section="general" active={active} panels={PANELS} />
})

function RuntimePanel({ onDirtyChange }: { onDirtyChange: (dirty: boolean) => void }) {
  const t = useAdminT()
  const form = useUiSection(SUB_KEYS.general.runtime, onDirtyChange, noProblems)
  const { value: v, patch } = form
  const min = t('settings.unit.minutes')
  const secs = t('settings.unit.seconds')
  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void form.save()} onDiscard={form.discard}>
      <Group title={t('settings.general.section_runtime')}>
        <Field label={t('settings.general.timezone')} hint={t('settings.general.timezone_hint')}>
          <SuggestInput id={settingId('timezone')} value={v.timezone || ''} options={TIMEZONES}
            placeholder={t('settings.general.timezone_placeholder')} onChange={tz => patch({ timezone: tz })} />
        </Field>
        <Pair>
          <NumField name="cron_traffic_pull_minutes" label={t('settings.general.cron_traffic_pull_minutes')} unit={min}
            hint={t('settings.general.cron_effect_hint')} value={v.cron_traffic_pull_minutes} onChange={n => patch({ cron_traffic_pull_minutes: n })} />
          <NumField name="cron_reconcile_minutes" label={t('settings.general.cron_reconcile_minutes')} unit={min}
            hint={t('settings.general.cron_effect_hint')} value={v.cron_reconcile_minutes} onChange={n => patch({ cron_reconcile_minutes: n })} />
        </Pair>
        <Pair>
          <NumField name="node_poll_seconds" label={t('settings.general.node_poll_seconds')} unit={secs} max={3600}
            hint={t('settings.general.node_poll_seconds_hint')} value={v.node_poll_seconds} onChange={n => patch({ node_poll_seconds: n })} />
          <NumField name="full_report_seconds" label={t('settings.general.full_report_seconds')} unit={secs} max={86400}
            hint={t('settings.general.full_report_seconds_hint')} value={v.full_report_seconds} onChange={n => patch({ full_report_seconds: n })} />
        </Pair>
        <NumField name="max_panel_concurrency" label={t('settings.general.max_panel_concurrency')}
          hint={t('settings.general.max_panel_concurrency_hint')} value={v.max_panel_concurrency} onChange={n => patch({ max_panel_concurrency: n })} />
      </Group>
    </SectionForm>
  )
}

function RetentionPanel({ onDirtyChange }: { onDirtyChange: (dirty: boolean) => void }) {
  const t = useAdminT()
  const form = useUiSection(SUB_KEYS.general.retention, onDirtyChange, retentionProblems)
  const { value: v, patch } = form
  const days = t('settings.unit.days')
  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void form.save()} onDiscard={form.discard}>
      <Group title={t('settings.general.section_retention')}>
        <Pair>
          <NumField name="audit_retention_days" label={t('settings.general.audit_retention_days')} unit={days}
            value={v.audit_retention_days} onChange={n => patch({ audit_retention_days: n })} />
          <NumField name="sync_task_retention_days" label={t('settings.general.sync_task_retention_days')} unit={days}
            value={v.sync_task_retention_days} onChange={n => patch({ sync_task_retention_days: n })} />
        </Pair>
        <NumField name="auth_event_retention_days" label={t('settings.general.auth_event_retention_days')} unit={days}
          hint={t('settings.general.auth_event_retention_days_hint')} value={v.auth_event_retention_days} onChange={n => patch({ auth_event_retention_days: n })} />
        <NumField name="traffic_history_days" label={t('settings.general.traffic_history_days')} unit={days}
          hint={t('settings.general.traffic_history_days_hint')} value={v.traffic_history_days} onChange={n => patch({ traffic_history_days: n })} />
      </Group>

      <Group title={t('settings.general.section_node_task_lifecycle')} description={t('settings.general.node_task_lifecycle_hint')}>
        <Pair>
          <NumField name="node_task_offline_reconcile_days" label={t('settings.general.node_task_offline_reconcile_days')} unit={days} min={1} max={3650}
            hint={t('settings.general.node_task_offline_reconcile_days_hint')} value={v.node_task_offline_reconcile_days}
            onChange={n => patch({ node_task_offline_reconcile_days: n })} />
          <NumField name="node_task_backup_restore_days" label={t('settings.general.node_task_backup_restore_days')} unit={days} min={1} max={3650}
            hint={t('settings.general.node_task_backup_restore_days_hint')} value={v.node_task_backup_restore_days}
            onChange={n => patch({ node_task_backup_restore_days: n })} />
        </Pair>
        <NumField name="node_task_result_retention_days" label={t('settings.general.node_task_result_retention_days')} unit={days} min={1} max={3650}
          hint={t('settings.general.node_task_result_retention_days_hint')} value={v.node_task_result_retention_days}
          error={form.problems.node_task_result_retention_days} onChange={n => patch({ node_task_result_retention_days: n })} />
        <Hint>{t('settings.general.node_task_policy_safety_hint')}</Hint>
      </Group>
    </SectionForm>
  )
}

function RemindersPanel({ onDirtyChange }: { onDirtyChange: (dirty: boolean) => void }) {
  const t = useAdminT()
  const form = useUiSection(SUB_KEYS.general.reminders, onDirtyChange, noProblems)
  const { value: v, patch } = form
  const days = t('settings.unit.days')
  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void form.save()} onDiscard={form.discard}>
      <Group title={t('settings.general.section_notify')}>
        <Pair>
          <NumField name="expire_before_days" label={t('settings.general.expire_before_days')} unit={days}
            hint={t('settings.general.expire_before_days_hint')} value={v.expire_before_days} onChange={n => patch({ expire_before_days: n })} />
          <NumField name="traffic_remain_percent" label={t('settings.general.traffic_remain_percent')} unit="%"
            hint={t('settings.general.traffic_remain_percent_hint')} value={v.traffic_remain_percent} onChange={n => patch({ traffic_remain_percent: n })} />
        </Pair>
      </Group>

      <Group title={t('settings.general.emergency_section')}>
        <SwitchField name="emergency_access_enabled" label={t('settings.general.emergency_access_enabled')}
          checked={v.emergency_access_enabled} onChange={c => patch({ emergency_access_enabled: c })} />
        <Pair>
          <NumField name="emergency_access_hours" label={t('settings.general.emergency_access_hours')} unit={t('settings.unit.hours')} min={v.emergency_access_enabled ? 1 : 0}
            value={v.emergency_access_hours} onChange={n => patch({ emergency_access_hours: n })} />
          <NumField name="emergency_access_max_count" label={t('settings.general.emergency_access_max_count')} min={v.emergency_access_enabled ? 1 : 0}
            value={v.emergency_access_max_count} onChange={n => patch({ emergency_access_max_count: n })} />
        </Pair>
        <NumField name="emergency_access_quota_gb" label={t('settings.general.emergency_access_quota_gb')} unit="GB" integer={false}
          hint={t('settings.general.emergency_access_quota_gb_hint')} value={v.emergency_access_quota_gb} onChange={n => patch({ emergency_access_quota_gb: n })} />
      </Group>
    </SectionForm>
  )
}

function NetworkPanel({ onDirtyChange }: { onDirtyChange: (dirty: boolean) => void }) {
  const t = useAdminT()
  const form = useUiSection(SUB_KEYS.general.network, onDirtyChange, noProblems)
  const { value: v, patch } = form
  // The Cloudflare switch reads as on when an older server does not say.
  const cloudflare = v.cloudflare_ips_auto_fetch ?? true
  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void form.save()} onDiscard={form.discard}>
      <Group title={t('settings.general.section_source_guard')}>
        <SwitchField name="cloudflare_ips_auto_fetch" label={t('settings.general.cloudflare_auto_fetch')} hint={t('settings.general.cloudflare_auto_fetch_hint')}
          checked={cloudflare} onChange={c => patch({ cloudflare_ips_auto_fetch: c })} />
      </Group>

      <GeoIpGroup form={form} />

      {/* The detectors that judge people by these regions (concurrent locations, the risk signals) are set on the risk
          center's policy tab, which owns them on the server too: this page's save keeps whatever policy it loaded. They
          read this database, so an admin who comes looking for them here finds the way. */}
      <div className="flex flex-wrap items-center gap-3 rounded-xl border border-dashed px-4 py-3">
        <p className="min-w-60 flex-1 text-sm text-muted-foreground">{t('settings.geo.policy_pointer')}</p>
        <Button variant="outline" render={<Link to="/admin/risk?tab=policy" />}>{t('settings.geo.open_policy')}</Button>
      </div>
    </SectionForm>
  )
}

/** The link-probe card keeps its own record, its own Save and its own leave guard: it is shown as it is, and reports nothing here. */
function ProbePanel() {
  return <LinkProbeSettings />
}
