import type { ConfirmOpts } from '@/components/ConfirmHost'
import { minutesWords } from '../confirmCopy'

type T = (key: string, opts?: Record<string, unknown>) => string

const P = 'admin:access_control.confirm.enable_policy.'
/** The last character is Chinese (or full-width): no space follows it. */
const CJK = /[\u3000-\u9fff\uff00-\uffef]$/

/** Who a policy reaches, as the confirmation names it. `users` is null when it cannot be counted (a policy scoped to servers). */
export interface EnableScope {
  scope: 'all' | 'groups' | 'panels'
  /** null: the groups have not been read (loading or failed), so no number is stated. */
  groups: number | null
  users: number | null
  panels: number
  /** The groups' or servers' names, for a scoped policy (shortened by the caller). */
  names: string
  /** Exempt accounts, which the block and report-only rules skip; null when the count could not be read. */
  exempt: number | null
}

/** The words naming who a policy reaches and what the nodes do about it: the tail of the enabling question. */
export function enableScopeText(
  { t, minutes, nodes }: { t: T; minutes: number | null; nodes: number | null },
  action: 'allow' | 'block',
  s: EnableScope,
): string {
  const when = minutesWords(t, minutes)
  const nodeWords = nodes === null ? t(`${P}nodes_unknown`) : t(`${P}nodes_up_to`, { count: nodes })
  // Exempt accounts skip the block and report-only rules (the exempt list sits between the allows and the blocks).
  // The count is of ALL exempt accounts, so for a policy that reaches only some of them it is said as a total.
  const exempt = action !== 'block' ? ''
    : s.exempt === null ? t(`${P}exempt_unknown`)
    : s.exempt === 0 ? ''
    : t(`${P}exempt_${s.scope}`, { count: s.exempt })
  // A space between sentences in a spaced script, none between Chinese ones.
  const gap = (text: string) => (CJK.test(text) ? '' : ' ')
  const known = s.groups !== null && s.users !== null
  const scope = s.scope === 'all'
    ? known ? t(`${P}scope_all`, { groups: s.groups, users: s.users }) : t(`${P}scope_all_unknown`)
    : s.scope === 'groups'
      ? known ? t(`${P}scope_groups`, { groups: s.groups, users: s.users, names: s.names }) : t(`${P}scope_groups_unknown`, { groups: s.groups ?? 0, names: s.names })
      : t(`${P}scope_panels`, { panels: s.panels, names: s.names })
  const head = exempt ? `${scope}${gap(scope)}${exempt}` : scope
  return `${head}${gap(head)}${t(`${P}restart`, { nodes: nodeWords, minutes: when })}`
}

/**
 * Switching a policy on: what it will start to do and to whom, before it does. The scope is named in
 * numbers (groups, users, nodes) because "enable" alone does not say that a block policy for everyone
 * disconnects matching traffic for every account. Focus starts on Cancel. Asked in place of nothing: a
 * toggle used to write at once; the first-ever policy still has its own, longer first-publish question.
 */
export function enablePolicyConfirm(
  args: { t: T; minutes: number | null; nodes: number | null },
  policy: { name: string; action: 'allow' | 'block'; state: 'report' | 'on' },
  s: EnableScope,
): ConfirmOpts {
  const { t } = args
  const effect = t(`${P}effect_${policy.state === 'report' ? 'report' : policy.action}`)
  return {
    title: t(`${P}title`, { name: policy.name }),
    // The effect is a clause, closed here: a full stop and a space in English, 。 in Chinese.
    message: `${effect}${CJK.test(effect) ? '\u3002' : '. '}${enableScopeText(args, policy.action, s)}`,
    confirmText: t(`${P}action_${policy.state}`),
    initialFocus: 'cancel',
  }
}
