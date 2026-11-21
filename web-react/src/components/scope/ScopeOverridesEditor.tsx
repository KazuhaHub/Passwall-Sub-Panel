// ScopeOverridesEditor renders the per-field inherit/override controls for one
// scope (a group). Pure controlled component: `scope` in, `onChange` out — it
// performs no I/O. Used by the group sheet's Overrides and Layout tabs, the
// Settings scope rail and the risk center's group card. Restrict to a subset
// with `categories` (default = all) and `keys` / `omitKeys` (default = every
// key the backend allows for the scope).
//
// Every row is an InheritanceField over two levels, Global -> This group, so
// the person always sees (1) the global value the group would inherit, (2)
// whether the group overrides it, and (3) the way back to "follow global".
// Only keys the backend lists in `scope.overridable` are offered: a key it
// refuses never gets a control.
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { InheritanceField, type InheritanceLevel } from '@/components/form/InheritanceField'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { goParseFloat } from '@/utils/goParseFloat'

import { SCOPE_CATEGORIES, SCOPE_KEYS, type ScopeKeyMeta, type ScopeState } from './scopeOverrides'

export default function ScopeOverridesEditor({
  scope,
  onChange,
  categories,
  keys,
  omitKeys,
  hideCategoryCaptions,
  showHints,
}: {
  scope: ScopeState
  onChange: (next: ScopeState) => void
  /** Restrict to these category ids; omit for all categories. */
  categories?: string[]
  /** Restrict to these "type.name" keys (still only those the backend allows). */
  keys?: readonly string[]
  /** Leave these "type.name" keys out (a host that shows them elsewhere). */
  omitKeys?: readonly string[]
  /** Suppress the per-category caption (e.g. when the surrounding section
   *  already names the single category being shown). */
  hideCategoryCaptions?: boolean
  /** Show each row's hint (ScopeKeyMeta.hintKey) under it. The risk
   *  center's group card asks for it: there the rows stand without the
   *  global fields beside them that explain the same knobs. */
  showHints?: boolean
}) {
  const { t } = useTranslation(['admin', 'common'])
  const cats = categories
    ? SCOPE_CATEGORIES.filter(c => categories.includes(c.id))
    : SCOPE_CATEGORIES

  const optionLabel = (k: ScopeKeyMeta, value: string) => {
    const o = k.options?.find(o => o.value === value)
    return o ? t(`admin:groups.scope.${o.labelKey}`, { defaultValue: o.def }) : value
  }
  const defaultSuffix = () => t('admin:groups.scope.default_suffix', { defaultValue: '（默认）' })

  // Localized display of a value at either level. Bools render as the
  // language's On/Off; an enum renders its option's label, and an unset one
  // ('') the label of the value the server uses for it. A key with an
  // unsetValue shows that default for a stored ''/'0', because there 0 means
  // "never configured", not zero. Everything else shows the raw string.
  const fmtVal = (k: ScopeKeyMeta, raw: string) => {
    if (k.kind === 'bool') {
      return raw === '1'
        ? t('admin:groups.scope.on', { defaultValue: 'On' })
        : t('admin:groups.scope.off', { defaultValue: 'Off' })
    }
    if (k.kind === 'enum') {
      return raw === '' && k.enumDefault !== undefined
        ? `${optionLabel(k, k.enumDefault)}${defaultSuffix()}`
        : optionLabel(k, raw)
    }
    if (k.unsetValue !== undefined && (raw === '' || raw === '0')) {
      return `${k.unsetValue}${defaultSuffix()}`
    }
    return raw === '' ? '—' : raw
  }

  // What an OVERRIDDEN value of such a key will mean, since the number in the
  // box does not say it. null = it means what it says; no hint.
  //
  // The server has two readings, and they are different:
  //   - an integer (intField.Unmarshal parses it) that is not positive is
  //     "never configured", exactly as for the global value, and becomes the
  //     SHIPPED DEFAULT (GeoPolicyFromSettings keeps only a positive number)
  //     — not 0 and not the global value. Against a global 4, a group admin
  //     typing 0 for "zero" or for "inherit" has made the group stricter (2);
  //   - an empty or unparsable value is skipped by applyScopeOverrides, which
  //     leaves the inherited GLOBAL value in place — the override does
  //     nothing, and "= default" would be the same lie the bare 0 told.
  //     "Parsable" is the parser the key's descriptor uses. For an int row
  //     that is Go's strconv.Atoi (intField): an optional sign, then digits —
  //     so '1.5' and '1e3' are skipped too. For a float row it is
  //     strconv.ParseFloat (floatField), which takes both, so read with the
  //     int rule a real ratio of 2.5 would be called an override that does
  //     nothing. It is ParseFloat itself (goParseFloat), not a decimal
  //     pattern, because the value may have come in through the API: 'Inf',
  //     'NaN', '1_5' and '0x1.8p1' all apply, and 1e400 is skipped — Go
  //     calls it out of range where Number() would answer Infinity.
  // A parsed NaN is neither > 0 nor <= 0. The policy keeps a value only when
  // it is > 0 (RiskPolicyFromSettings), so NaN takes the default like a 0
  // does — hence the test is "> 0 or the default", not "<= 0 or nothing".
  // +Inf applies and means "never over", which is what it says.
  // The field keeps what was typed; the hint says which of the two it is.
  const decode = (k: ScopeKeyMeta, raw: string) =>
    k.kind === 'float'
      ? goParseFloat(raw)
      : /^[+-]?\d+$/.test(raw) ? Number(raw) : undefined
  const overrideHint = (k: ScopeKeyMeta, raw: string) => {
    if (k.unsetValue === undefined) return null
    const v = decode(k, raw)
    if (v === undefined) {
      return `= ${t('admin:groups.scope.global_prefix', { defaultValue: '全局' })}: ${fmtVal(k, scope.global[k.key])}`
    }
    return v > 0 ? null : `= ${k.unsetValue}${defaultSuffix()}`
  }

  const globalLabel = t('admin:groups.scope.global_prefix', { defaultValue: '全局' })
  const groupLabel = t('admin:groups.scope.level_group', { defaultValue: '本组' })

  const row = (k: ScopeKeyMeta) => {
    const st = scope.edit[k.key]
    const levels: InheritanceLevel<string>[] = [
      { id: 'global', label: globalLabel, value: scope.global[k.key] },
      { id: 'group', label: groupLabel, value: st.on ? st.value : undefined },
    ]
    const hint: ReactNode[] = []
    if (showHints && k.hintKey) hint.push(<span key="hint" className="block">{t(k.hintKey)}</span>)
    const own = st.on ? overrideHint(k, st.value) : null
    if (own) hint.push(<span key="own" className="block">{own}</span>)
    return (
      <InheritanceField<string>
        key={k.key}
        label={t(`admin:groups.scope.${k.labelKey}`, { defaultValue: k.def })}
        levels={levels}
        editLevel="group"
        // The group's value is a raw string; undefined is "follow global", which
        // stores nothing and puts the global value back as the editor's baseline.
        onChange={next => onChange({
          ...scope,
          edit: { ...scope.edit, [k.key]: next === undefined ? { on: false, value: scope.global[k.key] } : { on: true, value: next } },
        })}
        renderValue={v => fmtVal(k, v)}
        defaultValue={k.kind === 'enum' && k.enumDefault !== undefined ? k.enumDefault : scope.global[k.key]}
        // An inherited '' is not one of an enum's options: the select starts on
        // the default it stands for, and nothing is written until it is edited.
        fitInherited={k.kind === 'enum' && k.enumDefault !== undefined
          ? v => (k.options?.some(o => o.value === v) ? v : k.enumDefault!)
          : undefined}
        hint={hint.length > 0 ? hint : undefined}
        renderEditor={p => {
          const set = (value: string) => p.onChange(value)
          if (k.kind === 'bool') {
            return (
              <label className="flex w-fit items-center gap-2 text-sm">
                <Switch id={p.id} aria-labelledby={p['aria-labelledby']} aria-describedby={p['aria-describedby']}
                  checked={p.value === '1'} onCheckedChange={c => set(c ? '1' : '0')} />
                <span>{fmtVal(k, p.value)}</span>
              </label>
            )
          }
          if (k.kind === 'enum') {
            return (
              <NativeSelect id={p.id} aria-labelledby={p['aria-labelledby']} aria-describedby={p['aria-describedby']}
                className="sm:max-w-xs" value={p.value} onChange={e => set(e.target.value)}>
                {(k.options ?? []).map(o => (
                  <option key={o.value} value={o.value}>{t(`admin:groups.scope.${o.labelKey}`, { defaultValue: o.def })}</option>
                ))}
              </NativeSelect>
            )
          }
          if (k.kind === 'str') {
            return k.multiline
              ? <Textarea id={p.id} aria-labelledby={p['aria-labelledby']} aria-describedby={p['aria-describedby']}
                  rows={3} className="font-mono text-[13px]" value={p.value} onChange={e => set(e.target.value)} />
              : <Input id={p.id} aria-labelledby={p['aria-labelledby']} aria-describedby={p['aria-describedby']}
                  value={p.value} onChange={e => set(e.target.value)} />
          }
          // The box keeps what was typed (an empty or odd value is meaningful to
          // the server, see overrideHint), so it is a plain number input rather
          // than a NumberField that would rewrite it.
          return (
            <Input id={p.id} aria-labelledby={p['aria-labelledby']} aria-describedby={p['aria-describedby']}
              type="number" inputMode="decimal" className="w-28"
              min={0} step={k.kind === 'float' ? 'any' : 1}
              value={p.value} onChange={e => set(e.target.value)} />
          )
        }}
      />
    )
  }

  return (
    <>
      {cats.map(cat => {
        const shown = SCOPE_KEYS.filter(k => k.cat === cat.id
          && scope.overridable.includes(k.key)
          && (!keys || keys.includes(k.key))
          && !omitKeys?.includes(k.key))
        if (!shown.length) return null
        return (
          <div key={cat.id} className="flex flex-col gap-4">
            {!hideCategoryCaptions && (
              <h4 className="text-xs font-semibold text-muted-foreground">
                {t(`admin:groups.scope.${cat.labelKey}`, { defaultValue: cat.def })}
              </h4>
            )}
            <div className="flex flex-col gap-5 divide-y [&>*:not(:first-child)]:pt-5">{shown.map(row)}</div>
          </div>
        )
      })}
    </>
  )
}
