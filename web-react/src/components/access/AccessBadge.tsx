import { StatusDot } from '@/components/common/StatusDot'
import { TONE_STYLE } from '@/components/common/tones'
import { cn } from '@/lib/utils'
import type { AccessToneKind } from '@/utils/accessControl'
import { accessStatus } from './accessStatus'

/**
 * One access-control status: its icon and its words, from the closed table in accessStatus.ts.
 * Rows that need no attention (ok, paused, nothing recorded) render as a dot and plain words; the
 * rest are a tinted badge with the row's own icon. The words stay in the foreground colour; only the
 * icon and the dot carry the hue, so contrast does not depend on a tint.
 */
export function AccessBadge({ kind, label, testId, className }: {
  kind: AccessToneKind
  label: string
  testId?: string
  className?: string
}) {
  const s = accessStatus(kind)
  if (s.quiet) {
    return <StatusDot tone={s.tone} label={label} className={cn('text-foreground', className)} />
  }
  const style = TONE_STYLE[s.tone]
  const Icon = s.icon
  return (
    <span
      data-slot="access-badge"
      data-kind={kind}
      data-testid={testId}
      className={cn(
        'inline-flex w-fit max-w-full shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-xs/5 font-medium whitespace-nowrap text-foreground ring-1 ring-inset',
        style.tint, style.ring, className,
      )}
    >
      <Icon aria-hidden className={cn('size-3.5 shrink-0', s.iconClass ?? style.ink)} />
      <span className="min-w-0 truncate">{label}</span>
    </span>
  )
}
