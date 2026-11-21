import { forwardRef, useImperativeHandle, useRef } from 'react'
import CodeMirror from '@uiw/react-codemirror'
import { yaml } from '@codemirror/lang-yaml'
import { EditorView } from '@codemirror/view'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { resolveEffectiveMode, useAppearanceStore } from '@/stores/appearance'

/** What a parent can ask of the editor. */
export interface CodeEditorHandle {
  /** Scrolls to line `n` (1-based) and selects it. An out-of-range line does nothing. */
  revealLine: (n: number) => void
  /** Puts `text` on a line of its own at the cursor: in place of an empty cursor
   *  line, else on a new line after the cursor's line. */
  insertLine: (text: string) => void
}

// One CodeMirror line at the 13px font: the height `minRows` is worth.
const ROW_PX = 20
const CHROME_PX = 8

// CodeEditor wraps CodeMirror 6 for editing rule-set content (Clash/Mihomo
// YAML payloads) and, with language="plain", the access-control lists. CM6
// virtualizes the document, so it stays smooth on multi-thousand-line lists
// where an autosizing <textarea> janks on every keystroke.
//
// KEYBOARD: Tab indents (the rule lists are YAML, where indentation is the syntax), which is
// @uiw/react-codemirror's default. CodeMirror itself provides the way out, so Tab never traps a
// keyboard user: press Escape, then Tab within two seconds to move focus on (Shift+Tab to move
// back), or Ctrl+M (Shift+Alt+M on macOS) to switch Tab between indenting and moving focus. Pages
// that show an editor say so beside it (admin:rules.editor_keyboard_hint). Do not turn
// `indentWithTab` off to "fix" this: that removes indenting for everyone.
// This module pulls
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
  /** The accessible name of the text area (the editor has no <label> of its own). */
  ariaLabel?: string
}>(function CodeEditor({ value, onChange, height, minRows, readOnly = false, dark, language = 'yaml', ariaLabel }, ref) {
  // The concrete mode, resolved the way App.tsx does for the page (the stored choice, 'auto' following the OS).
  const stored = useAppearanceStore(s => s.mode)
  const systemDark = useMediaQuery('(prefers-color-scheme: dark)')
  const mode = resolveEffectiveMode(stored, systemDark)
  const view = useRef<EditorView | null>(null)
  useImperativeHandle(ref, () => ({
    revealLine(n: number) {
      const v = view.current
      if (!v || !Number.isInteger(n) || n < 1 || n > v.state.doc.lines) return
      const line = v.state.doc.line(n)
      v.dispatch({ selection: { anchor: line.from, head: line.to }, scrollIntoView: true })
      v.focus()
    },
    insertLine(text: string) {
      const v = view.current
      if (!v) return
      const line = v.state.doc.lineAt(v.state.selection.main.head)
      const blank = line.text.trim() === ''
      const insert = blank ? text : `\n${text}`
      const from = blank ? line.from : line.to
      v.dispatch({ changes: { from, to: line.to, insert }, selection: { anchor: from + insert.length }, scrollIntoView: true })
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
      extensions={[
        ...(language === 'yaml' ? [yaml()] : []),
        ...(ariaLabel ? [EditorView.contentAttributes.of({ 'aria-label': ariaLabel })] : []),
      ]}
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
