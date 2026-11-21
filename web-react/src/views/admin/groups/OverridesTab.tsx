import { Link } from 'react-router'

import type { Group } from '@/api/types'
import { FormSection } from '@/components/form/FormSection'
import ScopeOverridesEditor from '@/components/scope/ScopeOverridesEditor'
import { overriddenCount, SCOPE_CATEGORIES, SCOPE_KEYS, type ScopeState } from '@/components/scope/scopeOverrides'
import { LAYOUT_SCOPE_KEYS } from './LayoutTab'
import { ScopeGate, type ScopeLoad } from './ScopeGate'
import { useGroupsT } from './useGroupsT'

/**
 * Categories edited on this tab. The detector exceptions (locations, auto-suspension, risk signals) are edited on
 * the risk center's policy page, beside the global values they override; saveScopeState writes only what
 * changed, so leaving them out here leaves them untouched.
 */
const CATEGORIES = ['2fa', 'notify', 'emergency', 'login', 'sub'] as const
const RISK_CATEGORIES = new Set(['geo', 'geo_ban', 'risk'])

/**
 * The group's overrides of global settings. Every row is an InheritanceField: the global value the group would
 * inherit, whether the group overrides it, and the switch back to "follow the level above". Only the keys the
 * backend allows per group are offered (scope.overridable); settings read before identity is known (login mode,
 * lockout, captcha, sub_path, sub_clients) are global-only and never appear. A category is a collapsible section
 * whose heading says how many of its rows are overridden, so a collapsed section still tells the truth.
 */
export function OverridesTab({ group, scope, onScope, reloadScope }: {
  group: Group | null
  scope: ScopeLoad
  onScope: (next: ScopeState) => void
  reloadScope: () => void
}) {
  const t = useGroupsT()
  if (!group) return <p className="text-sm text-muted-foreground">{t('admin:groups.scope.create_hint')}</p>
  return (
    <div className="flex flex-col gap-6">
      <p className="text-sm text-muted-foreground">{t('admin:groups.overrides.intro')}</p>
      <ScopeGate load={scope} retry={reloadScope}>
        {s => {
          // The layout tab owns the region flag; it is not repeated here.
          const rowsOf = (cat: string) => SCOPE_KEYS.filter(k => k.cat === cat && !LAYOUT_SCOPE_KEYS.includes(k.key as never) && s.overridable.includes(k.key))
          const elsewhere = overriddenCount(s, SCOPE_KEYS.filter(k => RISK_CATEGORIES.has(k.cat)))
          return (
            <>
              {CATEGORIES.map(cat => {
                const rows = rowsOf(cat)
                if (rows.length === 0) return null
                const n = overriddenCount(s, rows)
                const label = SCOPE_CATEGORIES.find(c => c.id === cat)!
                return (
                  <FormSection key={cat} advanced sectionKey={`groups.scope.${cat}`}
                    title={t(`admin:groups.scope.${label.labelKey}`, { defaultValue: label.def })}
                    description={n > 0 ? t('admin:groups.overrides.count', { count: n }) : t('admin:groups.overrides.none')}>
                    <ScopeOverridesEditor scope={s} onChange={onScope} categories={[cat]} omitKeys={LAYOUT_SCOPE_KEYS} hideCategoryCaptions />
                  </FormSection>
                )
              })}
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t pt-4 text-xs text-muted-foreground">
                <span className="min-w-0 flex-1 basis-60">
                  {t('admin:groups.scope.policy_pointer')}
                  {elsewhere > 0 && <> {t('admin:groups.overrides.risk_other', { count: elsewhere })}</>}
                </span>
                {/* Same tab: the sheet's own guard asks before a draft is dropped. */}
                <Link to={`/admin/risk?tab=policy&group=${group.id}`} className="text-sm text-foreground underline underline-offset-2 outline-none focus-visible:ring-3 focus-visible:ring-ring/50">{t('admin:groups.scope.open_policy')}</Link>
              </div>
            </>
          )
        }}
      </ScopeGate>
    </div>
  )
}
