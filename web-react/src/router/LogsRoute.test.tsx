// @vitest-environment jsdom
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import LogsRoute from './LogsRoute'

function Where() {
  const loc = useLocation()
  return <p data-testid="location">{loc.pathname + loc.search}</p>
}

function mount(url: string) {
  render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path="/admin/logs" element={<LogsRoute><p>logs page</p><Where /></LogsRoute>} />
        <Route path="/admin/risk" element={<><p>risk center</p><Where /></>} />
      </Routes>
    </MemoryRouter>,
  )
}

function signIn(role: 'admin' | 'operator') {
  useAuthStore.setState({ role, userId: role === 'admin' ? 1 : 2, hasToken: true })
}

afterEach(() => {
  cleanup()
  useAuthStore.setState({ role: '' })
})

// The location and risk tabs used to live on the Logs page, and their links are
// in bookmarks, old notifications and muscle memory. Without the redirect the
// Logs page's tab parser would quietly fall back to subscription logs — a page
// that answers "who is sharing?" with a list of fetches.
describe('LogsRoute', () => {
  it.each(['geo', 'risk'])("redirects an admin's ?tab=%s to the risk center", tab => {
    signIn('admin')
    mount(`/admin/logs?tab=${tab}`)

    expect(screen.getByTestId('location').textContent).toBe(`/admin/risk?tab=${tab}`)
    expect(screen.getByText('risk center')).toBeTruthy()
    expect(screen.queryByText('logs page')).toBeNull()
  })

  // An operator cannot open the risk center (it reads adminGroup endpoints
  // only). Sending one there would bounce them to the dashboard; the Logs page
  // they asked for is the better landing, and its own tab parser shows them
  // the subscription logs they can read.
  it.each(['geo', 'risk'])('keeps an operator on the logs page for ?tab=%s', tab => {
    signIn('operator')
    mount(`/admin/logs?tab=${tab}`)

    expect(screen.getByText('logs page')).toBeTruthy()
    expect(screen.getByTestId('location').textContent).toBe(`/admin/logs?tab=${tab}`)
  })

  it.each(['', '?tab=sub', '?tab=audit', '?tab=certs', '?tab=bogus'])('renders its children for %j', search => {
    signIn('admin')
    mount(`/admin/logs${search}`)

    expect(screen.getByText('logs page')).toBeTruthy()
    expect(screen.getByTestId('location').textContent).toBe(`/admin/logs${search}`)
  })
})
