// @vitest-environment jsdom
import { createRef, type CSSProperties } from 'react'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import CodeEditor, { type CodeEditorHandle } from './CodeEditor'
import { vscodeTheme } from './codeEditor/theme'

const propsSpy = vi.hoisted(() => vi.fn())
vi.mock('@uiw/react-codemirror', () => ({
  default: (props: { height: string; style: CSSProperties }) => {
    propsSpy(props)
    return <div data-testid="code-mirror" data-height={props.height} style={props.style} />
  },
}))
afterEach(() => { cleanup(); propsSpy.mockClear() })

it.each([undefined, '100%'])('applies height %s to both the wrapper and CodeMirror', height => {
  const onChange = vi.fn()
  render(<CodeEditor value="- MATCH,DIRECT" onChange={onChange} height={height} />)
  const editor = screen.getByTestId('code-mirror')
  const expected = height || '380px'
  expect(editor.getAttribute('data-height')).toBe(expected)
  expect(editor.style.height).toBe(expected)
  const props = propsSpy.mock.calls.at(-1)![0]
  expect(props.value).toBe('- MATCH,DIRECT')
  props.onChange('- MATCH,REJECT')
  expect(onChange).toHaveBeenCalledWith('- MATCH,REJECT')
})

it('turns minRows into a height unless a height is given', () => {
  render(<CodeEditor value="" onChange={() => {}} language="plain" minRows={6} />)
  expect(screen.getByTestId('code-mirror').style.height).toBe('128px')
  cleanup()
  render(<CodeEditor value="" onChange={() => {}} language="plain" minRows={6} height="50px" />)
  expect(screen.getByTestId('code-mirror').style.height).toBe('50px')
})

// Tab indents (YAML needs it); CodeMirror's own Escape-then-Tab is the way out, and the pages say so.
// Turning indentWithTab off here would silently remove indenting from every editor.
it('leaves CodeMirror\'s Tab handling on', () => {
  render(<CodeEditor value="" onChange={() => {}} />)
  expect(propsSpy.mock.calls.at(-1)![0].indentWithTab).toBeUndefined()
})

it('follows the stored appearance mode when dark is not given', () => {
  render(<CodeEditor value="" onChange={() => {}} />)
  expect(propsSpy.mock.calls.at(-1)![0].theme).toBe(vscodeTheme(false))
  cleanup()
  render(<CodeEditor value="" onChange={() => {}} dark />)
  expect(propsSpy.mock.calls.at(-1)![0].theme).toBe(vscodeTheme(true))
})

it('reveals and selects a line through the ref', () => {
  const dispatch = vi.fn()
  const view = { state: { doc: { lines: 5, line: (n: number) => ({ from: n * 10, to: n * 10 + 4 }) } }, dispatch, focus: vi.fn() }
  const ref = createRef<CodeEditorHandle>()
  render(<CodeEditor ref={ref} value="" onChange={() => {}} language="plain" />)
  propsSpy.mock.calls.at(-1)![0].onCreateEditor(view)
  ref.current!.revealLine(3)
  expect(dispatch).toHaveBeenCalledWith(expect.objectContaining({ selection: { anchor: 30, head: 34 }, scrollIntoView: true }))
  ref.current!.revealLine(99)
  expect(dispatch).toHaveBeenCalledTimes(1)
})

it('inserts a line of its own: in place of an empty cursor line, else after the cursor line', () => {
  const dispatch = vi.fn()
  const at = (text: string) => ({
    state: { selection: { main: { head: 7 } }, doc: { lineAt: () => ({ from: 5, to: 5 + text.length, text }) } }, dispatch, focus: vi.fn(),
  })
  const ref = createRef<CodeEditorHandle>()
  render(<CodeEditor ref={ref} value="" onChange={() => {}} language="plain" />)
  const create = propsSpy.mock.calls.at(-1)![0].onCreateEditor
  create(at('  '))
  ref.current!.insertLine('[[x]]')
  expect(dispatch).toHaveBeenLastCalledWith(expect.objectContaining({ changes: { from: 5, to: 7, insert: '[[x]]' } }))
  create(at('some text'))
  ref.current!.insertLine('[[x]]')
  expect(dispatch).toHaveBeenLastCalledWith(expect.objectContaining({ changes: { from: 14, to: 14, insert: '\n[[x]]' } }))
})

it('is read-only through the state, not by switching editing off, so the value can still be focused and copied', () => {
  render(<CodeEditor value="x" onChange={() => {}} readOnly />)
  const props = propsSpy.mock.calls.at(-1)![0]
  expect(props.readOnly).toBe(true)
  expect(props.editable).toBeUndefined()
})

it('lets the content grow between minRows and maxRows when no height is given', () => {
  render(<CodeEditor value="" onChange={() => {}} minRows={3} maxRows={10} />)
  const props = propsSpy.mock.calls.at(-1)![0]
  expect(props.height).toBeUndefined()
  expect(props.minHeight).toBe('68px')
  expect(props.maxHeight).toBe('208px')
})
