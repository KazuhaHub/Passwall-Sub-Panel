// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import DeleteIcon from '@mui/icons-material/Delete'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AsyncButton, AsyncIconButton } from './AsyncButton'

function deferred() {
  let resolve!: () => void
  let reject!: (e: unknown) => void
  const promise = new Promise<void>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

afterEach(cleanup)

describe('AsyncIconButton', () => {
  // A ROW ACTION ON A SLOW LINK: the icon has to change the moment it is
  // clicked, and a second click must not send the request twice.
  it('shows progress and ignores further clicks until the action settles', async () => {
    const pending = deferred()
    const onClick = vi.fn(() => pending.promise)
    render(<AsyncIconButton aria-label="delete row" onClick={onClick}><DeleteIcon /></AsyncIconButton>)
    const button = screen.getByRole('button', { name: 'delete row' })

    fireEvent.click(button)
    fireEvent.click(button)
    expect(onClick).toHaveBeenCalledTimes(1)
    expect(button.getAttribute('aria-busy')).toBe('true')
    expect((button as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByRole('progressbar')).toBeTruthy()
    expect(screen.queryByTestId('DeleteIcon')).toBeNull()

    await act(async () => { pending.resolve(); await pending.promise })
    expect((button as HTMLButtonElement).disabled).toBe(false)
    expect(button.getAttribute('aria-busy')).toBeNull()
    expect(screen.getByTestId('DeleteIcon')).toBeTruthy()
  })

  it('recovers from a failed action without an unhandled rejection', async () => {
    const pending = deferred()
    render(<AsyncIconButton aria-label="renew" onClick={() => pending.promise}><DeleteIcon /></AsyncIconButton>)
    const button = screen.getByRole('button', { name: 'renew' })
    fireEvent.click(button)
    await act(async () => { pending.reject(new Error('502')); await pending.promise.catch(() => {}) })
    expect((button as HTMLButtonElement).disabled).toBe(false)
  })

  it('stays an ordinary button for a synchronous handler', () => {
    const onClick = vi.fn()
    render(<AsyncIconButton aria-label="open" onClick={onClick}><DeleteIcon /></AsyncIconButton>)
    fireEvent.click(screen.getByRole('button', { name: 'open' }))
    fireEvent.click(screen.getByRole('button', { name: 'open' }))
    expect(onClick).toHaveBeenCalledTimes(2)
    expect(screen.queryByRole('progressbar')).toBeNull()
  })
})

describe('AsyncButton', () => {
  it('swaps its start icon for progress and disables itself while pending', async () => {
    const pending = deferred()
    render(<AsyncButton startIcon={<DeleteIcon />} onClick={() => pending.promise}>Purge</AsyncButton>)
    const button = screen.getByRole('button', { name: 'Purge' })
    fireEvent.click(button)
    expect((button as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByRole('progressbar')).toBeTruthy()
    expect(screen.queryByTestId('DeleteIcon')).toBeNull()
    await act(async () => { pending.resolve(); await pending.promise })
    expect((button as HTMLButtonElement).disabled).toBe(false)
    expect(screen.getByTestId('DeleteIcon')).toBeTruthy()
  })

  it('can be driven by an outside pending flag too', () => {
    const { rerender } = render(<AsyncButton pending={false} onClick={() => {}}>Refresh</AsyncButton>)
    expect(screen.queryByRole('progressbar')).toBeNull()
    rerender(<AsyncButton pending onClick={() => {}}>Refresh</AsyncButton>)
    expect(screen.getByRole('progressbar')).toBeTruthy()
    expect((screen.getByRole('button', { name: 'Refresh' }) as HTMLButtonElement).disabled).toBe(true)
  })
})
