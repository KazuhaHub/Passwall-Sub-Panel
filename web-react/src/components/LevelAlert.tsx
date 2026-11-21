import type { ReactNode } from 'react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { IconError, IconInfo, IconWarning } from '@/lib/icons'
import { cn } from '@/lib/utils'

export type AlertLevel = 'error' | 'warning' | 'info'

/**
 * A message with a level, for the findings of a validation (the rule-set inspector reports `error`, `warning`
 * and `info`). The level is always said by the icon AND the shape of the text colour, never by colour alone:
 * an error uses the destructive alert, the others the plain one with an amber or neutral icon.
 */
export function LevelAlert({ level, children, className }: { level: AlertLevel; children: ReactNode; className?: string }) {
  const Icon = level === 'error' ? IconError : level === 'warning' ? IconWarning : IconInfo
  return (
    <Alert variant={level === 'error' ? 'destructive' : 'default'} className={className}>
      <Icon aria-hidden className={cn(level === 'warning' && 'text-warning-ink')} />
      <AlertDescription className={cn(level !== 'error' && 'text-foreground')}>{children}</AlertDescription>
    </Alert>
  )
}
