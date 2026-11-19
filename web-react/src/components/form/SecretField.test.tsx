// @vitest-environment jsdom
import '@/test/initI18n'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { SecretField } from './SecretField'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('SecretField', () => {
  it('is masked until revealed, and reveal toggles back', () => {
    render(<SecretField aria-label="Token" value="s3cret" onChange={() => {}} />)
    const input = screen.getByLabelText('Token') as HTMLInputElement
    expect(input.type).toBe('password')
    fireEvent.click(screen.getByRole('button', { name: 'Show' }))
    expect(input.type).toBe('text')
    fireEvent.click(screen.getByRole('button', { name: 'Hide' }))
    expect(input.type).toBe('password')
  })

  it('copies the value and confirms it', async () => {
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    // The shared helper only trusts the async API in a secure context; jsdom reports none.
    Object.defineProperty(window, 'isSecureContext', { configurable: true, value: true })
    render(<SecretField aria-label="Token" value="s3cret" readOnly />)
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Copy' })) })
    expect(writeText).toHaveBeenCalledWith('s3cret')
    expect(screen.getByRole('button', { name: 'Copied' })).toBeTruthy()
  })

  // The panel is often served over plain HTTP, where navigator.clipboard does not exist. The shared
  // copy helper falls back to execCommand; a private writeText call here would silently do nothing.
  it('still copies where the async clipboard API is unavailable', async () => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
    Object.defineProperty(window, 'isSecureContext', { configurable: true, value: false })
    const exec = vi.fn(() => true)
    Object.defineProperty(document, 'execCommand', { configurable: true, value: exec })
    render(<SecretField aria-label="Token" value="s3cret" readOnly />)
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Copy' })) })
    expect(exec).toHaveBeenCalledWith('copy')
    expect(screen.getByRole('button', { name: 'Copied' })).toBeTruthy()
  })

  it('has no copy button when there is nothing to copy', () => {
    render(<SecretField aria-label="Token" value="" onChange={() => {}} />)
    expect(screen.queryByRole('button', { name: 'Copy' })).toBeNull()
  })

  it('says a stored secret exists without ever showing it', () => {
    render(<SecretField aria-label="Token" value="" isSet onChange={() => {}} />)
    const input = screen.getByLabelText('Token') as HTMLInputElement
    expect(input.placeholder).toBe('Already set; leave empty to keep it')
    expect(input.value).toBe('')
  })

  it('reports edits', () => {
    const onChange = vi.fn()
    render(<SecretField aria-label="Token" value="" onChange={onChange} />)
    fireEvent.change(screen.getByLabelText('Token'), { target: { value: 'abc' } })
    expect(onChange).toHaveBeenCalledWith('abc')
  })
})

describe('SecretField and password managers', () => {
  it('asks browsers and managers not to offer to save an API token as a login', () => {
    render(<SecretField aria-label="Token" value="" onChange={() => {}} />)
    const input = screen.getByLabelText('Token')
    // autocomplete="off" is ignored by Chrome for password inputs; new-password is not
    expect(input.getAttribute('autocomplete')).toBe('new-password')
    expect(input.getAttribute('data-1p-ignore')).not.toBeNull()
    expect(input.getAttribute('data-lpignore')).toBe('true')
    expect(input.getAttribute('data-bwignore')).toBe('true')
  })
})
