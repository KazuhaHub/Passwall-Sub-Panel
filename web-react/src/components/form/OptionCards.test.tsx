// @vitest-environment jsdom
import '@/test/initI18n'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { OptionCards } from './OptionCards'

afterEach(cleanup)

const options = [
  { value: 'forward', title: 'Port forwarding', description: 'The entry passes traffic through unchanged', diagram: <svg data-testid="d1" /> },
  { value: 'tunnel', title: 'Encrypted tunnel', description: 'The entry ends the connection, then tunnels to the landing node' },
  { value: 'cdn', title: 'CDN', description: 'The CDN fetches from the origin', disabled: true, disabledReason: 'This node has no domain' },
]

describe('OptionCards', () => {
  it('is a radio group of cards that show title, description and diagram', () => {
    render(<OptionCards aria-label="Line type" value="forward" onChange={() => {}} options={options} />)
    expect(screen.getByRole('radiogroup', { name: 'Line type' })).toBeTruthy()
    const forward = screen.getByRole('radio', { name: /Port forwarding/ })
    expect(forward.getAttribute('aria-checked')).toBe('true')
    expect(forward.textContent).toContain('The entry passes traffic through unchanged')
    expect(screen.getByTestId('d1')).toBeTruthy()
  })

  it('chooses on click and with Space/Enter', () => {
    const onChange = vi.fn()
    render(<OptionCards aria-label="Line type" value="forward" onChange={onChange} options={options} />)
    fireEvent.click(screen.getByRole('radio', { name: /Encrypted tunnel/ }))
    expect(onChange).toHaveBeenLastCalledWith('tunnel')
    fireEvent.keyDown(screen.getByRole('radio', { name: /Encrypted tunnel/ }), { key: ' ' })
    expect(onChange).toHaveBeenCalledTimes(2)
  })

  it('moves with arrow keys, skipping the disabled card', () => {
    const onChange = vi.fn()
    render(<OptionCards aria-label="Line type" value="tunnel" onChange={onChange} options={options} />)
    fireEvent.keyDown(screen.getByRole('radio', { name: /Encrypted tunnel/ }), { key: 'ArrowDown' })
    expect(onChange).toHaveBeenLastCalledWith('forward')
  })

  it('shows why a card is unavailable and does not select it', () => {
    const onChange = vi.fn()
    render(<OptionCards aria-label="Line type" value="forward" onChange={onChange} options={options} />)
    const cdn = screen.getByRole('radio', { name: /CDN/ })
    expect(cdn.getAttribute('aria-disabled')).toBe('true')
    expect(cdn.textContent).toContain('This node has no domain')
    expect(cdn.getAttribute('aria-describedby')).toBeTruthy()
    fireEvent.click(cdn)
    expect(onChange).not.toHaveBeenCalled()
  })
})
