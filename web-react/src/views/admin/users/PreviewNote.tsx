import type { PreviewHint } from '@/hooks/useDraftPreview'

/** The one line an edit-time preview shows: the matched items, "none" as a note, or "cannot be determined". Nothing while the first answer is pending, so an empty state never flashes. */
export function PreviewNote({ hint, ready, none, unknown, testId }: {
  hint?: PreviewHint
  ready: (items: string[]) => string
  none: string
  unknown: string
  testId: string
}) {
  if (!hint || hint.state === 'loading') return null
  const empty = hint.state === 'ready' && hint.items.length === 0
  return <p data-testid={testId} className="text-xs text-muted-foreground">{hint.state === 'error' ? unknown : empty ? none : ready(hint.items)}</p>
}
