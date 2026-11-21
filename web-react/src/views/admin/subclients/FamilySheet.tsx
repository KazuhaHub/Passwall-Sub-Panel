import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { SubClientApp, SubClientFamily, SubRenderFormat } from '@/api/settings'
import { IconAdd, IconDelete } from '@/lib/icons'
import { IconButton } from '@/components/common/IconButton'
import { DetailSheet } from '@/components/data/DetailSheet'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { PLATFORM_OPTIONS } from './presets'
import { addApp, parseKeywords, patchApp, patchFamily, removeApp, togglePlatform, toggleRecommended } from './registryOps'

const FORMATS: SubRenderFormat[] = ['mihomo', 'sing-box', 'uri-list']

/**
 * The editor of one detection family and its import apps. It edits the PAGE's draft directly (there is no
 * second draft with an Apply button): the page's save bar and leave guard already cover every change, so a
 * second layer of "unsaved" would only add a step. Closing the sheet loses nothing.
 */
export function FamilySheet({ families, index, onChange, onClose }: {
  families: SubClientFamily[]
  /** The family being edited; null: closed. */
  index: number | null
  onChange: (next: SubClientFamily[]) => void
  onClose: () => void
}) {
  const { t } = useTranslation('admin')
  const family = index === null ? undefined : families[index]
  return (
    <DetailSheet
      open={family !== undefined}
      onClose={onClose}
      title={family ? (family.name.trim() || t('settings.subscription.unnamed_family')) : ''}
      description={t('settings.subscription.registry_hint')}
      className="data-[side=right]:sm:w-[44rem] data-[side=right]:sm:max-w-[44rem]"
      footer={<Button variant="outline" onClick={onClose}>{t('settings.subscription.sheet_done')}</Button>}
    >
      {family && index !== null && <FamilyForm key={index} family={family} fi={index} families={families} onChange={onChange} />}
    </DetailSheet>
  )
}

function FamilyForm({ family, fi, families, onChange }: { family: SubClientFamily; fi: number; families: SubClientFamily[]; onChange: (next: SubClientFamily[]) => void }) {
  const { t } = useTranslation('admin')
  const s = (k: string) => t(`settings.subscription.${k}`)
  // The keyword box keeps its own text and commits the parsed array on every change. Re-joining the saved array
  // into the box on each keystroke would eat a typed comma or space before the next word arrives.
  const [kw, setKw] = useState(() => family.keywords.join(', '))
  const [touched, setTouched] = useState(false)
  const nameError = touched && !family.name.trim() ? s('name_required') : undefined
  const setApp = (ai: number, patch: Partial<SubClientApp>) => onChange(patchApp(families, fi, ai, patch))

  return (
    <div className="flex flex-col gap-4">
      <Field label={s('rule_field.name')} error={nameError} errorBlocksSave={false}>
        <Input value={family.name} onChange={e => onChange(patchFamily(families, fi, { name: e.target.value }))} onBlur={() => setTouched(true)} />
      </Field>
      <Field label={s('rule_field.keywords')}>
        <Input value={kw} onChange={e => { setKw(e.target.value); onChange(patchFamily(families, fi, { keywords: parseKeywords(e.target.value) })) }} />
      </Field>
      <Field label={s('rule_field.render_format')}>
        <NativeSelect value={family.render_format} onChange={e => onChange(patchFamily(families, fi, { render_format: e.target.value as SubRenderFormat }))}>
          {FORMATS.map(f => <option key={f} value={f}>{f === 'uri-list' ? s('format_uri_list') : f}</option>)}
        </NativeSelect>
      </Field>
      <Field label={s('rule_field.enabled')} orientation="horizontal">
        <Switch checked={family.enabled} onCheckedChange={enabled => onChange(patchFamily(families, fi, { enabled }))} />
      </Field>

      <div className="flex items-center justify-between gap-2 border-t pt-4">
        <h3 className="text-sm font-medium">{s('section_imports')}</h3>
        <Button variant="outline" size="sm" onClick={() => onChange(addApp(families, fi))}><IconAdd />{s('add_app')}</Button>
      </div>
      {family.apps.length === 0 && <p className="text-sm text-muted-foreground">{s('no_apps')}</p>}
      {family.apps.map((a, ai) => (
        // Index keys: an app has no id, and its fields are controlled, so nothing is lost on removal.
        <div key={ai} className="flex flex-col gap-3 rounded-lg border bg-muted/30 p-3">
          <div className="flex flex-wrap items-start gap-3">
            <Field label={s('client_field.name')} className="min-w-40 flex-1">
              <Input value={a.name} onChange={e => setApp(ai, { name: e.target.value })} />
            </Field>
            <Field label={s('client_field.sort')} className="w-24">
              <Input type="number" value={a.sort} onChange={e => setApp(ai, { sort: Number(e.target.value) })} />
            </Field>
            <Field label={s('client_field.enabled')} className="pt-0.5">
              <Switch checked={a.enabled} onCheckedChange={enabled => setApp(ai, { enabled })} />
            </Field>
            <IconButton label={t('settings.subscription.remove_app', { name: a.name || ai + 1 })} className="mt-6 ml-auto" onClick={() => onChange(removeApp(families, fi, ai))}>
              <IconDelete />
            </IconButton>
          </div>
          <Field label={s('client_field.platforms')}>
            {p => (
              <div role="group" aria-labelledby={p.labelId} className="flex flex-wrap gap-1.5">
                {PLATFORM_OPTIONS.map(pl => (
                  <Button key={pl} size="sm" variant={a.platforms.includes(pl) ? 'default' : 'outline'} aria-pressed={a.platforms.includes(pl)}
                    onClick={() => setApp(ai, togglePlatform(a, pl))}>{t(`settings.subscription.platform.${pl}`)}</Button>
                ))}
              </div>
            )}
          </Field>
          <Field label={s('client_field.recommended_for')}>
            {p => (
              <div role="group" aria-labelledby={p.labelId} className="flex flex-wrap gap-1.5">
                {PLATFORM_OPTIONS.filter(pl => a.platforms.includes(pl)).map(pl => (
                  <Button key={pl} size="sm" variant={(a.recommended_for ?? []).includes(pl) ? 'default' : 'outline'} aria-pressed={(a.recommended_for ?? []).includes(pl)}
                    onClick={() => setApp(ai, { recommended_for: toggleRecommended(a, pl) })}>{t(`settings.subscription.platform.${pl}`)}</Button>
                ))}
                {a.platforms.length === 0 && <span className="text-sm text-muted-foreground">{s('client_field.recommended_for_empty')}</span>}
              </div>
            )}
          </Field>
          <Field label={s('client_field.import_url_template')}>
            <Input value={a.import_url_template} onChange={e => setApp(ai, { import_url_template: e.target.value })} />
          </Field>
          <Field label={s('client_field.install_url')}>
            <Input value={a.install_url} onChange={e => setApp(ai, { install_url: e.target.value })} />
          </Field>
        </div>
      ))}
    </div>
  )
}
