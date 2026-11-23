import type { ConfirmOpts } from '@/components/ConfirmHost'
import { minutesWords } from '../confirmCopy'

type T = (key: string, opts?: Record<string, unknown>) => string

const P = 'admin:access_control.confirm.enable_policy.'

/** Who a policy reaches, as the confirmation names it. `users` is null when it cannot be counted (a policy scoped to servers). */
export interface EnableScope {
  scope: 'all' | 'groups' | 'panels'
  groups: number
  users: number | null
  panels: number
  /** The groups' or servers' names, for a scoped policy (shortened by the caller). */
  names: string
}

/**
 * Switching a policy on: what it will start to do and to whom, before it does. The scope is named in
 * numbers (groups, users, servers) because "enable" alone does not say that a block policy for everyone
 * disconnects matching traffic for every account. Focus starts on Cancel. Asked in place of nothing: a
 * toggle used to write at once; the first-ever policy still has its own, longer first-publish question.
 */
export function enablePolicyConfirm(
  { t, minutes, nodes }: { t: T; minutes: number | null; nodes: number | null },
  policy: { name: string; action: 'allow' | 'block'; state: 'report' | 'on' },
  s: EnableScope,
): ConfirmOpts {
  const when = minutesWords(t, minutes)
  const nodeWords = nodes === null ? t('admin:access_control.confirm.nodes_unknown') : t('admin:access_control.confirm.nodes', { count: nodes })
  const effect = t(`${P}effect_${policy.state === 'report' ? 'report' : policy.action}`)
  const message = s.scope === 'all'
    ? t(`${P}message_all`, { effect, groups: s.groups, users: s.users ?? 0, nodes: nodeWords, minutes: when })
    : s.scope === 'groups'
      ? t(`${P}message_groups`, { effect, groups: s.groups, users: s.users ?? 0, names: s.names, nodes: nodeWords, minutes: when })
      : t(`${P}message_panels`, { effect, panels: s.panels, names: s.names, minutes: when })
  return {
    title: t(`${P}title`, { name: policy.name }),
    message,
    confirmText: t(`${P}action_${policy.state}`),
    initialFocus: 'cancel',
  }
}
