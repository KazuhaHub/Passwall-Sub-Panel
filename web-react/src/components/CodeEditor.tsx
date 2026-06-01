import CodeMirror from '@uiw/react-codemirror'
import { yaml } from '@codemirror/lang-yaml'

// CodeEditor wraps CodeMirror 6 for editing rule-set content (Clash/Mihomo
// YAML payloads). CM6 virtualizes the document, so it stays smooth on
// multi-thousand-line rule lists where an autosizing <textarea> janks on every
// keystroke. This module pulls in the heavy CM deps, so consumers should
// React.lazy() it — that keeps it out of the initial SPA bundle (loaded only
// when a rule-set editor opens). Default export for lazy().
export default function CodeEditor({
  value,
  onChange,
  height = '380px',
  readOnly = false,
  dark = false,
}: {
  value: string
  onChange: (next: string) => void
  height?: string
  readOnly?: boolean
  dark?: boolean
}) {
  return (
    <CodeMirror
      value={value}
      height={height}
      theme={dark ? 'dark' : 'light'}
      editable={!readOnly}
      readOnly={readOnly}
      extensions={[yaml()]}
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
}
