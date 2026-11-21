import type { ReactNode } from 'react'
import { IconError, IconInfo, IconWarning } from '@/lib/icons'
import { TONE_STYLE, type Tone } from '@/components/common/tones'
import { cn } from '@/lib/utils'

const ICON = { warning: IconWarning, danger: IconError, info: IconInfo } as const

/**
 * A sentence that belongs to one part of a page and is not an error state of the whole page: an unsupported
 * server, a withheld shared port, a published-nothing warning. The icon and the words carry the meaning, the
 * tint only echoes it (the same tones as StatusBadge). `action` is the one thing the reader can do about it.
 */
export function Notice({ tone = 'warning', children, action, className, role = 'status' }: {
  tone?: keyof typeof ICON & Tone
  children: ReactNode
  action?: ReactNode
  className?: string
  role?: 'status' | 'alert'
}) {
  const style = TONE_STYLE[tone]
  const Icon = ICON[tone]
  return (
    <div role={role} data-slot="notice" className={cn('flex flex-wrap items-start gap-x-3 gap-y-2 rounded-lg px-3 py-2 text-sm text-foreground ring-1 ring-inset', style.tint, style.ring, className)}>
      <Icon aria-hidden className={cn('mt-0.5 size-4 shrink-0', style.ink)} />
      <div className="min-w-0 flex-1">{children}</div>
      {action}
    </div>
  )
}
