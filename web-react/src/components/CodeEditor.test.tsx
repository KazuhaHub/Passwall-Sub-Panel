/** @vitest-environment jsdom */
import { createRef, forwardRef, useImperativeHandle } from 'react'
import { ThemeProvider } from '@mui/material'
import { cleanup, render } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
const probe = vi.hoisted(() => ({ props: {} as Record<string, unknown>, dispatch: vi.fn(), focus: vi.fn(), state: null as import('@uiw/react-codemirror').EditorState | null }))
vi.mock('@uiw/react-codemirror', async importOriginal => ({ ...await importOriginal<object>(), default: forwardRef((props: Record<string, unknown>, ref) => {
  probe.props = props
  useImperativeHandle(ref, () => ({ view: { state: probe.state ?? { doc: { lines: 3, line: (n: number) => [{ from: 0, to: 1 }, { from: 2, to: 4 }, { from: 5, to: 8 }][n - 1] } }, dispatch: probe.dispatch, focus: probe.focus } }))
  return null
}) }))
import CodeEditor, { type CodeEditorHandle } from './CodeEditor'
import { EditorState, EditorView, type Extension } from '@uiw/react-codemirror'
afterEach(cleanup)
beforeEach(() => { probe.dispatch.mockClear(); probe.focus.mockClear(); probe.state = null })
it('attaches the supplied accessible label to the editing surface', () => {
  render(<CodeEditor value="10.0.0.0/8" onChange={() => {}} language="plain" ariaLabel="CIDRs (one per line)" />)
  const state = EditorState.create({ extensions: probe.props.extensions as Extension[] })
  expect(state.facet(EditorView.contentAttributes)).toContainEqual({ 'aria-label': 'CIDRs (one per line)' })
})

it('defaults to the active theme, supports plain text and converts minimum rows', () => {
  const theme = createAppTheme({ mode: 'dark', sourceColor: '#6750a4', language: 'en-US' })
  render(<ThemeProvider theme={theme}><CodeEditor value="a\nbb\nccc" onChange={() => {}} language="plain" minRows={6} /></ThemeProvider>)
  expect(probe.props.theme).toBe('dark')
  expect(probe.props.extensions).toEqual([])
  expect(probe.props.height).not.toBe('380px')
})

it('reveals and selects the requested line through its public ref', () => {
  const ref = createRef<CodeEditorHandle>()
  render(<CodeEditor ref={ref} value="a\nbb\nccc" onChange={() => {}} />)
  ref.current?.revealLine(2)
  expect(probe.dispatch).toHaveBeenCalledWith({ selection: { anchor: 2, head: 4 }, scrollIntoView: true })
  expect(probe.focus).toHaveBeenCalled()
})

it('bounds line selection and ignores invalid line numbers', () => {
  const ref = createRef<CodeEditorHandle>()
  render(<CodeEditor ref={ref} value="a\nbb\nccc" onChange={() => {}} />)
  ref.current?.revealLine(0)
  expect(probe.dispatch).toHaveBeenLastCalledWith({ selection: { anchor: 0, head: 1 }, scrollIntoView: true })
  ref.current?.revealLine(99)
  expect(probe.dispatch).toHaveBeenLastCalledWith({ selection: { anchor: 5, head: 8 }, scrollIntoView: true })
  ref.current?.revealLine(Number.NaN)
  expect(probe.dispatch).toHaveBeenCalledTimes(2)
})

it('retains YAML and read-only behavior and honors explicit height and theme', () => {
  render(<ThemeProvider theme={createAppTheme({ mode: 'dark', sourceColor: '#6750a4', language: 'en-US' })}>
    <CodeEditor value="rules: []" onChange={() => {}} readOnly dark={false} height="240px" minRows={10} />
  </ThemeProvider>)
  expect(probe.props.theme).toBe('light')
  expect(probe.props.height).toBe('240px')
  expect(probe.props.extensions).toHaveLength(1)
  expect(probe.props.readOnly).toBe(true)
  expect(probe.props.editable).toBe(false)
})

it('inserts a standalone line at the current cursor as one undoable edit', () => {
  probe.state = EditorState.create({ doc: 'BeforeAfter', selection: { anchor: 6 } })
  const ref = createRef<CodeEditorHandle>()
  render(<CodeEditor ref={ref} value="BeforeAfter" onChange={() => {}} language="plain" />)
  ref.current?.insertLine('[[data-collection]]')
  expect(probe.dispatch).toHaveBeenCalledWith({ changes: { from: 6, to: 6, insert: '\n[[data-collection]]\n' }, selection: { anchor: 27 }, scrollIntoView: true })
  expect(probe.focus).toHaveBeenCalled()
})

it('does not insert into a readonly editor', () => {
  probe.state = EditorState.create({ doc: '' })
  const ref = createRef<CodeEditorHandle>()
  render(<CodeEditor ref={ref} value="" onChange={() => {}} readOnly />)
  ref.current?.insertLine('[[data-collection]]')
  expect(probe.dispatch).not.toHaveBeenCalled()
})
