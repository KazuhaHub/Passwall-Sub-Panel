// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { IconButton } from './IconButton'

afterEach(cleanup)

describe('IconButton', () => {
  it('has an accessible name from its label', () => {
    render(<IconButton label="刷新"><svg /></IconButton>)
    expect(screen.getByRole('button', { name: '刷新' })).toBeTruthy()
  })

  it('shows the same words as a tooltip on keyboard focus', async () => {
    render(<IconButton label="刷新"><svg /></IconButton>)
    const button = screen.getByRole('button', { name: '刷新' })
    fireEvent.focus(button)
    await vi.waitFor(() => expect(document.querySelector('[data-slot="tooltip-content"]')?.textContent).toBe('刷新'))
  })

  it('forwards clicks and other props to the button', () => {
    const onClick = vi.fn()
    render(<IconButton label="删除" onClick={onClick} data-x="1"><svg /></IconButton>)
    const button = screen.getByRole('button', { name: '删除' })
    fireEvent.click(button)
    expect(onClick).toHaveBeenCalledTimes(1)
    expect(button.getAttribute('data-x')).toBe('1')
  })
})

describe('touch targets', () => {
  // jsdom evaluates no media queries, so this checks the rule is on the element;
  // scripts/dist-guards.mjs (run by smoke:dist) checks Tailwind generated it into the CSS.
  it.each(['icon', 'icon-sm', 'icon-xs'] as const)('an %s button is at least 2rem square on a coarse pointer', size => {
    render(<IconButton label="x" size={size}><svg /></IconButton>)
    const cls = screen.getByRole('button', { name: 'x' }).className
    expect(cls).toContain('pointer-coarse:min-h-[2rem]')
    expect(cls).toContain('pointer-coarse:min-w-[2rem]')
  })

  it('does not rely on an invisible hit area that could overlap a neighbour', () => {
    render(<IconButton label="x" size="icon-xs"><svg /></IconButton>)
    expect(screen.getByRole('button', { name: 'x' }).className).not.toContain('after:')
  })

  it('keeps a caller’s own class', () => {
    render(<IconButton label="y" className="rounded-full"><svg /></IconButton>)
    expect(screen.getByRole('button', { name: 'y' }).className).toContain('rounded-full')
  })
})

describe('a switched-off control that still explains itself', () => {
  // A `disabled` button cannot be focused and shows no tooltip, so nobody could learn why it is
  // off. aria-disabled keeps it focusable; the click must still be ignored.
  it('ignores the click while aria-disabled and stays focusable', () => {
    const onClick = vi.fn()
    render(<IconButton label="下一页" aria-disabled onClick={onClick}><svg /></IconButton>)
    const button = screen.getByRole('button', { name: '下一页' })
    fireEvent.click(button)
    expect(onClick).not.toHaveBeenCalled()
    expect((button as HTMLButtonElement).disabled).toBe(false)
    expect(button.className).toContain('aria-disabled:opacity-50')
    expect(button.className).toContain('aria-disabled:cursor-not-allowed')
  })

  it('still clicks when aria-disabled is false or absent', () => {
    const onClick = vi.fn()
    render(<IconButton label="上一页" aria-disabled={false} onClick={onClick}><svg /></IconButton>)
    fireEvent.click(screen.getByRole('button', { name: '上一页' }))
    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('shows a tooltip that differs from the accessible name, and accepts rich content', async () => {
    render(<IconButton label="拖动" tooltip={<span data-testid="why">已排序，无法拖动</span>}><svg /></IconButton>)
    fireEvent.focus(screen.getByRole('button', { name: '拖动' }))
    await vi.waitFor(() => expect(document.querySelector('[data-testid="why"]')?.textContent).toBe('已排序，无法拖动'))
  })
})
