import { useTranslation } from 'react-i18next'
import { StatusBadge } from '@/components/common/StatusBadge'
import type { Tone } from '@/components/common/tones'
import type { CardState, Severity, VerdictTone } from '@/utils/diagnostics'

// The tables that turn this page's own vocabularies into the shared tones.
// Every one of them also says its state in words: the shared badge is a quiet
// dot plus text for the calm tones and a tinted badge with an icon for the
// rest, so no state is told by colour alone. A healthy subsystem is therefore
// a dot and a word; only what needs a look is drawn loud.

export const STATE_TONE: Record<CardState, Tone> = {
  failing: 'danger',
  attention: 'warning',
  ok: 'success',
  idle: 'neutral',
  measuring: 'info',
  inhibited: 'neutral',
  not_applicable: 'neutral',
  none: 'neutral',
  recorded: 'neutral',
}

export const SEVERITY_TONE: Record<Severity, Tone> = {
  critical: 'danger',
  error: 'danger',
  warn: 'warning',
  notice: 'info',
}

export const VERDICT_TONE: Record<VerdictTone, Tone> = {
  critical: 'danger',
  action: 'danger',
  attention: 'warning',
  blackout: 'info',
  measuring: 'info',
  ok: 'success',
}

/** One subsystem's state. The wrapper carries the hooks tests and styles read. */
export function CardStateBadge({ state }: { state: CardState }) {
  const { t } = useTranslation(['admin'])
  return (
    <span data-testid="state-badge" data-state={state} className="inline-flex shrink-0">
      <StatusBadge tone={STATE_TONE[state]} label={t(`admin:diagnostics.state.${state}`)} />
    </span>
  )
}

/** A finding's severity. */
export function SeverityBadge({ severity }: { severity: Severity }) {
  const { t } = useTranslation(['admin'])
  return (
    <StatusBadge tone={SEVERITY_TONE[severity]} label={t(`admin:diagnostics.severity.${severity}`)}
      testId="severity-badge" />
  )
}
