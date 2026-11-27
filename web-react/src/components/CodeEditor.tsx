import { forwardRef, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react'
import CodeMirror from '@uiw/react-codemirror'
import { EditorView } from '@codemirror/view'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { cn } from '@/lib/utils'
import { resolveEffectiveMode, useAppearanceStore } from '@/stores/appearance'
import { languageExtensions, type EditorLanguage } from './codeEditor/languages'
import { templatePlaceholders } from './codeEditor/placeholders'
import { vscodeTheme } from './codeEditor/theme'

export type { EditorLanguage } from './codeEditor/languages'

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

export interface CodeEditorProps {
  value: string
  onChange: (next: string) => void
  height?: string
  /** A height in lines, for an editor that has no height of its own. With `maxRows` the editor grows between the two. */
  minRows?: number
  maxRows?: number
  readOnly?: boolean
  /** Defaults to the theme's mode. */
  dark?: boolean
  /** What the text is, so it can be coloured: 'yaml' (the default, for the old callers), 'json', 'markdown', 'pem', or
   *  'plain' for a list (no grammar: `domain:example.com` is not a YAML key). `{{ placeholder }}` is coloured in all of them. */
  language?: EditorLanguage
  /** The accessible name of the text area (the editor has no <label> of its own). */
  ariaLabel?: string
  /** The id of the element that names it. Not needed inside a Field: the label that points at `id` is found. */
  labelledBy?: string
  /** Wired by `Field` (it clones `id`, `aria-describedby`, `aria-invalid`, `aria-required` onto its control). */
  id?: string
  'aria-describedby'?: string
  'aria-invalid'?: boolean | 'true' | 'false'
  'aria-required'?: boolean | 'true' | 'false'
  /** For a box of list entries or a PEM block, where indentation means nothing: Tab moves focus on, as in a text area,
   *  so the box is no keyboard trap and needs no hint. Off (Tab indents) by default, because YAML and JSON need it. */
  tabMovesFocus?: boolean
  /** Not editable and dimmed, for a field the person may not change right now (a save is running, a role forbids it). */
  disabled?: boolean
  /** Focus left the editor: the page validates on blur. */
  onBlur?: () => void
  /** Wrap long lines instead of scrolling sideways: for commands, PEM and prose. Off for rule sets, where a line is a rule and
   *  wrapping a 9k-line document would make every height measurement dearer. */
  wrap?: boolean
  /** Shown while the text is empty; line breaks are kept. */
  placeholder?: string
  className?: string
}

// CodeEditor wraps CodeMirror 6 for every place an admin edits source text: rule sets, templates, language packs, JSON,
// PEM blocks, lists. One editor so that all of them read the same: VS Code's Dark+ / Light+ colours (see
// codeEditor/vscodePalette.ts), line numbers, the app's mono font, and the keyboard behaviour below. CM6
// virtualizes the document, so it stays smooth on multi-thousand-line lists where an autosizing <textarea> janks
// on every keystroke.
//
// KEYBOARD: Tab indents (the rule lists are YAML, where indentation is the syntax), which is
// @uiw/react-codemirror's default. CodeMirror itself provides the way out, so Tab never traps a
// keyboard user: press Escape, then Tab within two seconds to move focus on (Shift+Tab to move
// back), or Ctrl+M (Shift+Alt+M on macOS) to switch Tab between indenting and moving focus. Pages
// that show an editor say so beside it (admin:rules.editor_keyboard_hint). Do not turn
// `indentWithTab` off to "fix" this: that removes indenting for everyone.
// This module pulls
// in the heavy CM deps, so consumers should React.lazy() it (LazyCodeEditor does) — that keeps it
// out of the initial SPA bundle (loaded only when an editor opens). Default
// export for lazy().
const CodeEditor = forwardRef<CodeEditorHandle, CodeEditorProps>(function CodeEditor({
  value, onChange, height, minRows, maxRows, readOnly = false, dark, language = 'yaml', ariaLabel, labelledBy, id, placeholder, className, tabMovesFocus, wrap, disabled = false, onBlur,
  'aria-describedby': describedBy, 'aria-invalid': invalid, 'aria-required': required,
}, ref) {
  // The concrete mode, resolved the way App.tsx does for the page (the stored choice, 'auto' following the OS).
  const stored = useAppearanceStore(s => s.mode)
  const systemDark = useMediaQuery('(prefers-color-scheme: dark)')
  const mode = resolveEffectiveMode(stored, systemDark)
  const isDark = dark ?? mode === 'dark'
  const view = useRef<EditorView | null>(null)
  // The editor's focusable element is a contenteditable <div>, which a <label for> does not name. A Field
  // (or any <label for={id}>) around it is found here and wired through aria-labelledby instead.
  const [labelFor, setLabelFor] = useState<string | undefined>()
  useEffect(() => {
    if (!id || labelledBy || ariaLabel) { setLabelFor(undefined); return }
    const label = document.querySelector<HTMLElement>(`label[for="${CSS.escape(id)}"]`)
    setLabelFor(label?.id || undefined)
  }, [id, labelledBy, ariaLabel])
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

  const fixed = height ?? (minRows && !maxRows ? `${minRows * ROW_PX + CHROME_PX}px` : maxRows ? undefined : '380px')
  const h = fixed
  const minH = !fixed && minRows ? `${minRows * ROW_PX + CHROME_PX}px` : undefined
  const maxH = !fixed && maxRows ? `${maxRows * ROW_PX + CHROME_PX}px` : undefined

  // Rebuilt only when what they describe changes, or CodeMirror would reconfigure on every render.
  const extensions = useMemo(() => [
    ...languageExtensions(language),
    templatePlaceholders,
    ...(wrap ? [EditorView.lineWrapping] : []),
    EditorView.contentAttributes.of({
      ...(ariaLabel ? { 'aria-label': ariaLabel } : {}),
      ...(labelledBy ?? labelFor ? { 'aria-labelledby': labelledBy ?? labelFor } : {}),
      ...(id ? { id } : {}),
      ...(describedBy ? { 'aria-describedby': describedBy } : {}),
      ...(invalid && invalid !== 'false' ? { 'aria-invalid': 'true' } : {}),
      ...(required && required !== 'false' ? { 'aria-required': 'true' } : {}),
      ...(disabled ? { 'aria-disabled': 'true' } : {}),
      spellcheck: 'false',
      autocapitalize: 'off',
    }),
  ], [language, wrap, disabled, ariaLabel, labelledBy, labelFor, id, describedBy, invalid, required])

  return (
    <CodeMirror
      value={value}
      height={h}
      minHeight={minH}
      maxHeight={maxH}
      theme={vscodeTheme(isDark)}
      // Not `editable={false}`: a read-only value stays focusable, selectable and copyable from the keyboard, like a readonly textarea.
      readOnly={readOnly || disabled}
      onBlur={onBlur}
      placeholder={placeholder}
      indentWithTab={tabMovesFocus ? false : undefined}
      extensions={extensions}
      onChange={onChange}
      onCreateEditor={v => { view.current = v }}
      basicSetup={{
        lineNumbers: true,
        foldGutter: false,
        highlightActiveLine: !readOnly && !disabled,
        highlightActiveLineGutter: !readOnly && !disabled,
        autocompletion: false,
        // A list, a PEM block or prose has no brackets worth pairing; typing a quote must not insert two.
        closeBrackets: language === 'yaml' || language === 'json',
      }}
      className={cn(
        'overflow-hidden rounded-lg border border-input transition-colors focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50',
        disabled && 'opacity-60',
        invalid && invalid !== 'false' && 'border-destructive focus-within:border-destructive focus-within:ring-destructive/30',
        className,
      )}
      style={{ height: h, minHeight: minH, maxHeight: maxH }}
    />
  )
})

export default CodeEditor
