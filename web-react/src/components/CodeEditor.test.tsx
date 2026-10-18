// @vitest-environment jsdom
import type { CSSProperties } from 'react'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import CodeEditor from './CodeEditor'

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
