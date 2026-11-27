import { forwardRef, useImperativeHandle } from 'react'
import { vi } from 'vitest'
import type { CodeEditorHandle, CodeEditorProps } from '@/components/CodeEditor'

/**
 * A text area standing in for the code editor in a test of a page that uses it: CodeMirror has no layout
 * in jsdom, and a test of a page is about its values, not about colours. It carries the same accessibility
 * wiring the real editor puts on its content element, so queries by label and role keep working.
 * It is installed for every test by setup.ts.
 */
export const CodeEditorStub = forwardRef<CodeEditorHandle, CodeEditorProps>(function CodeEditorStub(p, ref) {
  useImperativeHandle(ref, () => ({ revealLine: vi.fn(), insertLine: vi.fn() }))
  return (
    <textarea id={p.id} aria-label={p.ariaLabel} aria-labelledby={p.labelledBy} aria-describedby={p['aria-describedby']}
      aria-invalid={p['aria-invalid']} aria-required={p['aria-required']} placeholder={p.placeholder} readOnly={p.readOnly} disabled={p.disabled} onBlur={p.onBlur}
      data-language={p.language ?? 'yaml'} value={p.value} onChange={e => p.onChange(e.target.value)} />
  )
})

export const lazyCodeEditorMock = () => ({ LazyCodeEditor: CodeEditorStub })
