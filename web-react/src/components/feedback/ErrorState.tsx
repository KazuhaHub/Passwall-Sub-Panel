import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { IconError, IconRefresh } from '@/lib/icons'

import { AsyncButton } from '@/components/common/AsyncButton'
import { cn } from '@/lib/utils'

/** The words for a thrown value: an Error's message, a string, or an API error body. */
export function describeError(reason: unknown): string | undefined {
  if (!reason) return undefined
  if (typeof reason === 'string') return reason
  const r = reason as { response?: { data?: { error?: unknown } }; message?: unknown }
  const api = r.response?.data?.error
  if (typeof api === 'string' && api) return api
  return typeof r.message === 'string' && r.message ? r.message : undefined
}

/**
 * THE ERROR STATE (plan 3.4): what went wrong, in the server's words when it gave
 * any, what to do next, and a retry that shows its own wait. "Retry" is the
 * action, not a link to documentation; the hint line says what to try when retry
 * does not help. `retry` may return a promise: the button is busy until it settles.
 */
export function ErrorState({ title, reason, hint, retry, compact, className, children }: {
  title?: string
  /** The cause: an Error, a string or an API error. Shown as-is, in small type. */
  reason?: unknown
  /** Overrides the default next-step line. */
  hint?: string
  retry?: () => unknown
  compact?: boolean
  className?: string
  children?: ReactNode
}) {
  const { t } = useTranslation('common')
  const cause = describeError(reason)
  return (
    <div
      role="alert"
      data-slot="error-state"
      className={cn(
        'flex flex-col items-center justify-center gap-2 text-center',
        compact ? 'px-4 py-8' : 'rounded-xl border border-dashed border-destructive/30 px-6 py-14',
        className,
      )}
    >
      <div aria-hidden className="mb-1 flex size-12 items-center justify-center rounded-full bg-destructive/10 text-destructive">
        <IconError className="size-6" />
      </div>
      <div className="text-base font-medium">{title ?? t('ui.feedback.error_title')}</div>
      {cause && <p className="max-w-md font-mono text-xs break-words text-muted-foreground">{cause}</p>}
      <p className="max-w-md text-sm text-muted-foreground">{hint ?? t('ui.feedback.error_hint')}</p>
      {children}
      {retry && (
        <AsyncButton variant="outline" className="mt-2" icon={<IconRefresh />} onClick={retry}>
          {t('actions.retry')}
        </AsyncButton>
      )}
    </div>
  )
}
