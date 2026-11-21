import { Link } from 'react-router'

import type { Group } from '@/api/types'
import { FormSection } from '@/components/form/FormSection'
import ScopeOverridesEditor from '@/components/scope/ScopeOverridesEditor'
import type { ScopeState } from '@/components/scope/scopeOverrides'
import { ScopeGate, type ScopeLoad } from './ScopeGate'
import { useGroupsT } from './useGroupsT'

/** The one overridable setting that shapes how a subscription's node names look: the region flag in front of them. */
export const LAYOUT_SCOPE_KEYS = ['sub.sub_region_flag_prefix'] as const

/**
 * How a group's subscription is laid out. Order is no longer decided here: the Nodes page has ONE global order
 * that admins arrange by dragging, and a group's sort strategy and weights only order lines that tie (lines
 * never positioned). The saved layout (separators, strategy, weights) is shown for reference, as it always
 * was: nothing on this page edits or saves it. The region flag is a per-group override of a global setting, so
 * it is saved with the other overrides.
 */
export function LayoutTab({ group, scope, onScope, reloadScope }: {
  /** The saved group; null while creating. */
  group: Group | null
  scope: ScopeLoad
  onScope: (next: ScopeState) => void
  reloadScope: () => void
}) {
  const t = useGroupsT()
  const layout = group?.layout
  const separators = layout?.separators?.length ?? 0
  const weights = layout?.sort?.length ?? 0
  const strategy = layout?.default_sort_strategy
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-2 text-sm">
        <p>{t('admin:groups.layout.order_note')}</p>
        <p><Link to="/admin/nodes" className="underline underline-offset-2 outline-none focus-visible:ring-3 focus-visible:ring-ring/50">{t('admin:groups.layout.open_nodes')}</Link></p>
      </div>

      {group && (
        <FormSection title={t('admin:groups.layout.saved_title')} description={t('admin:groups.layout.saved_note')}>
          <dl className="grid grid-cols-[minmax(0,auto)_minmax(0,1fr)] gap-x-6 gap-y-1.5 text-sm">
            <dt className="text-muted-foreground">{t('admin:groups.layout.separators')}</dt>
            <dd className="tabular-nums">{separators > 0 ? separators : t('admin:groups.layout.none')}</dd>
            <dt className="text-muted-foreground">{t('admin:groups.layout.sort_strategy')}</dt>
            <dd className="break-words">{strategy || t('admin:groups.layout.strategy_default')}</dd>
            <dt className="text-muted-foreground">{t('admin:groups.layout.sort_weights')}</dt>
            <dd className="tabular-nums">{weights > 0 ? weights : t('admin:groups.layout.none')}</dd>
          </dl>
        </FormSection>
      )}

      <FormSection title={t('admin:groups.layout.flag_title')}>
        {group ? (
          <ScopeGate load={scope} retry={reloadScope}>
            {s => <ScopeOverridesEditor scope={s} onChange={onScope} categories={['sub']} keys={LAYOUT_SCOPE_KEYS} hideCategoryCaptions />}
          </ScopeGate>
        ) : (
          <p className="text-sm text-muted-foreground">{t('admin:groups.layout.create_hint')}</p>
        )}
      </FormSection>
    </div>
  )
}
