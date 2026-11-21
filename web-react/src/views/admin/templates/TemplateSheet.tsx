import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import type { RuleSet } from '@/api/rules'
import { saveTemplate, type Template } from '@/api/templates'
import { DetailSheet } from '@/components/data/DetailSheet'
import { pushToast } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { Combobox, ComboboxChip, ComboboxChips, ComboboxChipsInput, ComboboxContent, ComboboxEmpty, ComboboxItem, ComboboxList, ComboboxValue, useComboboxAnchor } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { cn } from '@/lib/utils'
import { useSheetGuard } from '../subscription/useSheetGuard'
import { formHint } from './analysis'

export const EMPTY: Template = { slug: '', name: '', client_type: 'mihomo', is_default: false, rule_sets: [], content: '' }

/** What the sheet is for: a new template, a copy of one (saved as new), or an existing one. */
export type TemplateTarget = { kind: 'new' } | { kind: 'copy'; draft: Template } | { kind: 'edit'; template: Template }

/** A copy of a template for the create flow: is_default is forced off so it does not fight the original for the per-client-type default. */
export function duplicateOf(tpl: Template): Template {
  return { ...tpl, slug: tpl.slug + '-copy', name: tpl.name + ' (Copy)', is_default: false, rule_sets: tpl.rule_sets ? [...tpl.rule_sets] : [] }
}

const same = (a: Template, b: Template) => JSON.stringify(a) === JSON.stringify(b)
const copyOf = (tpl: Template): Template => ({ ...tpl, rule_sets: tpl.rule_sets ? [...tpl.rule_sets] : [] })

/**
 * The template editor: a wide sheet with a SaveBar (a template is a long document). The save sends the draft as
 * it is, exactly as the old dialog did; the hint under the rule-set picker only reads it.
 */
export function TemplateSheet({ target, existingSlugs, ruleSets, onClose, onSaved }: {
  target: TemplateTarget | null
  existingSlugs: readonly string[]
  ruleSets: readonly RuleSet[]
  onClose: () => void
  onSaved: (saved: Template, wasEdit: boolean) => void
}) {
  if (!target) return null
  const key = target.kind === 'edit' ? `edit:${target.template.slug}` : target.kind === 'copy' ? `copy:${target.draft.slug}` : 'new'
  return <SheetBody key={key} target={target} existingSlugs={existingSlugs} ruleSets={ruleSets} onClose={onClose} onSaved={onSaved} />
}

function SheetBody({ target, existingSlugs, ruleSets, onClose, onSaved }: {
  target: TemplateTarget
  existingSlugs: readonly string[]
  ruleSets: readonly RuleSet[]
  onClose: () => void
  onSaved: (saved: Template, wasEdit: boolean) => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const editing = target.kind === 'edit'
  const seed = target.kind === 'edit' ? target.template : target.kind === 'copy' ? target.draft : EMPTY
  // A copy is unsaved from the start, so it is compared with a blank one.
  const baseline = useMemo(() => copyOf(editing ? target.template : EMPTY),
    // Seeded once per opening.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [])
  const [form, setForm] = useState<Template>(() => copyOf(seed))
  const [attempted, setAttempted] = useState(false)
  const [pending, setPending] = useState(false)
  const anchor = useComboboxAnchor()
  const dirty = !same(form, baseline)
  const { close, finish } = useSheetGuard({ dirty, pending, onClose })

  const ruleSetMap = useMemo(() => new Map(ruleSets.map(r => [r.slug, r])), [ruleSets])
  // The picker works on objects; a bound slug that no longer exists still shows (by its slug) and can be removed.
  const options = useMemo<RuleSet[]>(() => {
    const known = new Set(ruleSets.map(r => r.slug))
    const dangling = form.rule_sets.filter(s => !known.has(s)).map(s => ({ slug: s, name: s, enabled: false } as RuleSet))
    return [...ruleSets, ...dangling]
  }, [ruleSets, form.rule_sets])
  const chosen = form.rule_sets.map(s => options.find(o => o.slug === s)).filter((x): x is RuleSet => Boolean(x))
  const hint = formHint(form, ruleSetMap)

  const slugTaken = !editing && existingSlugs.includes(form.slug)
  const slugError = attempted && !form.slug ? t('admin:templates.validate.slug_name_required')
    : attempted && slugTaken ? t('admin:templates.validate.slug_exists', { slug: form.slug }) : undefined
  const nameError = attempted && !form.name ? t('admin:templates.validate.slug_name_required') : undefined

  async function save() {
    if (!form.slug || !form.name) { setAttempted(true); pushToast({ message: t('admin:templates.validate.slug_name_required'), severity: 'warning' }); return }
    // Save is a slug-keyed upsert: a new template with an existing slug would silently overwrite that one.
    if (slugTaken) { setAttempted(true); pushToast({ message: t('admin:templates.validate.slug_exists', { slug: form.slug }), severity: 'warning' }); return }
    setPending(true)
    try {
      await saveTemplate(form)
      pushToast({ message: t('admin:templates.toast.saved'), severity: 'success' })
      finish(() => onSaved(form, editing))
    } catch { /* The request layer already surfaced the failure. */ } finally { setPending(false) }
  }

  return (
    <DetailSheet
      open
      onClose={close}
      title={editing ? t('admin:templates.edit_title') : t('admin:templates.create')}
      description={editing ? form.slug : undefined}
      className="data-[side=right]:sm:w-[min(64rem,100vw)] data-[side=right]:sm:max-w-none"
    >
      <FormValidityProvider>
        <form onSubmit={e => { e.preventDefault(); void save() }} noValidate className="flex flex-col gap-4">
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <Field label={t('admin:templates.field.slug')} required error={slugError}>
              <Input value={form.slug} disabled={editing} onChange={e => setForm({ ...form, slug: e.target.value })} />
            </Field>
            <Field label={t('admin:templates.field.name')} required error={nameError}>
              <Input value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} />
            </Field>
            <Field label={t('admin:templates.field.client_type')}>
              <NativeSelect value={form.client_type} onChange={e => setForm({ ...form, client_type: e.target.value })}>
                <option value="mihomo">mihomo</option>
                <option value="sing-box">Sing-box</option>
              </NativeSelect>
            </Field>
            <Field label={t('admin:templates.field.is_default')} orientation="horizontal">
              <Switch checked={form.is_default} onCheckedChange={c => setForm({ ...form, is_default: c })} />
            </Field>
          </div>
          <Field label={t('admin:templates.field.rule_sets')} hint={t('admin:templates.hint.rule_sets')}>
            {p => (
              <Combobox multiple items={options} value={chosen}
                onValueChange={v => setForm(f => ({ ...f, rule_sets: (v as RuleSet[]).map(r => r.slug) }))}
                itemToStringLabel={(r: RuleSet) => `${r.name} (${r.slug})`} isItemEqualToValue={(a: RuleSet, b: RuleSet) => a.slug === b.slug}>
                <ComboboxChips ref={anchor}>
                  <ComboboxValue>
                    {(values: RuleSet[]) => (
                      <>
                        {values.map(r => <ComboboxChip key={r.slug}>{r.name || r.slug}</ComboboxChip>)}
                        <ComboboxChipsInput id={p.id} aria-describedby={p['aria-describedby']} placeholder={values.length === 0 ? t('admin:templates.placeholder.rule_sets') : undefined} />
                      </>
                    )}
                  </ComboboxValue>
                </ComboboxChips>
                <ComboboxContent anchor={anchor}>
                  <ComboboxEmpty>{t('admin:templates.no_rule_sets')}</ComboboxEmpty>
                  <ComboboxList>
                    {(r: RuleSet) => (
                      <ComboboxItem key={r.slug} value={r} disabled={!r.enabled && !form.rule_sets.includes(r.slug)}>
                        {`${r.name} (${r.slug})`}{!r.enabled && <span className="ml-1 text-xs text-muted-foreground">{t('admin:rules.status.disabled')}</span>}
                      </ComboboxItem>
                    )}
                  </ComboboxList>
                </ComboboxContent>
              </Combobox>
            )}
          </Field>
          {hint && (
            <p data-testid="template-hint" className={cn('text-xs', hint.tone === 'bad' ? 'text-destructive' : hint.tone === 'good' ? 'text-foreground' : 'text-muted-foreground')}>
              {t(hint.key, hint.params)}
            </p>
          )}
          <Field label={t('admin:templates.field.content')}>
            <Textarea rows={18} spellCheck={false} className="max-h-[60vh] min-h-[24rem] overflow-y-auto font-mono text-[13px]" placeholder={t('admin:templates.placeholder.content')}
              value={form.content} onChange={e => setForm({ ...form, content: e.target.value })} />
          </Field>
          <SaveBar dirty={dirty} pending={pending} onSave={() => void save()} onDiscard={() => { setForm(copyOf(seed)); setAttempted(false) }} />
        </form>
      </FormValidityProvider>
    </DetailSheet>
  )
}
