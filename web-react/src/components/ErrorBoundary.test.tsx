// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'

import ErrorBoundary from './ErrorBoundary'

function Boom(): never {
  throw new Error('render exploded')
}

describe('ErrorBoundary', () => {
  beforeEach(() => {
    // React logs a caught render error; the boundary also logs. Not what this checks.
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders its children when nothing throws', () => {
    render(<ErrorBoundary><p>fine</p></ErrorBoundary>)
    expect(screen.getByText('fine')).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  // No provider is mounted around it: the fallback must not need a query client, router or any
  // store, because any of those may be what threw.
  it('replaces a crashed subtree with a fallback that offers a reload, with no provider around it', () => {
    const reload = vi.fn()
    vi.stubGlobal('location', { ...window.location, reload })
    render(<ErrorBoundary><Boom /></ErrorBoundary>)
    const alert = screen.getByRole('alert')
    expect(alert).toBeTruthy()
    const buttons = screen.getAllByRole('button')
    // The reload button comes first; the details toggle follows it.
    fireEvent.click(buttons[0])
    expect(reload).toHaveBeenCalledTimes(1)
    vi.unstubAllGlobals()
  })

  it('keeps the technical detail behind a toggle and shows the message when opened', () => {
    render(<ErrorBoundary><Boom /></ErrorBoundary>)
    expect(screen.queryByText(/render exploded/)).toBeNull()
    const toggle = screen.getAllByRole('button').find(b => b.getAttribute('aria-expanded') === 'false')!
    fireEvent.click(toggle)
    expect(screen.getByText(/render exploded/)).toBeTruthy()
  })
})
