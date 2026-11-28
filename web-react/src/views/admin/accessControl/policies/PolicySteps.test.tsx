// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { PolicySteps, type StepEntry } from './PolicySteps'

const entries = (): StepEntry[] => [
  { id: 'a', n: 1, label: 'First', count: 3, render: vi.fn((last: boolean) => <div>body-a-{String(last)}</div>) },
  { id: 'b', n: 2, label: 'Second', count: 0, render: vi.fn((last: boolean) => <div>body-b-{String(last)}</div>) },
  { id: 'c', n: 3, label: 'Third', render: vi.fn((last: boolean) => <div>body-c-{String(last)}</div>) },
]

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('PolicySteps', () => {
  it('stacks every step when the width is not measured, the last one marked last', () => {
    const e = entries()
    render(<PolicySteps entries={e} selected="a" onSelect={vi.fn()} label="Steps" />)
    expect(screen.getByText('body-a-false')).toBeTruthy()
    expect(screen.getByText('body-b-false')).toBeTruthy()
    expect(screen.getByText('body-c-true')).toBeTruthy()
    expect(screen.queryByRole('navigation')).toBeNull()
  })

  describe('when wide', () => {
    const wide = () => {
      vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
      vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 1000, height: 100, x: 0, y: 0, top: 0, left: 0, right: 1000, bottom: 100, toJSON: () => ({}) })
    }

    it('shows only the selected step and a step list with every label', () => {
      wide()
      render(<PolicySteps entries={entries()} selected="b" onSelect={vi.fn()} label="Match order" />)
      expect(screen.getByText('body-b-true')).toBeTruthy()
      expect(screen.queryByText(/body-a/)).toBeNull()
      expect(screen.queryByText(/body-c/)).toBeNull()
      const nav = screen.getByRole('navigation', { name: 'Match order' })
      for (const l of ['First', 'Second', 'Third']) expect(nav.textContent).toContain(l)
    })

    it('marks the selected step as current and shows counts above zero only', () => {
      wide()
      render(<PolicySteps entries={entries()} selected="b" onSelect={vi.fn()} label="Match order" />)
      expect(screen.getByTestId('step-nav-b').getAttribute('aria-current')).toBe('step')
      expect(screen.getByTestId('step-nav-a').getAttribute('aria-current')).toBeNull()
      expect(screen.getByTestId('step-nav-a').textContent).toContain('3')
      expect(screen.getByTestId('step-nav-b').textContent).not.toMatch(/0/)
    })

    it('calls onSelect with the id of the step clicked', () => {
      wide()
      const onSelect = vi.fn()
      render(<PolicySteps entries={entries()} selected="a" onSelect={onSelect} label="Match order" />)
      fireEvent.click(screen.getByTestId('step-nav-c'))
      expect(onSelect).toHaveBeenCalledWith('c')
    })
  })
})
