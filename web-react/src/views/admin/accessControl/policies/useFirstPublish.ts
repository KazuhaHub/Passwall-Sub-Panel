import { useTranslation } from 'react-i18next'

import type { DestAction, DestScope, DestState } from '@/api/accessControl'
import { confirm } from '@/components/ConfirmHost'
import { useDestPolicies, useDestSettings, useDestSourceRules, useDestStatus } from '@/query/accessControl'
import { useAllGroups } from '@/query/groups'
import { useServersList } from '@/query/servers'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes, enabledPolicies, enforcingNodeCount, needsFirstPublishConfirm } from '@/utils/accessControl'
import { firstPublishConfirm } from '../confirmCopy'
import { enablePolicyConfirm } from './enableConfirm'

/** The policy a write is about to switch on, so the question can name who it reaches. */
export interface EnableSubject {
  name: string
  action: DestAction
  state: DestState
  scope: DestScope
  groupIds: number[]
  panelIds: number[]
}

const NAMES_SHOWN = 3

/**
 * The one gate every "something becomes enforced" write goes through (S20):
 * the row's switch, the editor's save and a template. Resolves true to go on.
 * Counts come from the cached reads; an unknown node count says "each node".
 *
 * The very first policy asks the longer first-publish question. Any later one that is switched on
 * from off (`subject` given) asks which groups, users and nodes it reaches; the two are never asked
 * together.
 */
export function useFirstPublishGate() {
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const policies = useDestPolicies(scope)
  const sources = useDestSourceRules(scope)
  const status = useDestStatus(scope)
  const settings = useDestSettings(scope)
  const groups = useAllGroups(scope)
  const servers = useServersList(scope, { page: 1, page_size: 200 })
  return async (turnsOn: boolean, subject?: EnableSubject): Promise<boolean> => {
    // Report-only is deployed too (the nodes restart for it), so it is "something enforced" for this purpose.
    const policiesOn = enabledPolicies(policies.data).length
    const sourceRulesOn = (sources.data?.items ?? []).filter(r => r.state !== 'off').length
    const groupsOn = policies.data?.allowlist_groups.length ?? 0
    const nodes = enforcingNodeCount(status.data)
    if (needsFirstPublishConfirm({ policiesOn, sourceRulesOn, groupsOn }, { turnsOn })) {
      return confirm(firstPublishConfirm({
        t, minutes: applyMinutes(status.data), nodes,
        seconds: settings.data?.effective.dest_policy_apply_min_seconds ?? null,
      }))
    }
    if (!turnsOn || !subject || subject.state === 'off') return true
    const all = groups.data ?? []
    const byId = new Map(all.map(g => [g.id, g]))
    const pickedGroups = subject.scope === 'groups' ? subject.groupIds : all.map(g => g.id)
    const panelNames = new Map((servers.data?.items ?? []).map(s => [s.id, s.name]))
    const shown = (names: string[]) => names.slice(0, NAMES_SHOWN).join(t('admin:access_control.policies.separator')) + (names.length > NAMES_SHOWN ? ` +${names.length - NAMES_SHOWN}` : '')
    return confirm(enablePolicyConfirm(
      { t, minutes: applyMinutes(status.data), nodes },
      { name: subject.name, action: subject.action, state: subject.state },
      {
        scope: subject.scope,
        groups: pickedGroups.length,
        users: pickedGroups.reduce((n, id) => n + (byId.get(id)?.members ?? 0), 0),
        panels: subject.panelIds.length,
        names: subject.scope === 'groups'
          ? shown(subject.groupIds.map(id => byId.get(id)?.name ?? `#${id}`))
          : shown(subject.panelIds.map(id => panelNames.get(id) ?? `#${id}`)),
      },
    ))
  }
}
