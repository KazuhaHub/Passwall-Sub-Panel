import { useTranslation } from 'react-i18next'

import { confirm } from '@/components/ConfirmHost'
import { useDestPolicies, useDestSettings, useDestSourceRules, useDestStatus } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes, enforcingNodeCount, needsFirstPublishConfirm } from '@/utils/accessControl'
import { firstPublishConfirm } from '../confirmCopy'

/**
 * The one gate every "something becomes enforced" write goes through (S20):
 * the row's switch, the editor's save and a template. Resolves true to go on.
 * Counts come from the cached reads; an unknown node count says "each node".
 */
export function useFirstPublishGate() {
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const policies = useDestPolicies(scope)
  const sources = useDestSourceRules(scope)
  const status = useDestStatus(scope)
  const settings = useDestSettings(scope)
  return async (turnsOn: boolean): Promise<boolean> => {
    const policiesOn = [...(policies.data?.allow ?? []), ...(policies.data?.block ?? [])].filter(p => p.state === 'on').length
    const sourceRulesOn = (sources.data?.items ?? []).filter(r => r.state === 'on').length
    if (!needsFirstPublishConfirm({ policiesOn, sourceRulesOn }, { turnsOn })) return true
    const nodes = enforcingNodeCount(status.data)
    return confirm(firstPublishConfirm({
      t, minutes: applyMinutes(status.data), nodes,
      seconds: settings.data?.effective.dest_policy_apply_min_seconds ?? null,
    }))
  }
}
