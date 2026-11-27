import { forwardRef, lazy, Suspense } from 'react'
import { Skeleton } from '@/components/ui/skeleton'
import type { CodeEditorHandle, CodeEditorProps } from './CodeEditor'

// CodeMirror is heavy, so it is fetched when the first editor opens, not with the app. Call sites that
// only need an editor in a form use this instead of repeating lazy() and a fallback each time.
const CodeEditor = lazy(() => import('./CodeEditor'))

/** The editor, loaded on demand. Props and ref are CodeEditor's; `fallbackHeight` sizes the placeholder while it loads. */
export const LazyCodeEditor = forwardRef<CodeEditorHandle, CodeEditorProps & { fallbackHeight?: string }>(function LazyCodeEditor({ fallbackHeight, ...props }, ref) {
  const rows = props.minRows ?? props.maxRows
  const h = props.height ?? fallbackHeight ?? (rows ? `${rows * 20 + 8}px` : '380px')
  return (
    <Suspense fallback={<Skeleton className="rounded-lg" style={{ height: h }} />}>
      <CodeEditor ref={ref} {...props} />
    </Suspense>
  )
})
