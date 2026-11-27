import { useEffect, useMemo, useState, type MutableRefObject } from 'react'
import { useTranslation } from 'react-i18next'

import { destErrorOf, isDestUnavailable, type DestState } from '@/api/accessControl'
import { confirm } from '@/components/ConfirmHost'
import { AsyncButton } from '@/components/common/AsyncButton'
import { FieldHint } from '@/components/common/HelpTip'
import { ErrorState } from '@/components/feedback/ErrorState'
import { pushToast } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Combobox, ComboboxChip, ComboboxChips, ComboboxChipsInput, ComboboxContent, ComboboxEmpty, ComboboxItem, ComboboxList, ComboboxValue, useComboboxAnchor } from '@/components/ui/combobox'
import { Skeleton } from '@/components/ui/skeleton'
import { LazyCodeEditor } from '@/components/LazyCodeEditor'
import { useDestPolicies, useDestSourceRule, useDestSourceRules, useDestStatus, usePutSourceRule } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes, enabledPolicies, enforcingNodeCount, needsFirstPublishConfirm } from '@/utils/accessControl'
import { firstPublishConfirm } from './confirmCopy'
import { destErrorText } from './destErrors'

// ISO 3166-1 alpha-2 officially assigned codes: the choices of "deny source
// country". Names are localised with Intl.DisplayNames at render time.
export const ISO_COUNTRY_CODES: readonly string[] = (
  'AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ ' +
  'CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR ' +
  'GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO ' +
  'JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR ' +
  'MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO ' +
  'RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV ' +
  'TW TZ UA UG US UM UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW'
).split(' ')

export function countryName(code: string, lang: string): string {
  try {
    return new Intl.DisplayNames([lang], { type: 'region' }).of(code) ?? code
  } catch {
    return code
  }
}

const V4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\/(\d{1,2})$/
/** A CIDR prefix, IPv4 or IPv6 (the server canonicalises it; this only catches typos). */
export function isCidr(line: string): boolean {
  const v4 = V4.exec(line)
  if (v4) return v4.slice(1, 5).every(o => Number(o) <= 255) && Number(v4[5]) <= 32
  const m = /^([0-9a-fA-F:.]+)\/(\d{1,3})$/.exec(line)
  if (!m || !m[1].includes(':') || Number(m[2]) > 128) return false
  return /^[0-9a-fA-F:]+$/.test(m[1]) && (m[1].match(/::/g) ?? []).length <= 1 && m[1].split(':').length <= 8
}

/** The non-empty lines, trimmed, and the 1-based numbers of those that are not CIDRs. */
export function parseCidrLines(text: string): { cidrs: string[]; bad: number[] } {
  const cidrs: string[] = []
  const bad: number[] = []
  text.split('\n').forEach((raw, i) => {
    const line = raw.trim()
    if (!line) return
    if (isCidr(line)) cidrs.push(line)
    else bad.push(i + 1)
  })
  return { cidrs, bad }
}

const S = 'admin:access_control.source_rules.'

/** What the host dialog asks of the draft: is it unsaved, and save it now (true = nothing left unsaved). */
export interface SourceDraftHandle {
  dirty: () => boolean
  save: () => Promise<boolean>
}

/** Countries to refuse, picked by typing: chips for what is chosen, each with its own remove button. */
function CountryPicker({ options, value, onChange, label, id, disabled, ...aria }: {
  options: readonly string[]
  value: readonly string[]
  onChange: (next: string[]) => void
  label: (code: string) => string
  id?: string
  disabled?: boolean
  'aria-describedby'?: string
  'aria-invalid'?: boolean | 'true' | 'false'
}) {
  const { t } = useTranslation(['admin'])
  const anchor = useComboboxAnchor()
  return (
    <Combobox multiple items={options as string[]} value={value as string[]} disabled={disabled}
      onValueChange={v => onChange(v as string[])} itemToStringLabel={(c: string) => label(c)}>
      <ComboboxChips ref={anchor}>
        <ComboboxValue>
          {(values: string[]) => (
            <>
              {values.map(c => <ComboboxChip key={c}>{label(c)}</ComboboxChip>)}
              <ComboboxChipsInput id={id} aria-describedby={aria['aria-describedby']} aria-invalid={aria['aria-invalid']} />
            </>
          )}
        </ComboboxValue>
      </ComboboxChips>
      <ComboboxContent anchor={anchor}>
        <ComboboxEmpty>{t(`${S}countries_empty`)}</ComboboxEmpty>
        <ComboboxList>
          {(c: string) => <ComboboxItem key={c} value={c}>{label(c)}</ComboboxItem>}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  )
}

/**
 * "Deny source country or region" for one inbound of a native node. It is a
 * different endpoint from the node's own save (PUT /dest/source-rules/:id), so
 * it keeps its own [Save], its own pending state and its own restart sentence;
 * it is never folded into the node page's save bar. The host learns whether the
 * draft is unsaved through `dirtyRef` (read on demand) and `onDirtyChange` (as it changes).
 */
export default function SourceRulesSection({ nodeId, dirtyRef, onDirtyChange }: {
  nodeId: number
  /** Filled with the draft's handle; the host asks it before closing or saving the inbound. */
  dirtyRef?: MutableRefObject<SourceDraftHandle | null>
  /** Told whenever the draft becomes unsaved or clean. */
  onDirtyChange?: (dirty: boolean) => void
}) {
  const { t, i18n } = useTranslation(['admin'])
  const scope = useQueryScope()
  const read = useDestSourceRule(scope, nodeId)
  const all = useDestSourceRules(scope)
  const policies = useDestPolicies(scope)
  const status = useDestStatus(scope)
  const put = usePutSourceRule(scope)

  const [countries, setCountries] = useState<string[]>([])
  const [cidrText, setCidrText] = useState('')
  // Off / Report only / On: the same three states as a block policy (a source rule is always a block).
  const [state, setState] = useState<DestState>('off')
  // A dialog that is gone has no draft to guard.
  useEffect(() => () => { if (dirtyRef) dirtyRef.current = null }, [dirtyRef])
  const [seeded, setSeeded] = useState<string>('')
  const [fieldError, setFieldError] = useState<{ field?: string; text: string } | null>(null)

  const rule = read.data?.rule
  // Seed the draft once per read of the rule (its updated_at): a refetch of
  // the same rule never clobbers what the admin is typing.
  const seedKey = rule ? `${nodeId}:${rule.updated_at ?? 0}:${rule.state}` : ''
  useEffect(() => {
    if (!rule || seedKey === seeded) return
    setSeeded(seedKey)
    setCountries(rule.countries)
    setCidrText(rule.cidrs.join('\n'))
    setState(rule.state)
    setFieldError(null)
  }, [rule, seedKey, seeded])

  const options = useMemo(
    () => [...ISO_COUNTRY_CODES].sort((a, b) => countryName(a, i18n.language).localeCompare(countryName(b, i18n.language), i18n.language)),
    [i18n.language],
  )
  const parsed = parseCidrLines(cidrText)
  const dirty = !!rule && (
    state !== rule.state
    || countries.join() !== rule.countries.join()
    || parsed.cidrs.join() !== rule.cidrs.join()
  )
  useEffect(() => { onDirtyChange?.(dirty) }, [dirty, onDirtyChange])

  if (read.isPending) return <Skeleton className="mt-4 h-32 w-full" />
  if (read.isError || !read.data) {
    if (isDestUnavailable(read.error)) return null
    return <ErrorState compact className="mt-4" title={t(`${S}read_failed`)} retry={() => void read.refetch()} />
  }
  if (!read.data.native) return null

  const minutes = applyMinutes(status.data)
  const geoip = read.data.geoip_available

  const save = async (): Promise<boolean> => {
    if (!rule) return false
    if (parsed.bad.length > 0) {
      setFieldError({ field: 'cidrs', text: t(`${S}bad_lines`, { lines: parsed.bad.join(', ') }) })
      return false
    }
    // Report-only is deployed too: the node restarts for it.
    const policiesOn = enabledPolicies(policies.data).length
    const sourceRulesOn = (all.data?.items ?? []).filter(r => r.state !== 'off' && r.node_id !== nodeId).length
    if (needsFirstPublishConfirm({ policiesOn, sourceRulesOn, groupsOn: policies.data?.allowlist_groups.length ?? 0 }, { turnsOn: state !== 'off' && (countries.length > 0 || parsed.cidrs.length > 0) })) {
      const nodes = enforcingNodeCount(status.data)
      const ok = await confirm(firstPublishConfirm({
        t, minutes, nodes, seconds: null,
      }))
      if (!ok) return false
    }
    setFieldError(null)
    try {
      await put.mutateAsync({
        nodeId,
        input: { countries, cidrs: parsed.cidrs, state, updated_at: rule.updated_at ?? undefined },
      })
      pushToast({ message: t(`${S}saved`), severity: 'success' })
      return true
    } catch (err) {
      const e = destErrorOf(err)
      if (e?.code === 'dest_source_rule_stale') {
        setFieldError({ text: t(`${S}stale`) })
        void read.refetch()
      } else if (e?.status === 400 && e.field) {
        setFieldError({ field: e.field, text: destErrorText(t, err) })
      } else {
        setFieldError({ text: destErrorText(t, err) })
      }
      return false
    }
  }
  if (dirtyRef) dirtyRef.current = { dirty: () => dirty, save }

  const cidrError = parsed.bad.length > 0
    ? t(`${S}bad_lines`, { lines: parsed.bad.join(', ') })
    : fieldError?.field === 'cidrs' ? fieldError.text : undefined

  return (
    <section data-testid="source-rules-section" className="mt-5 grid gap-4 rounded-xl border bg-card p-4">
      <div>
        <h2 className="text-base font-semibold">{t(`${S}title`)}</h2>
        <p className="mt-1 text-sm text-muted-foreground">{t(`${S}hint`)}</p>
      </div>
      {!geoip && <div className="text-sm"><FieldHint summary={t(`${S}geoip_missing.summary`)} detail={t(`${S}geoip_missing.detail`)} /></div>}
      {rule?.note === 'geoip_unavailable' && geoip && (
        <div className="text-sm"><FieldHint summary={t(`${S}note.geoip_unavailable.summary`)} detail={t(`${S}note.geoip_unavailable.detail`)} /></div>
      )}
      {rule?.note === 'empty' && (
        <div className="text-sm"><FieldHint tone="muted" summary={t(`${S}note.empty.summary`)} detail={t(`${S}note.empty.detail`)} /></div>
      )}
      {fieldError && !fieldError.field && <p role="alert" className="text-sm text-destructive">{fieldError.text}</p>}
      <Field label={t(`${S}countries`)} error={fieldError?.field === 'countries' ? fieldError.text : undefined} errorBlocksSave={false}>
        {p => (
          <CountryPicker id={p.id} aria-describedby={p['aria-describedby']} aria-invalid={p['aria-invalid']}
            options={options} value={countries} onChange={setCountries} disabled={put.isPending}
            label={c => `${countryName(c, i18n.language)} (${c})`} />
        )}
      </Field>
      <Field label={t(`${S}cidrs`)} hint={t(`${S}cidrs_hint`)} error={cidrError} errorBlocksSave={false}>
        <LazyCodeEditor key={seedKey} language="plain" tabMovesFocus minRows={3} maxRows={12} value={cidrText} disabled={put.isPending}
          placeholder="203.0.113.0/24" onChange={setCidrText} />
      </Field>
      {dirty && <div className="text-sm"><FieldHint summary={t(`${S}unsaved.summary`)} detail={t(`${S}unsaved.detail`)} /></div>}
      <div className="flex flex-wrap items-end gap-3">
        <div className="grid min-w-60 flex-1 gap-1.5">
          <p id={`src-state-${nodeId}`} className="text-sm font-semibold">{t(`${S}state`)}</p>
          <SegmentedControl<DestState> aria-labelledby={`src-state-${nodeId}`} value={state} disabled={put.isPending}
            onChange={setState} className="w-fit"
            options={(['off', 'report', 'on'] as const).map(st => ({ value: st, label: t(`admin:access_control.policies.state.${st}`) }))} />
          <p className="text-sm text-muted-foreground">{t(`${S}state_help.${state}`)}</p>
        </div>
        <AsyncButton size="sm" disabled={!dirty} pending={put.isPending} onClick={save}>{t(`${S}save`)}</AsyncButton>
      </div>
      <p className="-mt-2 text-xs text-muted-foreground">
        {minutes === null ? t(`${S}restart_unknown`) : t(`${S}restart`, { count: minutes })}
      </p>
    </section>
  )
}
