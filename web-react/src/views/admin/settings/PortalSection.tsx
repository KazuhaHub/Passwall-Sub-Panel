import { memo } from 'react'
import type { GlobalAnnouncement, UISettings } from '@/api/settings'
import { Field } from '@/components/form/Field'
import { Textarea } from '@/components/ui/textarea'
import { validateUrl } from '@/utils/validators'
import { useAdminT } from '../useAdminT'
import { SUB_KEYS, settingId } from './model'
import { Group, SectionForm, SelectRow, SwitchField, TextRow } from './parts'
import { QuickLinksEditor, quickLinkProblems } from './QuickLinksEditor'
import { SubTabs, type SubPanel } from './SubTabs'
import { useUiSection } from './useUiSection'

type Panel = { onDirtyChange: (dirty: boolean) => void }
type LinksDraft = Pick<UISettings, 'quick_links'>
type AnnouncementDraft = Pick<UISettings, 'global_announcement'>

function linkProblems(v: LinksDraft, t: ReturnType<typeof useAdminT>): Record<string, string> {
  return quickLinkProblems(v.quick_links, t, validateUrl)
}

function announcementProblems(v: AnnouncementDraft, t: ReturnType<typeof useAdminT>): Record<string, string> {
  const out: Record<string, string> = {}
  // An announcement switched on with no title or text would show an empty notice on the portal.
  const a = v.global_announcement
  if (a?.enabled) {
    if (!a.title?.trim()) out.announcement_title = t('validation.required')
    if (!a.content?.trim()) out.announcement_content = t('validation.required')
  }
  return out
}

const PANELS = { links: LinksPanel, announcement: AnnouncementPanel } satisfies Record<string, SubPanel>

/** The user portal: the shortcuts users see, and the announcement over the whole site. */
export const PortalSection = memo(function PortalSection({ active }: { active: boolean }) {
  return <SubTabs section="portal" active={active} panels={PANELS} />
})

function LinksPanel({ onDirtyChange }: Panel) {
  const form = useUiSection(SUB_KEYS.portal.links, onDirtyChange, linkProblems)
  const { value: v, patch } = form
  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void form.save()} onDiscard={form.discard}>
      <QuickLinksEditor links={v.quick_links} onChange={l => patch({ quick_links: l })} problems={form.problems} show={form.attempted} />
    </SectionForm>
  )
}

function AnnouncementPanel({ onDirtyChange }: Panel) {
  const t = useAdminT()
  const form = useUiSection(SUB_KEYS.portal.announcement, onDirtyChange, announcementProblems)
  const { value: v, patch, error, blur } = form
  const a = v.global_announcement
  const set = (p: Partial<GlobalAnnouncement>) => patch({ global_announcement: { ...a, ...p } as GlobalAnnouncement })
  const on = a?.enabled ?? false

  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void form.save()} onDiscard={form.discard}>
      <Group title={t('settings.portal.section_announcement')}>
        <SwitchField name="announcement_enabled" label={t('settings.portal.announcement.enabled')} checked={on} onChange={c => set({ enabled: c })} />
        <SwitchField name="announcement_popup" label={t('settings.portal.announcement.popup')} hint={t('settings.portal.announcement.popup_hint')}
          checked={a?.popup ?? false} disabled={!on} onChange={c => set({ popup: c })} />
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <TextRow name="announcement_title" label={t('settings.portal.announcement.title')} required={on} value={a?.title ?? ''}
            onChange={s => set({ title: s })} onBlur={() => blur('announcement_title')} error={error('announcement_title')} />
          <SelectRow<GlobalAnnouncement['level']> name="announcement_level" label={t('settings.portal.announcement.level')} value={a?.level ?? 'info'}
            onChange={l => set({ level: l })}
            options={[
              { value: 'info', label: t('settings.portal.announcement.level_info') },
              { value: 'warning', label: t('settings.portal.announcement.level_warning') },
              { value: 'danger', label: t('settings.portal.announcement.level_danger') },
            ]} />
        </div>
        <Field label={t('settings.portal.announcement.content')} required={on} error={error('announcement_content')}>
          <Textarea id={settingId('announcement_content')} rows={4} value={a?.content ?? ''} onChange={e => set({ content: e.target.value })} onBlur={() => blur('announcement_content')} />
        </Field>
      </Group>
    </SectionForm>
  )
}
