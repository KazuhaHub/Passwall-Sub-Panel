/** @vitest-environment jsdom */
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { useDrawerParam } from './useDrawerParam'

// The access-control page's drawers (plan §7.1): a page-level sheet is a
// string param (`sheet=test`), the account drawer a number (`user=7`), and
// only one is open at a time. Opening the account drawer from a sheet REPLACES
// the sheet's entry, so history holds the page and the account drawer and one
// Back from the drawer lands on the page with no sheet. A prefill (the test
// panel's destination) rides in history state, never in the URL.

const SHEETS = ['test', 'nodes', 'exemptions', 'list'] as const
type Sheet = (typeof SHEETS)[number]
const parseSheet = (raw: string | null): Sheet | null => (SHEETS as readonly string[]).includes(raw ?? '') ? raw as Sheet : null

function Harness() {
  const sheet = useDrawerParam<Sheet>('sheet', { parse: parseSheet, exclusive: ['user'] })
  const user = useDrawerParam('user', { exclusive: ['sheet'] })
  const loc = useLocation()
  const navigate = useNavigate()
  return (
    <>
      <p data-testid="location">{loc.pathname + loc.search}</p>
      <p data-testid="state">{JSON.stringify(loc.state ?? null)}</p>
      <p data-testid="sheet">{sheet.value ?? 'none'}</p>
      <p data-testid="user">{user.value === null ? 'none' : String(user.value)}</p>
      <button onClick={() => sheet.open('test', { prefill: { target: 'smtp.example.com', port: 587 } })}>open sheet</button>
      <button onClick={() => sheet.open('nodes')}>open nodes</button>
      <button onClick={() => sheet.close()}>close sheet</button>
      <button onClick={() => user.open(7)}>open user</button>
      <button onClick={() => user.open(7, { replace: true })}>open user replace</button>
      <button onClick={() => user.close()}>close user</button>
      <button onClick={() => navigate(-1)}>back</button>
    </>
  )
}

function mount(entries: string[]) {
  render(
    <MemoryRouter initialEntries={entries} initialIndex={entries.length - 1}>
      <Routes>
        <Route path="/admin/access-control" element={<Harness />} />
        <Route path="/admin/dashboard" element={<p data-testid="location">/admin/dashboard</p>} />
      </Routes>
    </MemoryRouter>,
  )
}

const where = () => screen.getByTestId('location').textContent
afterEach(cleanup)

describe('useDrawerParam with string values and exclusive drawers', () => {
  it('opens a sheet with a string value, marks the entry and carries a prefill in state, not the URL', () => {
    mount(['/admin/dashboard', '/admin/access-control?tab=policies'])
    fireEvent.click(screen.getByText('open sheet'))
    expect(where()).toBe('/admin/access-control?tab=policies&sheet=test')
    expect(where()).not.toContain('smtp')
    expect(JSON.parse(screen.getByTestId('state').textContent!)).toEqual({
      drawer: 'sheet', prefill: { target: 'smtp.example.com', port: 587 },
    })
  })

  it('reads only a known sheet', () => {
    mount(['/admin/access-control?sheet=bogus'])
    expect(screen.getByTestId('sheet').textContent).toBe('none')
  })

  it('sheet -> user (replace) -> Back lands on the page and the sheet does not come back', () => {
    mount(['/admin/dashboard', '/admin/access-control?tab=policies'])
    fireEvent.click(screen.getByText('open sheet'))
    fireEvent.click(screen.getByText('open user replace'))
    // One entry for the page, one for the account drawer; the sheet param went in the same step.
    expect(where()).toBe('/admin/access-control?tab=policies&user=7')
    expect(screen.getByTestId('sheet').textContent).toBe('none')
    act(() => { fireEvent.click(screen.getByText('close user')) })
    expect(where()).toBe('/admin/access-control?tab=policies')
    expect(screen.getByTestId('sheet').textContent).toBe('none')
    // ...and one more Back leaves the page.
    act(() => { fireEvent.click(screen.getByText('back')) })
    expect(where()).toBe('/admin/dashboard')
  })

  it('the prefill does not come back after Back', () => {
    mount(['/admin/access-control?tab=policies'])
    fireEvent.click(screen.getByText('open sheet'))
    expect(screen.getByTestId('state').textContent).toContain('smtp.example.com')
    act(() => { fireEvent.click(screen.getByText('back')) })
    expect(where()).toBe('/admin/access-control?tab=policies')
    expect(screen.getByTestId('state').textContent).not.toContain('smtp.example.com')
    // Opening the sheet again without a prefill does not conjure the old one.
    fireEvent.click(screen.getByText('open nodes'))
    expect(screen.getByTestId('state').textContent).not.toContain('smtp.example.com')
  })

  it('opening one drawer closes the other in the same push', () => {
    mount(['/admin/access-control?tab=policies&user=7'])
    fireEvent.click(screen.getByText('open nodes'))
    expect(where()).toBe('/admin/access-control?tab=policies&sheet=nodes')
  })

  it('a drawer closed after a push goes Back; a deep-linked one is replaced away', () => {
    mount(['/admin/dashboard', '/admin/access-control?sheet=nodes'])
    fireEvent.click(screen.getByText('close sheet'))
    expect(where()).toBe('/admin/access-control')
    act(() => { fireEvent.click(screen.getByText('back')) })
    expect(where()).toBe('/admin/dashboard')
  })

  it('honours the legacy riskDrawer mark of an entry an older build pushed', () => {
    render(
      <MemoryRouter initialEntries={['/admin/dashboard', { pathname: '/admin/access-control', search: '?user=7', state: { riskDrawer: 'user' } }]} initialIndex={1}>
        <Routes>
          <Route path="/admin/access-control" element={<Harness />} />
          <Route path="/admin/dashboard" element={<p data-testid="location">/admin/dashboard</p>} />
        </Routes>
      </MemoryRouter>,
    )
    // Pushed by us: close goes Back instead of replacing in place.
    act(() => { fireEvent.click(screen.getByText('close user')) })
    expect(where()).toBe('/admin/dashboard')
  })
})
