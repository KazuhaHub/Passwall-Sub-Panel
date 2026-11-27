import { memo } from 'react'
import type { UISettings } from '@/api/settings'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Field } from '@/components/form/Field'
import { confirm } from '@/components/ConfirmHost'
import { validateUrl } from '@/utils/validators'
import { useAdminT } from '../useAdminT'
import { addressHazard, normalizePanelPath } from './dangerConfirms'
import { placeId, placeName } from './tree'
import { useOtherDirtyPlaces } from './dirtyContext'
import { SUB_KEYS, settingId, type SubKeys } from './model'
import { Group, Hint, SectionForm, TextRow } from './parts'
import { SubTabs, type SubPanel } from './SubTabs'
import { useUiSection } from './useUiSection'

type Draft = Pick<UISettings, SubKeys<'brand', 'addresses'>>
type Panel = { onDirtyChange: (dirty: boolean) => void }
const noProblems = () => ({})

/** The subscription URL embeds this address: saving one that is not a URL would break every client, so it is required and checked. */
function addressProblems(v: Draft, t: ReturnType<typeof useAdminT>): Record<string, string> {
  const err = validateUrl(v.sub_base_url, { required: true })
  return err ? { sub_base_url: t(err) } : {}
}

const PANELS = { identity: IdentityPanel, addresses: AddressesPanel } satisfies Record<string, SubPanel>

/** Brand: what the site is called and looks like, and where it lives. */
export const BrandSection = memo(function BrandSection({ active }: { active: boolean }) {
  return <SubTabs section="brand" active={active} panels={PANELS} />
})

function IdentityPanel({ onDirtyChange }: Panel) {
  const t = useAdminT()
  const form = useUiSection(SUB_KEYS.brand.identity, onDirtyChange, noProblems)
  const { value: v, patch } = form
  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void form.save()} onDiscard={form.discard}>
      <Group title={t('settings.brand.section_text')}>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <TextRow name="site_title" label={t('settings.brand.site_title')} value={v.site_title} onChange={s => patch({ site_title: s })} />
          <TextRow name="app_title" label={t('settings.brand.app_title')} value={v.app_title} onChange={s => patch({ app_title: s })} />
        </div>
        <TextRow name="footer_text" label={t('settings.brand.footer_text')} value={v.footer_text} onChange={s => patch({ footer_text: s })} />
      </Group>

      <Group title={t('settings.brand.section_assets')}>
        <TextRow name="icon_url" label={t('settings.brand.icon_url')} value={v.icon_url} onChange={s => patch({ icon_url: s })} />
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <TextRow name="logo_url" label={t('settings.brand.logo_url')} value={v.logo_url} onChange={s => patch({ logo_url: s })} />
          <TextRow name="logo_url_dark" label={t('settings.brand.logo_url_dark')} value={v.logo_url_dark} onChange={s => patch({ logo_url_dark: s })} />
        </div>
        <Hint>{t('settings.brand.asset_hint')}</Hint>
      </Group>

      <Group title={t('settings.brand.section_theme')}>
        <Field label={t('settings.brand.theme_color')} hint={t('settings.brand.theme_color_hint')}>
          {/* The colour well and the text box edit the same value, so a HEX typed or picked ends up the same string. */}
          <div className="flex items-center gap-2">
            <Input type="color" aria-label={t('settings.brand.theme_color_picker')} value={v.theme_color || '#0061A4'}
              className="size-(--control-h) shrink-0 cursor-pointer p-1" onChange={e => patch({ theme_color: e.target.value.toUpperCase() })} />
            <Input id={settingId('theme_color')} value={v.theme_color} placeholder="#0061A4" onChange={e => patch({ theme_color: e.target.value })} />
            {v.theme_color && <Button type="button" variant="outline" onClick={() => patch({ theme_color: '' })}>{t('settings.brand.theme_color_clear')}</Button>}
          </div>
        </Field>
      </Group>

    </SectionForm>
  )
}

function AddressesPanel({ onDirtyChange }: Panel) {
  const t = useAdminT()
  const form = useUiSection(SUB_KEYS.brand.addresses, onDirtyChange, addressProblems)
  const { value: v, patch, saved, error, blur } = form
  // The panel path reloads the page: whatever is unsaved anywhere else on it is dropped, so the confirmation names it.
  const lost = useOtherDirtyPlaces(placeId('brand', 'addresses'))

  async function save() {
    // The panel path moves this page itself, so say where it goes before it does.
    const from = normalizePanelPath(saved.panel_path); const to = normalizePanelPath(v.panel_path)
    if (from !== to && !(await confirm(addressHazard('panel_path', from, to, t, lost.map(p => placeName(p, t)))))) return
    await form.save()
  }

  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void save()} onDiscard={form.discard}>
      <Group title={t('settings.brand.section_addresses')}>
        <TextRow name="sub_base_url" label={t('settings.brand.sub_base_url')} hint={t('settings.brand.sub_base_url_hint')} required
          value={v.sub_base_url} onChange={s => patch({ sub_base_url: s })} onBlur={() => blur('sub_base_url')} error={error('sub_base_url')} />
        <TextRow name="panel_path" label={t('settings.brand.panel_path')} hint={t('settings.brand.panel_path_hint')} placeholder="/panel"
          value={v.panel_path} onChange={s => patch({ panel_path: s })} />
      </Group>

      <Group title={t('settings.brand.section_email')}>
        <TextRow name="email_domain" label={t('settings.brand.email_domain')} value={v.email_domain} onChange={s => patch({ email_domain: s })} />
      </Group>
    </SectionForm>
  )
}
