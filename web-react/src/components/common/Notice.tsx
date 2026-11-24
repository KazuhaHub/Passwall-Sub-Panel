import type { ReactNode } from 'react'
import { IconError, IconInfo, IconSuccess, IconWarning, type IconType } from '@/lib/icons'
import { TONE_STYLE, type Tone } from './tones'
import { cn } from '@/lib/utils'

const ICON: Partial<Record<Tone, IconType>> = { info: IconInfo, success: IconSuccess, warning: IconWarning, danger: IconError }

/**
 * A toned sentence that belongs to one part of a page, a sheet or a dialog and is not an error state of the whole
 * page: an unsupported server, a withheld shared port, a failed step in a dialog. The icon and the words carry the
 * meaning, the tint only echoes it (the same tones as StatusBadge), so a tone is never colour alone. `action` is the
 * one thing the reader can do about it. A failure (`danger`) is announced as an alert by default; every other tone is
 * a polite status. For a whole-page failure use ErrorState, for a banner under the header use Alert.
 */
export function Notice({ tone = 'warning', children, action, className, role, testId }: {
  tone?: Exclude<Tone, 'neutral'>
  children: ReactNode
  action?: ReactNode
  className?: string
  role?: 'status' | 'alert'
  testId?: string
}) {
  const style = TONE_STYLE[tone]
  const Icon = ICON[tone]!
  return (
    <div role={role ?? (tone === 'danger' ? 'alert' : 'status')} data-slot="notice" data-testid={testId}
      className={cn('flex flex-wrap items-start gap-x-3 gap-y-2 rounded-lg px-3 py-2 text-sm text-foreground ring-1 ring-inset', style.tint, style.ring, className)}>
      <Icon aria-hidden className={cn('mt-0.5 size-4 shrink-0', style.ink)} />
      <div className="min-w-0 flex-1">{children}</div>
      {action}
    </div>
  )
}
