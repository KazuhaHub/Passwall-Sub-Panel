/** @vitest-environment jsdom */
import { createRef, forwardRef, useImperativeHandle } from 'react'
import { ThemeProvider } from '@mui/material'
import { cleanup, render } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
const probe = vi.hoisted(() => ({ props: {} as Record<string, unknown>, dispatch: vi.fn(), focus: vi.fn() }))
vi.mock('@uiw/react-codemirror', () => ({ default: forwardRef((props: Record<string, unknown>, ref) => {
  probe.props = props
  useImperativeHandle(ref, () => ({ view: { state: { doc: { lines: 3, line: (n: number) => [{ from: 0, to: 1 }, { from: 2, to: 4 }, { from: 5, to: 8 }][n - 1] } }, dispatch: probe.dispatch, focus: probe.focus } }))
  return null
}) }))
import CodeEditor, { type CodeEditorHandle } from './CodeEditor'
afterEach(cleanup)
beforeEach(() => { probe.dispatch.mockClear(); probe.focus.mockClear() })

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
