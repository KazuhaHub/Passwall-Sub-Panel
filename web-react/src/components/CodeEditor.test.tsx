// @vitest-environment jsdom
import { createRef, type CSSProperties } from 'react'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import CodeEditor, { type CodeEditorHandle } from './CodeEditor'

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

// A list is not YAML: `domain:example.com` would be drawn as a key. 'plain'
// adds no language extension, and the default stays yaml for the old callers.
it('adds the yaml language by default and none for plain', () => {
  render(<CodeEditor value="a" onChange={() => {}} />)
  expect(propsSpy.mock.calls.at(-1)![0].extensions).toHaveLength(1)
  cleanup()
  render(<CodeEditor value="a" onChange={() => {}} language="plain" />)
  expect(propsSpy.mock.calls.at(-1)![0].extensions).toHaveLength(0)
})

it('turns minRows into a height unless a height is given', () => {
  render(<CodeEditor value="" onChange={() => {}} language="plain" minRows={6} />)
  expect(screen.getByTestId('code-mirror').style.height).toBe('128px')
  cleanup()
  render(<CodeEditor value="" onChange={() => {}} language="plain" minRows={6} height="50px" />)
  expect(screen.getByTestId('code-mirror').style.height).toBe('50px')
})

it('follows the theme mode when dark is not given', () => {
  render(<CodeEditor value="" onChange={() => {}} />)
  expect(propsSpy.mock.calls.at(-1)![0].theme).toBe('light')
  cleanup()
  render(<CodeEditor value="" onChange={() => {}} dark />)
  expect(propsSpy.mock.calls.at(-1)![0].theme).toBe('dark')
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
