// @vitest-environment jsdom
import { StrictMode } from 'react'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { Trash2Icon } from 'lucide-react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AsyncButton, AsyncIconButton } from './AsyncButton'

// The semantics are those of components/AsyncButton.test.tsx, re-asserted on the
// shadcn buttons: same pending behaviour, so a page swapping one for the other
// loses nothing.

function deferred() {
  let resolve!: () => void
  let reject!: (e: unknown) => void
  const promise = new Promise<void>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

const spinner = () => document.querySelector('svg.animate-spin')

afterEach(cleanup)

describe('AsyncIconButton', () => {
  // A ROW ACTION ON A SLOW LINK: the icon has to change the moment it is clicked,
  // and a second click must not send the request twice.
  it('shows progress and ignores further clicks until the action settles', async () => {
    const pending = deferred()
    const onClick = vi.fn(() => pending.promise)
    render(<AsyncIconButton label="delete row" onClick={onClick}><Trash2Icon data-testid="icon" /></AsyncIconButton>)
    const button = screen.getByRole('button', { name: 'delete row' }) as HTMLButtonElement

    fireEvent.click(button)
    fireEvent.click(button)
    expect(onClick).toHaveBeenCalledTimes(1)
    expect(button.getAttribute('aria-busy')).toBe('true')
    expect(button.disabled).toBe(true)
    expect(spinner()).toBeTruthy()
    expect(screen.queryByTestId('icon')).toBeNull()

    await act(async () => { pending.resolve(); await pending.promise })
    expect(button.disabled).toBe(false)
    expect(button.getAttribute('aria-busy')).toBeNull()
    expect(screen.getByTestId('icon')).toBeTruthy()
  })

  it('recovers from a failed action without an unhandled rejection', async () => {
    const pending = deferred()
    render(<AsyncIconButton label="renew" onClick={() => pending.promise}><Trash2Icon /></AsyncIconButton>)
    const button = screen.getByRole('button', { name: 'renew' }) as HTMLButtonElement
    fireEvent.click(button)
    await act(async () => { pending.reject(new Error('502')); await pending.promise.catch(() => {}) })
    expect(button.disabled).toBe(false)
  })

  // STRICT MODE REPLAYS EFFECTS ON MOUNT (setup, cleanup, setup). A mounted flag
  // that only its cleanup writes ends that replay as "unmounted", and the button
  // then never leaves its busy state after the first action.
  it('returns to idle after an action under StrictMode, and again after the next one', async () => {
    let pending = deferred()
    render(<StrictMode><AsyncIconButton label="retry" onClick={() => pending.promise}><Trash2Icon /></AsyncIconButton></StrictMode>)
    const button = screen.getByRole('button', { name: 'retry' }) as HTMLButtonElement
    for (let round = 0; round < 2; round++) {
      fireEvent.click(button)
      expect(button.disabled).toBe(true)
      await act(async () => { pending.resolve(); await pending.promise })
      expect(button.disabled).toBe(false)
      pending = deferred()
    }
  })

  it('stays an ordinary button for a synchronous handler', () => {
    const onClick = vi.fn()
    render(<AsyncIconButton label="open" onClick={onClick}><Trash2Icon /></AsyncIconButton>)
    fireEvent.click(screen.getByRole('button', { name: 'open' }))
    fireEvent.click(screen.getByRole('button', { name: 'open' }))
    expect(onClick).toHaveBeenCalledTimes(2)
    expect(spinner()).toBeNull()
  })
})

describe('AsyncButton', () => {
  it('swaps its icon for progress and disables itself while pending', async () => {
    const pending = deferred()
    render(<AsyncButton icon={<Trash2Icon data-testid="icon" />} onClick={() => pending.promise}>Purge</AsyncButton>)
    const button = screen.getByRole('button', { name: 'Purge' }) as HTMLButtonElement
    fireEvent.click(button)
    expect(button.disabled).toBe(true)
    expect(spinner()).toBeTruthy()
    expect(screen.queryByTestId('icon')).toBeNull()
    await act(async () => { pending.resolve(); await pending.promise })
    expect(button.disabled).toBe(false)
    expect(screen.getByTestId('icon')).toBeTruthy()
  })

  it('can be driven by an outside pending flag too', () => {
    const { rerender } = render(<AsyncButton pending={false} onClick={() => {}}>Refresh</AsyncButton>)
    expect(spinner()).toBeNull()
    rerender(<AsyncButton pending onClick={() => {}}>Refresh</AsyncButton>)
    expect(spinner()).toBeTruthy()
    expect((screen.getByRole('button', { name: 'Refresh' }) as HTMLButtonElement).disabled).toBe(true)
  })
})
