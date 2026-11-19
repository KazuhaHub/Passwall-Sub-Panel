// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { KpiGrid, KpiTile } from './KpiTile'

afterEach(cleanup)

describe('KpiTile', () => {
  it('is a static tile without onToggle: no button, no aria-pressed', () => {
    render(<KpiGrid><KpiTile label="Rules" value="12" caption="of 256" /></KpiGrid>)
    expect(screen.queryByRole('button')).toBeNull()
    expect(screen.getByText('12')).toBeTruthy()
    expect(screen.getByText('of 256')).toBeTruthy()
  })

  it('is a toggle with aria-pressed when onToggle is given, and a second click asks to clear', () => {
    const onToggle = vi.fn()
    const { rerender } = render(<KpiTile label="Problems" value="1" pressed={false} onToggle={onToggle} />)
    const button = screen.getByRole('button', { name: /Problems/ })
    expect(button.getAttribute('aria-pressed')).toBe('false')
    fireEvent.click(button)
    expect(onToggle).toHaveBeenCalledTimes(1)

    rerender(<KpiTile label="Problems" value="1" pressed onToggle={onToggle} />)
    const pressed = screen.getByRole('button', { name: /Problems/ })
    expect(pressed.getAttribute('aria-pressed')).toBe('true')
    // The pressed state is more than colour: a ring as well as the tint.
    expect(pressed.className).toContain('ring-2')
    fireEvent.click(pressed)
    expect(onToggle).toHaveBeenCalledTimes(2)
  })

  it('lays a clickable and a plain tile out identically, so labels line up in a row', () => {
    render(<><KpiTile label="a" value="1" /><KpiTile label="b" value="2" onToggle={() => {}} /></>)
    const layout = (el: Element) => el.className.split(' ').filter(c => /^(flex|min-h|flex-col|items-|justify-|px-|py-)/.test(c)).sort().join(' ')
    const plain = document.querySelector('div[data-slot="kpi-tile"]')!
    const button = document.querySelector('button[data-slot="kpi-tile"]')!
    expect(layout(plain)).toBe(layout(button))
    expect(plain.className).toContain('justify-start')
  })

  it('colours the figure only when it is itself a failure', () => {
    render(<><KpiTile label="a" value="3" danger /><KpiTile label="b" value="4" /></>)
    expect(screen.getByText('3').className).toContain('text-destructive')
    expect(screen.getByText('4').className).not.toContain('text-destructive')
  })
})
