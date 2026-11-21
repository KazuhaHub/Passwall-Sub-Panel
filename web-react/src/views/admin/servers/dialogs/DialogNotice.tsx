import type { ReactNode } from 'react'
import { IconError, IconInfo, IconSuccess, IconWarning, type IconType } from '@/lib/icons'
import { TONE_STYLE, type Tone } from '@/components/common/tones'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { cn } from '@/lib/utils'

const ICON: Partial<Record<Tone, IconType>> = { info: IconInfo, success: IconSuccess, warning: IconWarning, danger: IconError }

/**
 * A sentence a dialog says to the person, in a tone: the tint carries the hue, the icon carries the meaning, so a
 * tone is never colour alone. Only a failure is announced as an alert; the others are a polite status.
 */
export function DialogNotice({ tone = 'info', children, className, testId }: { tone?: Exclude<Tone, 'neutral'>; children: ReactNode; className?: string; testId?: string }) {
  const style = TONE_STYLE[tone]
  const Icon = ICON[tone]!
  return (
    <Alert role={tone === 'danger' ? 'alert' : 'status'} data-testid={testId} className={cn('border-transparent ring-1 ring-inset', style.tint, style.ring, className)}>
      <Icon aria-hidden className={style.ink} />
      <AlertDescription className="text-foreground">{children}</AlertDescription>
    </Alert>
  )
}
