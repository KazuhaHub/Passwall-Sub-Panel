import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import type { RuleSet } from '@/api/rules'
import type { Template } from '@/api/templates'
import { Field } from '@/components/form/Field'
import { Combobox, ComboboxChip, ComboboxChips, ComboboxChipsInput, ComboboxContent, ComboboxEmpty, ComboboxItem, ComboboxList, ComboboxValue, useComboboxAnchor } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { cn } from '@/lib/utils'
import { formHint } from './analysis'

/**
 * The template's fields, laid out for a page: the document takes the main column and as much height as it needs (the
 * PAGE scrolls, never the textarea beside it, so there is one scrollbar), and the small fields plus what the analysis
 * says about the draft sit in a column beside it on a desktop. On a phone the small fields come first and the document
 * below them, because the name and the rule sets are what one reaches for before a long text.
 */
export function TemplateForm({ form, setForm, editing, readOnly, slugError, nameError, ruleSets }: {
  form: Template
  setForm: (update: (prev: Template) => Template) => void
  editing: boolean
  readOnly: boolean
  slugError?: string
  nameError?: string
  ruleSets: readonly RuleSet[]
}) {
  const { t } = useTranslation(['admin'])
  const anchor = useComboboxAnchor()
  const ruleSetMap = useMemo(() => new Map(ruleSets.map(r => [r.slug, r])), [ruleSets])
  // The picker works on objects; a bound slug that no longer exists still shows (by its slug) and can be removed.
  const options = useMemo<RuleSet[]>(() => {
    const known = new Set(ruleSets.map(r => r.slug))
    const dangling = form.rule_sets.filter(s => !known.has(s)).map(s => ({ slug: s, name: s, enabled: false } as RuleSet))
    return [...ruleSets, ...dangling]
  }, [ruleSets, form.rule_sets])
  const chosen = form.rule_sets.map(s => options.find(o => o.slug === s)).filter((x): x is RuleSet => Boolean(x))
  const hint = formHint(form, ruleSetMap)
  const patch = (p: Partial<Template>) => setForm(f => ({ ...f, ...p }))

  return (
    <div className="grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_21rem]">
      <aside className="flex flex-col gap-4 lg:order-2" data-slot="template-side">
        <Field label={t('admin:templates.field.slug')} required={!readOnly} error={slugError}>
          <Input value={form.slug} disabled={editing || readOnly} onChange={e => patch({ slug: e.target.value })} />
        </Field>
        <Field label={t('admin:templates.field.name')} required={!readOnly} error={nameError}>
          <Input value={form.name} disabled={readOnly} onChange={e => patch({ name: e.target.value })} />
        </Field>
        <Field label={t('admin:templates.field.client_type')}>
          <NativeSelect value={form.client_type} disabled={readOnly} onChange={e => patch({ client_type: e.target.value })}>
            <option value="mihomo">mihomo</option>
            <option value="sing-box">Sing-box</option>
          </NativeSelect>
        </Field>
        <Field label={t('admin:templates.field.is_default')} orientation="horizontal">
          <Switch checked={form.is_default} disabled={readOnly} onCheckedChange={c => patch({ is_default: c })} />
        </Field>
        <Field label={t('admin:templates.field.rule_sets')} hint={t('admin:templates.hint.rule_sets')}>
          {p => (
            <Combobox multiple items={options} value={chosen} disabled={readOnly}
              onValueChange={v => patch({ rule_sets: (v as RuleSet[]).map(r => r.slug) })}
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
        {/* The subscription render cache is not cleared by a template save (see the report's proof list). */}
        <p className="text-xs text-muted-foreground">{t('admin:templates.hint.propagation')}</p>
      </aside>
      <div className="min-w-0 lg:order-1">
        <Field label={t('admin:templates.field.content')}>
          <Textarea spellCheck={false} readOnly={readOnly} className="min-h-[max(24rem,60dvh)] font-mono text-[13px]" placeholder={t('admin:templates.placeholder.content')}
            value={form.content} onChange={e => patch({ content: e.target.value })} />
        </Field>
      </div>
    </div>
  )
}
