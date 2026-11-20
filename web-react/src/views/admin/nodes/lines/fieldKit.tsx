import type { ComponentProps, ReactNode } from 'react'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import type { PreviewHint } from '@/hooks/useDraftPreview'
import type { LineDraft, LineField } from './lineModel'

/** What every group of fields of the editor is given. */
export interface FieldsProps {
  draft: LineDraft
  patch: (p: Partial<LineDraft>) => void
  /** The message of a field's problem once it may be shown (after it was left, or after a save was tried). */
  error: (field: LineField) => ReactNode | undefined
  blur: (field: LineField) => void
}

/** The ids the controls carry, so the save bar can take a person to the field that blocks it. */
export const ID = {
  name: 'line-name', entry: 'line-entry', address: 'line-address', port: 'line-port', domain: 'line-domain', factor: 'line-factor', region: 'line-region',
} as const

/** A port as digits only; 0 (an empty box) is "use the landing's port" or "the default", which each field's hint says. */
export function PortInput({ value, onChange, className, ...rest }: { value: number; onChange: (port: number) => void } & Omit<ComponentProps<typeof Input>, 'value' | 'onChange' | 'type'>) {
  return (
    <Input {...rest} inputMode="numeric" autoComplete="off" value={value ? String(value) : ''} className={cn('tabular-nums', className)}
      onChange={e => onChange(Number(e.target.value.replace(/\D/g, '')) || 0)} />
  )
}

/** The one line an edit-time preview shows: the matched items, "none" as a warning, or "cannot be determined". Nothing while the first answer is pending. */
export function PreviewNote({ hint, ready, none, unknown, testId }: { hint?: PreviewHint; ready: (items: string[]) => string; none: string; unknown: string; testId: string }) {
  if (!hint || hint.state === 'loading') return null
  const empty = hint.state === 'ready' && hint.items.length === 0
  return <p data-testid={testId} className={cn('text-xs', empty ? 'text-destructive' : 'text-muted-foreground')}>{hint.state === 'error' ? unknown : empty ? none : ready(hint.items)}</p>
}
