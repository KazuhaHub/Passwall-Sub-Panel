import type { TFunction } from 'i18next'
import type { ConfirmOpts } from '@/components/ConfirmHost'
import type { DestinationPoliciesView, DestinationStatus } from '@/api/accessControl'

const P = 'admin:access_control.confirm.'
export function discardSettingsCopy(t: TFunction): ConfirmOpts {
  return { title: t(`${P}discard_title`), message: t(`${P}discard_message`), confirmText: t(`${P}discard_action`) }
}
export function shortenRetentionCopy(t: TFunction, days: number): ConfirmOpts {
  return { title: t(`${P}shorten_title`), message: t(`${P}shorten_message`, { days }), confirmText: t(`${P}shorten_action`), destructive: true }
}
export function needsFirstPublishConfirm(policies: DestinationPoliciesView, _groups: readonly { mode?: string }[], change: { enabled?: boolean; mode?: 'open' | 'allowlist' }): boolean {
  // Only the selected published snapshot is evidence of prior enablement.
  return !policies.published_has_access_control && (change.enabled === true || change.mode === 'allowlist')
}
export function firstPublishCopy(t: TFunction, status: DestinationStatus | undefined, seconds: number | undefined): ConfirmOpts {
  const count = status?.nodes.filter(node => node.kind === 'psp' && node.supports.policy && !['offline', 'unsupported_version'].includes(node.state)).length
  return { title: t(`${P}first_title`), message: t(`${P}first_message`, {
    nodes: count === undefined ? t(`${P}each_node`) : t(`${P}node_count`, { count }),
    wait: seconds === undefined ? t(`${P}wait_unknown`) : t(`${P}wait_seconds`, { seconds }),
    eta: status?.apply_eta_ms ? t(`${P}eta_minutes`, { minutes: Math.ceil(status.apply_eta_ms / 60000) }) : t(`${P}eta_unknown`),
  }), confirmText: t(`${P}first_action`) }
}
