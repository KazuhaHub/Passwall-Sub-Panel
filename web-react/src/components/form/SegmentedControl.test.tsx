// @vitest-environment jsdom
import '@/test/initI18n'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { SegmentedControl } from './SegmentedControl'

afterEach(cleanup)

const options = [
  { value: 'a', label: 'Alpha' },
  { value: 'b', label: 'Beta', disabled: true },
  { value: 'c', label: 'Gamma' },
]

describe('SegmentedControl', () => {
  it('is a radio group with the current value checked and one tab stop', () => {
    render(<SegmentedControl aria-label="Mode" value="a" onChange={() => {}} options={options} />)
    expect(screen.getByRole('radiogroup', { name: 'Mode' })).toBeTruthy()
    expect(screen.getByRole('radio', { name: 'Alpha' }).getAttribute('aria-checked')).toBe('true')
    const tabbable = screen.getAllByRole('radio').filter(r => r.getAttribute('tabindex') === '0')
    expect(tabbable).toHaveLength(1)
  })

  it('selects on click', () => {
    const onChange = vi.fn()
    render(<SegmentedControl aria-label="Mode" value="a" onChange={onChange} options={options} />)
    fireEvent.click(screen.getByRole('radio', { name: 'Gamma' }))
    expect(onChange).toHaveBeenCalledWith('c')
  })

  it('moves with the arrow keys and skips a disabled option', () => {
    const onChange = vi.fn()
    render(<SegmentedControl aria-label="Mode" value="a" onChange={onChange} options={options} />)
    fireEvent.keyDown(screen.getByRole('radio', { name: 'Alpha' }), { key: 'ArrowRight' })
    expect(onChange).toHaveBeenCalledWith('c')
    fireEvent.keyDown(screen.getByRole('radio', { name: 'Alpha' }), { key: 'ArrowLeft' })
    expect(onChange).toHaveBeenLastCalledWith('c')
  })

  it('does not select a disabled option', () => {
    const onChange = vi.fn()
    render(<SegmentedControl aria-label="Mode" value="a" onChange={onChange} options={options} />)
    fireEvent.click(screen.getByRole('radio', { name: 'Beta' }))
    expect(onChange).not.toHaveBeenCalled()
  })
})

describe('SegmentedControl contrast', () => {
  it('draws unselected options in foreground at 70%, not the muted grey that is 4.34:1 on the track', () => {
    render(<SegmentedControl aria-label="Mode" value="a" onChange={() => {}} options={options} />)
    expect(screen.getByRole('radio', { name: 'Gamma' }).className).toContain('text-foreground/70')
  })
})
