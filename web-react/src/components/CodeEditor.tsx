import { forwardRef, useImperativeHandle, useMemo, useRef } from 'react'
import { useTheme } from '@mui/material'
import CodeMirror, { type ReactCodeMirrorRef } from '@uiw/react-codemirror'
import { yaml } from '@codemirror/lang-yaml'

// CodeEditor wraps CodeMirror 6 for editing rule-set content (Clash/Mihomo
// YAML payloads). CM6 virtualizes the document, so it stays smooth on
// multi-thousand-line rule lists where an autosizing <textarea> janks on every
// keystroke. This module pulls in the heavy CM deps, so consumers should
// React.lazy() it — that keeps it out of the initial SPA bundle (loaded only
// when a rule-set editor opens). Default export for lazy().
export interface CodeEditorHandle { revealLine(n: number): void }

const CodeEditor = forwardRef<CodeEditorHandle, {
  value: string
  onChange: (next: string) => void
  height?: string
  readOnly?: boolean
  dark?: boolean
  language?: 'yaml' | 'plain' | 'markdown'
  minRows?: number
}>(function CodeEditor({
  value,
  onChange,
  height,
  readOnly = false,
  dark,
  language = 'yaml',
  minRows,
}, ref) {
  const theme = useTheme()
  const editor = useRef<ReactCodeMirrorRef>(null)
  // Markdown uses plain text until its separate language extension is added.
  const extensions = useMemo(() => language === 'yaml' ? [yaml()] : [], [language])
  useImperativeHandle(ref, () => ({ revealLine(n) {
    const view = editor.current?.view
    if (!view || !Number.isFinite(n)) return
    const line = view.state.doc.line(Math.max(1, Math.min(view.state.doc.lines, Math.trunc(n))))
    view.dispatch({ selection: { anchor: line.from, head: line.to }, scrollIntoView: true })
    view.focus()
  } }), [])
  return (
    <CodeMirror
      ref={editor}
      value={value}
      height={height ?? (minRows != null && Number.isFinite(minRows) ? `${Math.max(1, Math.trunc(minRows)) * 20 + 16}px` : '380px')}
      theme={(dark ?? (theme.palette.mode === 'dark')) ? 'dark' : 'light'}
      editable={!readOnly}
      readOnly={readOnly}
      extensions={extensions}
      onChange={onChange}
      basicSetup={{
        lineNumbers: true,
        foldGutter: false,
        highlightActiveLine: !readOnly,
        autocompletion: false,
      }}
      style={{ fontSize: 13, borderRadius: 8, overflow: 'hidden' }}
    />
  )
})

export default CodeEditor
