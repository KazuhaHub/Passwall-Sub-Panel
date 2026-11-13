import { forwardRef, useImperativeHandle, useRef } from 'react'
import CodeMirror from '@uiw/react-codemirror'
import { yaml } from '@codemirror/lang-yaml'
import type { EditorView } from '@codemirror/view'
import { useTheme } from '@mui/material'

/** What a parent can ask of the editor. */
export interface CodeEditorHandle {
  /** Scrolls to line `n` (1-based) and selects it. An out-of-range line does nothing. */
  revealLine: (n: number) => void
}

// One CodeMirror line at the 13px font: the height `minRows` is worth.
const ROW_PX = 20
const CHROME_PX = 8

// CodeEditor wraps CodeMirror 6 for editing rule-set content (Clash/Mihomo
// YAML payloads) and, with language="plain", the access-control lists. CM6
// virtualizes the document, so it stays smooth on multi-thousand-line lists
// where an autosizing <textarea> janks on every keystroke. This module pulls
// in the heavy CM deps, so consumers should React.lazy() it — that keeps it
// out of the initial SPA bundle (loaded only when an editor opens). Default
// export for lazy().
const CodeEditor = forwardRef<CodeEditorHandle, {
  value: string
  onChange: (next: string) => void
  height?: string
  /** A height in lines, for an editor that has no height of its own. */
  minRows?: number
  readOnly?: boolean
  /** Defaults to the theme's mode. */
  dark?: boolean
  /** 'yaml' (the default, for the old callers) or 'plain': no language, so
   *  `domain:example.com` is not drawn as a YAML key. */
  language?: 'yaml' | 'plain'
}>(function CodeEditor({ value, onChange, height, minRows, readOnly = false, dark, language = 'yaml' }, ref) {
  const mode = useTheme().palette.mode
  const view = useRef<EditorView | null>(null)
  useImperativeHandle(ref, () => ({
    revealLine(n: number) {
      const v = view.current
      if (!v || !Number.isInteger(n) || n < 1 || n > v.state.doc.lines) return
      const line = v.state.doc.line(n)
      v.dispatch({ selection: { anchor: line.from, head: line.to }, scrollIntoView: true })
      v.focus()
    },
  }), [])
  const h = height ?? (minRows ? `${minRows * ROW_PX + CHROME_PX}px` : '380px')
  return (
    <CodeMirror
      value={value}
      height={h}
      theme={(dark ?? mode === 'dark') ? 'dark' : 'light'}
      editable={!readOnly}
      readOnly={readOnly}
      extensions={language === 'yaml' ? [yaml()] : []}
      onChange={onChange}
      onCreateEditor={v => { view.current = v }}
      basicSetup={{
        lineNumbers: true,
        foldGutter: false,
        highlightActiveLine: !readOnly,
        autocompletion: false,
      }}
      style={{ height: h, fontSize: 13, borderRadius: 8, overflow: 'hidden' }}
    />
  )
})

export default CodeEditor
