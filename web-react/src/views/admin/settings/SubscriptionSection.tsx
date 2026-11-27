import { memo } from 'react'
import { Field } from '@/components/form/Field'
import { Input } from '@/components/ui/input'
import { confirm } from '@/components/ConfirmHost'
import { useAdminT } from '../useAdminT'
import { addressHazard, normalizeSubPath } from './dangerConfirms'
import { SUB_KEYS, settingId } from './model'
import { Group, Nested, NumField, SectionForm, SwitchField, TextRow } from './parts'
import { SubTabs, type SubPanel } from './SubTabs'
import { useUiSection } from './useUiSection'

type Panel = { onDirtyChange: (dirty: boolean) => void }
const noProblems = () => ({})
const slash = (p: string) => '/' + (p || '').replace(/^\/+/, '')

const PANELS = { link: LinkPanel, content: ContentPanel, protection: ProtectionPanel } satisfies Record<string, SubPanel>

/**
 * Subscription: the link every client fetches (global), what the file says and is called (a group can override), and
 * what happens to a client that should not be fetching (a group can override). One sub-tab each, so what a group's
 * audience can change is never on the same screen as what it cannot.
 */
export const SubscriptionSection = memo(function SubscriptionSection({ active }: { active: boolean }) {
  return <SubTabs section="subscription" active={active} panels={PANELS} />
})

function LinkPanel({ onDirtyChange }: Panel) {
  const t = useAdminT()
  const form = useUiSection(SUB_KEYS.subscription.link, onDirtyChange, noProblems)
  const { value: v, patch, saved } = form

  async function save() {
    // Every subscription link already handed out carries the old path.
    if (normalizeSubPath(v.sub_path) !== normalizeSubPath(saved.sub_path) && !(await confirm(addressHazard('sub_path', slash(saved.sub_path), slash(v.sub_path), t)))) return
    await form.save()
  }

  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void save()} onDiscard={form.discard}>
      <Group title={t('settings.subscription.section_basic')}>
        <Field label={t('settings.subscription.sub_path')} hint={t('settings.subscription.sub_path_hint')}>
          {/* Shown with its leading slash, which cannot be deleted; extra slashes typed or pasted are folded into it. */}
          <Input id={settingId('sub_path')} value={slash(v.sub_path)} onChange={e => patch({ sub_path: e.target.value.replace(/^\/+/, '') })} />
        </Field>
        <TextRow name="sub_import_tutorial_url" label={t('settings.subscription.sub_import_tutorial_url')}
          value={v.sub_import_tutorial_url} onChange={s => patch({ sub_import_tutorial_url: s })} />
        <div className="max-w-xs">
          <NumField name="sub_log_retention_days" label={t('settings.subscription.sub_log_retention_days')} unit={t('settings.unit.days')}
            value={v.sub_log_retention_days} onChange={n => patch({ sub_log_retention_days: n })} />
        </div>
      </Group>
    </SectionForm>
  )
}

function ContentPanel({ onDirtyChange }: Panel) {
  const t = useAdminT()
  const form = useUiSection(SUB_KEYS.subscription.content, onDirtyChange, noProblems)
  const { value: v, patch } = form
  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void form.save()} onDiscard={form.discard}>
      <Group title={t('settings.subscription.section_content')}>
        <div className="max-w-xs">
          <NumField name="sub_update_interval_hours" label={t('settings.subscription.sub_update_interval_hours')} unit={t('settings.unit.hours')}
            value={v.sub_update_interval_hours} onChange={n => patch({ sub_update_interval_hours: n })} />
        </div>
        <TextRow name="sub_profile_name_template" label={t('settings.subscription.sub_profile_name_template')} placeholder="{{ site_title }} - {{ user }}"
          hint={t('settings.subscription.sub_profile_name_template_hint')} value={v.sub_profile_name_template} onChange={s => patch({ sub_profile_name_template: s })} />
        <SwitchField name="sub_region_flag_prefix" label={t('settings.subscription.sub_region_flag_prefix')} hint={t('settings.subscription.sub_region_flag_prefix_hint')}
          checked={v.sub_region_flag_prefix} onChange={c => patch({ sub_region_flag_prefix: c })} />
      </Group>
    </SectionForm>
  )
}

function ProtectionPanel({ onDirtyChange }: Panel) {
  const t = useAdminT()
  const form = useUiSection(SUB_KEYS.subscription.protection, onDirtyChange, noProblems)
  const { value: v, patch } = form
  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void form.save()} onDiscard={form.discard}>
      <Group title={t('settings.subscription.section_protection')}>
        <SwitchField name="sub_block_auto_disable" label={t('settings.subscription.sub_block_auto_disable')}
          checked={v.sub_block_auto_disable} onChange={c => patch({ sub_block_auto_disable: c })} />
        <div className="max-w-xs">
          <NumField name="sub_block_auto_disable_count" label={t('settings.subscription.sub_block_auto_disable_count')}
            value={v.sub_block_auto_disable_count} onChange={n => patch({ sub_block_auto_disable_count: n })} />
        </div>
        <SwitchField name="sub_block_notify_user" label={t('settings.subscription.sub_block_notify_user')}
          checked={v.sub_block_notify_user} onChange={c => patch({ sub_block_notify_user: c })} />
        {v.sub_block_notify_user && (
          <Nested>
            <div className="max-w-xs">
              <NumField name="sub_block_notify_max_per_day" label={t('settings.subscription.sub_block_notify_max_per_day')}
                value={v.sub_block_notify_max_per_day} onChange={n => patch({ sub_block_notify_max_per_day: n })} />
            </div>
          </Nested>
        )}
      </Group>
    </SectionForm>
  )
}
