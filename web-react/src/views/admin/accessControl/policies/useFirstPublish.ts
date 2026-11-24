import { useTranslation } from 'react-i18next'

import type { DestAction, DestScope, DestState } from '@/api/accessControl'
import { confirm } from '@/components/ConfirmHost'
import { useDestPolicies, useDestSettings, useDestSourceRules, useDestStatus } from '@/query/accessControl'
import { useAllGroups } from '@/query/groups'
import { useServersList } from '@/query/servers'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes, enabledPolicies, enforcingNodeCount, needsFirstPublishConfirm } from '@/utils/accessControl'
import { firstPublishConfirm } from '../confirmCopy'
import { enablePolicyConfirm, enableScopeText, type EnableScope } from './enableConfirm'

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

/** The reads every enabling question is made of: the groups and servers a policy names, the node count, the exemptions. */
function useEnableReads() {
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const policies = useDestPolicies(scope)
  const status = useDestStatus(scope)
  const groups = useAllGroups(scope)
  const servers = useServersList(scope, { page: 1, page_size: 200 })
  const nodes = enforcingNodeCount(status.data)
  const minutes = applyMinutes(status.data)
  const scopeOf = (subject: EnableSubject): EnableScope => {
    // A read that has not answered (or failed) leaves the counts unknown, never zero.
    const read = groups.isSuccess ? groups.data : null
    const all = read ?? []
    const byId = new Map(all.map(g => [g.id, g]))
    const pickedGroups = subject.scope === 'groups' ? subject.groupIds : all.map(g => g.id)
    const panelNames = new Map((servers.data?.items ?? []).map(s => [s.id, s.name]))
    const shown = (names: string[]) => names.slice(0, NAMES_SHOWN).join(t('admin:access_control.policies.separator')) + (names.length > NAMES_SHOWN ? ` +${names.length - NAMES_SHOWN}` : '')
    return {
      scope: subject.scope,
      // A scoped policy names its own groups, so their number is known; the people in them are not without the read.
      groups: subject.scope === 'groups' ? subject.groupIds.length : read ? pickedGroups.length : null,
      users: read ? pickedGroups.reduce((n, id) => n + (byId.get(id)?.members ?? 0), 0) : null,
      panels: subject.panelIds.length,
      names: subject.scope === 'groups'
        ? shown(subject.groupIds.map(id => byId.get(id)?.name ?? `#${id}`))
        : shown(subject.panelIds.map(id => panelNames.get(id) ?? `#${id}`)),
      exempt: policies.data ? policies.data.exemptions.count : null,
    }
  }
  return { t, scope, policies, status, nodes, minutes, scopeOf }
}

/**
 * Who a policy reaches and what the nodes do about it, as a sentence, for a dialog that already asks the
 * question (the "report only or blocking" choice) and so must not be followed by a second one.
 */
export function useEnableScopeText() {
  const { t, nodes, minutes, scopeOf } = useEnableReads()
  return (subject: EnableSubject): string => enableScopeText({ t, minutes, nodes }, subject.action, scopeOf(subject))
}

/**
 * The one gate every "something becomes enforced" write goes through (S20):
 * the row's switch, the editor's save and a template. Resolves true to go on.
 * Counts come from the cached reads; an unknown node count says "the nodes it applies to".
 *
 * The very first policy asks the longer first-publish question. Any later one that is switched on
 * from off (`subject` given) asks which groups, users and nodes it reaches; the two are never asked
 * together. `asked: true` says a dialog the person just answered already named that scope (the
 * report-only-or-blocking choice), so only the first-publish question can still follow.
 */
export function useFirstPublishGate() {
  const { t, scope, policies, nodes, minutes, scopeOf } = useEnableReads()
  const sources = useDestSourceRules(scope)
  const settings = useDestSettings(scope)
  return async (turnsOn: boolean, subject?: EnableSubject, opts?: { asked?: boolean }): Promise<boolean> => {
    // Report-only is deployed too (the nodes restart for it), so it is "something enforced" for this purpose.
    const policiesOn = enabledPolicies(policies.data).length
    const sourceRulesOn = (sources.data?.items ?? []).filter(r => r.state !== 'off').length
    const groupsOn = policies.data?.allowlist_groups.length ?? 0
    if (needsFirstPublishConfirm({ policiesOn, sourceRulesOn, groupsOn }, { turnsOn })) {
      return confirm(firstPublishConfirm({
        t, minutes, nodes,
        seconds: settings.data?.effective.dest_policy_apply_min_seconds ?? null,
      }))
    }
    if (!turnsOn || !subject || subject.state === 'off' || opts?.asked) return true
    return confirm(enablePolicyConfirm(
      { t, minutes, nodes },
      { name: subject.name, action: subject.action, state: subject.state },
      scopeOf(subject),
    ))
  }
}
