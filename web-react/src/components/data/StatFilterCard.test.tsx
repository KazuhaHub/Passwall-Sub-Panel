// @vitest-environment jsdom
import '@/test/initI18n'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { StatFilterCard, StatFilterGrid } from './StatFilterCard'

afterEach(cleanup)

describe('StatFilterCard', () => {
  it('is a static tile without onToggle: no button, no pressed state', () => {
    render(<StatFilterGrid><StatFilterCard label="Nodes" value="12" caption="of 20" /></StatFilterGrid>)
    expect(screen.queryByRole('button')).toBeNull()
    expect(screen.getByText('12')).toBeTruthy()
    expect(screen.getByText('of 20')).toBeTruthy()
  })

  it('is a toggle button whose name is its label and number, pressed while its filter is on', () => {
    const onToggle = vi.fn()
    const { rerender } = render(<StatFilterCard label="Problems" value="3" pressed={false} onToggle={onToggle} />)
    const b = screen.getByRole('button', { name: /Problems.*3/ })
    expect(b.getAttribute('aria-pressed')).toBe('false')
    fireEvent.click(b)
    expect(onToggle).toHaveBeenCalledTimes(1)
    rerender(<StatFilterCard label="Problems" value="3" pressed onToggle={onToggle} />)
    expect(screen.getByRole('button', { name: /Problems/ }).getAttribute('aria-pressed')).toBe('true')
    fireEvent.click(screen.getByRole('button', { name: /Problems/ }))
    expect(onToggle).toHaveBeenCalledTimes(2)
  })

  it('shows a dash while the number is not known', () => {
    render(<StatFilterCard label="Online" value={undefined} />)
    expect(screen.getByText('—')).toBeTruthy()
  })
})
