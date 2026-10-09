import type { TFunction } from 'i18next'
import type { ConfirmOpts } from '@/components/ConfirmHost'
import type { DestinationPoliciesView, DestinationStatus } from '@/api/accessControl'

const P = 'admin:access_control.confirm.'
function applyETACopy(t: TFunction, etaMs?: number): string {
  return etaMs ? t(`${P}eta_minutes`, { minutes: Math.ceil(etaMs / 60000) }) : t(`${P}eta_unknown`)
}
export function deletePolicyCopy(t: TFunction, name: string, etaMs?: number): ConfirmOpts {
  return { title: t('admin:access_control.policies.delete_title', { name }), message: t('admin:access_control.policies.delete_message', { eta: applyETACopy(t, etaMs) }), confirmText: t('common:actions.delete'), destructive: true }
}
export function switchListKindCopy(t: TFunction): ConfirmOpts {
  return { title: t('admin:access_control.list_editor.switch_title'), message: t('admin:access_control.list_editor.switch_message'), confirmText: t('admin:access_control.list_editor.switch_action') }
}
export function pauseExecutionCopy(t: TFunction, paused: boolean, etaMs?: number): ConfirmOpts {
  const action = paused ? 'pause' : 'resume'
  return { title: t(`${P}${action}_title`), message: t(`${P}${action}_message`, { eta: applyETACopy(t, etaMs) }), confirmText: t(`admin:access_control.${action}`), destructive: paused }
}
export function cancelExemptionCopy(t: TFunction, upn: string, etaMs?: number): ConfirmOpts {
  return { title: t('admin:access_control.exemptions.cancel_title', { upn }), message: t('admin:access_control.exemptions.cancel_message', { eta: applyETACopy(t, etaMs) }), confirmText: t('admin:access_control.exemptions.cancel') }
}
export function deleteListCopy(t: TFunction, name: string): ConfirmOpts {
  return { title: t('admin:access_control.lists.delete_title', { name }), message: t('admin:access_control.lists.delete_message'), confirmText: t('common:actions.delete'), destructive: true }
}
export function listInUseCopy(t: TFunction): ConfirmOpts {
  return { title: t('admin:access_control.lists.in_use_title'), message: t('admin:access_control.lists.in_use_message'), confirmText: t('common:actions.close') }
}
export function discardSettingsCopy(t: TFunction): ConfirmOpts {
  return { title: t(`${P}discard_title`), message: t(`${P}discard_message`), confirmText: t(`${P}discard_action`), cancelText: t(`${P}continue_editing`) }
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
    eta: applyETACopy(t, status?.apply_eta_ms),
  }), confirmText: t(`${P}first_action`) }
}
