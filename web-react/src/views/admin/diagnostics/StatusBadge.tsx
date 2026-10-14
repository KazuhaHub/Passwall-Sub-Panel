import { useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { ToneBadge, severityTone, stateTone } from '@/components/ToneBadge'
import type { CardState, Severity } from '@/utils/diagnostics'

// Compatibility exports for existing diagnostics consumers during UI-0.
export { amber, severityTone, stateTone, type Tone } from '@/components/ToneBadge'

/** One area card's state: icon, words and colour from one closed table. */
export function CardStateBadge({ state }: { state: CardState }) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  return <ToneBadge tone={stateTone(theme, state)} label={t(`admin:diagnostics.state.${state}`)} testId="state-badge" data={state} />
}

/** A finding's severity, in the same colours as the card states. */
export function SeverityBadge({ severity }: { severity: Severity }) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  return <ToneBadge tone={severityTone(theme, severity)} label={t(`admin:diagnostics.severity.${severity}`)} testId="severity-badge" data={severity} />
}
