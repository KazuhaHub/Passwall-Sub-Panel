import { lazy, Suspense } from 'react'
import { useTranslation } from 'react-i18next'

import type { DestList, DestScope } from '@/api/accessControl'
import { AccessBadge } from '@/components/access/AccessBadge'
import { Field } from '@/components/form/Field'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { FieldHint } from '@/components/common/HelpTip'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { MultiPicker, type PickOption } from './MultiPicker'
import type { PolicyForm } from './policyForm'

// The IP-range box is a code editor (plain: a list is not YAML), loaded only
// when an editor opens.
const CodeEditor = lazy(() => import('@/components/CodeEditor'))

const D = 'admin:access_control.policies.editor.'

type Setter = <K extends keyof PolicyForm>(k: K, v: PolicyForm[K]) => void

/**
 * WHAT THE POLICY MATCHES: destination lists, inline IP ranges, ports, network and the two switches. A policy
 * made from a catalog template keeps its matching (`locked`): the mapping is the catalog's, not the admin's.
 */
export function MatchFields({ form, set, errors, touched, locked, allLists, notReadyCount }: {
  form: PolicyForm
  set: Setter
  errors: Record<string, string>
  touched: boolean
  locked: boolean
  allLists: readonly DestList[]
  notReadyCount: number
}) {
  const { t, i18n } = useTranslation(['admin'])
  const num = new Intl.NumberFormat(i18n.language)
  const options: PickOption[] = allLists.map(l => {
    const pending = l.state === 'pending' || (l.state === 'failed' && l.last_fetched_at === null)
    return {
      id: l.id, name: l.name, disabled: l.empty && l.kind === 'custom',
      row: (
        <span className="flex flex-wrap items-center gap-2">
          <span>{t(`${D}list_option`, { name: l.name, kind: t(`admin:access_control.policies.list_kind.${l.kind}`), entries: num.format(l.entry_count), regexps: l.regexp_count })}</span>
          {pending && <AccessBadge kind="attention" label={t(`${D}list_not_ready`)} />}
        </span>
      ),
    }
  })
  return (
    <section className="grid gap-4">
      <h3 className="text-sm font-semibold">{t(`${D}match`)}</h3>
      {locked && <FieldHint tone="muted" summary={t(`${D}template_locked.summary`)} detail={t(`${D}template_locked.detail`)} />}
      <Field label={t(`${D}lists`)} error={errors.list_ids}>
        {p => <MultiPicker id={p.id} options={options} value={form.listIds} disabled={locked}
          emptyText={t(`${D}pick_none`)} onChange={ids => set('listIds', ids)} aria-describedby={p['aria-describedby']} aria-invalid={p['aria-invalid']} />}
      </Field>
      {notReadyCount > 0 && <FieldHint tone="warning" summary={t(`${D}not_ready_hint.summary`)} detail={t(`${D}not_ready_hint.detail`)} />}

      <Field label={t(`${D}cidrs`)} error={errors.cidrs}
        hint={<FieldHint tone="muted" summary={t(`${D}cidr_hint.summary`)} detail={t(`${D}cidr_hint.detail`)} />}>
        {() => (
          <Suspense fallback={<Skeleton className="h-32 w-full" />}>
            <CodeEditor value={form.cidrs} onChange={v => set('cidrs', v)} language="plain" minRows={6} readOnly={locked} />
          </Suspense>
        )}
      </Field>

      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t(`${D}ports`)} error={errors.ports} hint={t(`${D}ports_hint`)}>
          <Input disabled={locked} value={form.ports} onChange={e => set('ports', e.target.value)} />
        </Field>
        <Field label={t(`${D}network`)}>
          {p => (
            <SegmentedControl aria-labelledby={p.labelId} disabled={locked} value={form.network || 'all'}
              onChange={v => set('network', v === 'all' ? '' : v)}
              options={[{ value: 'all', label: t(`${D}network_all`) }, { value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }]} />
          )}
        </Field>
      </div>

      <div className="grid gap-3">
        <Field orientation="horizontal" label={t(`${D}bittorrent`)} hint={form.action === 'allow' ? t(`${D}bittorrent_block_only`) : t(`${D}bittorrent_hint`)}>
          <Checkbox disabled={locked || form.action === 'allow'} checked={form.bittorrent} onCheckedChange={c => set('bittorrent', c)} />
        </Field>
        <Field orientation="horizontal" label={t(`${D}private`)}>
          <Checkbox disabled={locked} checked={form.private} onCheckedChange={c => set('private', c)} />
        </Field>
        {form.action === 'block' && (
          <Field orientation="horizontal" label={t(`${D}counts_as_risk`)} hint={t(`${D}counts_as_risk_hint`)}>
            <Checkbox checked={form.countsAsRisk} onCheckedChange={c => set('countsAsRisk', c)} />
          </Field>
        )}
      </div>
      {errors.match && touched && <p role="alert" className="text-sm text-destructive">{errors.match}</p>}
    </section>
  )
}

/** WHO THE POLICY APPLIES TO: everyone, some groups, or the nodes of some servers. */
export function ScopeFields({ form, set, errors, groupOptions, panelOptions, removed }: {
  form: PolicyForm
  set: Setter
  errors: Record<string, string>
  groupOptions: readonly PickOption[]
  panelOptions: readonly PickOption[]
  /** Group and server ids the policy named that no longer exist: dropped from the form, counted here. */
  removed: number
}) {
  const { t } = useTranslation(['admin'])
  return (
    <section className="grid gap-3">
      <Field label={t(`${D}scope`)}>
        {p => (
          <SegmentedControl aria-labelledby={p.labelId} value={form.scope} onChange={(v: DestScope) => set('scope', v)}
            options={[
              { value: 'all', label: t(`${D}scope_all`) },
              { value: 'groups', label: t(`${D}scope_groups`) },
              { value: 'panels', label: t(`${D}scope_panels`) },
            ]} />
        )}
      </Field>
      {form.scope === 'groups' && (
        <Field label={t(`${D}groups`)} error={errors.group_ids ?? errors.scope}>
          {p => <MultiPicker id={p.id} options={groupOptions} value={form.groupIds} emptyText={t(`${D}pick_none`)} onChange={ids => set('groupIds', ids)}
            aria-describedby={p['aria-describedby']} aria-invalid={p['aria-invalid']} />}
        </Field>
      )}
      {form.scope === 'panels' && (
        <Field label={t(`${D}panels`)} error={errors.panel_ids ?? errors.scope}>
          {p => <MultiPicker id={p.id} options={panelOptions} value={form.panelIds} emptyText={t(`${D}pick_none`)} onChange={ids => set('panelIds', ids)}
            aria-describedby={p['aria-describedby']} aria-invalid={p['aria-invalid']} />}
        </Field>
      )}
      {removed > 0 && <p className="text-sm text-muted-foreground">{t(`${D}removed_deleted`, { count: removed })}</p>}
    </section>
  )
}
