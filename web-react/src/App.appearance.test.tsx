// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from '@testing-library/react'

// Everything below App's own appearance wiring is stubbed: this test is about
// what App hands to the <html> sync, not about routing or the query cache.
vi.mock('@/router', () => ({ router: {} }))
vi.mock('@/router/AppRouter', () => ({ default: () => null }))
vi.mock('@/query/QuerySessionProvider', () => ({ QuerySessionProvider: ({ children }: { children: unknown }) => children }))
vi.mock('@/components/SnackbarHost', () => ({ default: () => null }))
vi.mock('@/components/ConfirmHost', () => ({ default: () => null }))
vi.mock('@/components/RequestProgressBar', () => ({ default: () => null }))

import App from '@/App'
import { useAppearanceStore } from '@/stores/appearance'

function stubMatchMedia(dark: boolean) {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: query.includes('dark') ? dark : false,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
  }))
}

describe('App -> <html> appearance sync', () => {
  beforeEach(() => {
    document.documentElement.className = ''
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it("resolves mode 'auto' against a dark OS preference before syncing", () => {
    stubMatchMedia(true)
    useAppearanceStore.setState({ mode: 'auto' })
    render(<App />)
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it("leaves .dark off for mode 'auto' on a light OS", () => {
    stubMatchMedia(false)
    useAppearanceStore.setState({ mode: 'auto' })
    render(<App />)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('an explicit light mode wins over a dark OS preference', () => {
    stubMatchMedia(true)
    useAppearanceStore.setState({ mode: 'light' })
    render(<App />)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })
})
