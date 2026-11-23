import type { ConfirmOpts } from '@/components/ConfirmHost'
import { minutesWords } from '../confirmCopy'

type T = (key: string, opts?: Record<string, unknown>) => string

const P = 'admin:access_control.confirm.enable_policy.'
/** The last character is Chinese (or full-width): no space follows it. */
const CJK = /[\u3000-\u9fff\uff00-\uffef]$/

/** Who a policy reaches, as the confirmation names it. `users` is null when it cannot be counted (a policy scoped to servers). */
export interface EnableScope {
  scope: 'all' | 'groups' | 'panels'
  groups: number
  users: number | null
  panels: number
  /** The groups' or servers' names, for a scoped policy (shortened by the caller). */
  names: string
  /** Exempt accounts, which the block and report-only rules skip. */
  exempt: number
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
  const exempt = action === 'block' && s.exempt > 0 ? t(`${P}exempt`, { count: s.exempt }) : ''
  // A space between sentences in a spaced script, none between Chinese ones.
  const gap = (text: string) => (CJK.test(text) ? '' : ' ')
  const scope = s.scope === 'all'
    ? t(`${P}scope_all`, { groups: s.groups, users: s.users ?? 0 })
    : s.scope === 'groups'
      ? t(`${P}scope_groups`, { groups: s.groups, users: s.users ?? 0, names: s.names })
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
