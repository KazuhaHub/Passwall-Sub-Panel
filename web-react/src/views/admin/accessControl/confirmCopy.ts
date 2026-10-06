import type { TFunction } from 'i18next'
import type { ConfirmOpts } from '@/components/ConfirmHost'

const P = 'admin:access_control.confirm.'
export function discardSettingsCopy(t: TFunction): ConfirmOpts {
  return { title: t(`${P}discard_title`), message: t(`${P}discard_message`), confirmText: t(`${P}discard_action`) }
}
export function shortenRetentionCopy(t: TFunction, days: number): ConfirmOpts {
  return { title: t(`${P}shorten_title`), message: t(`${P}shorten_message`, { days }), confirmText: t(`${P}shorten_action`), destructive: true }
}
