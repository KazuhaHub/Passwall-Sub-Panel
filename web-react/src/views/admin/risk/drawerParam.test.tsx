/** @vitest-environment jsdom */
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { parseUserId, useDrawerParam } from './drawerParam'

// The drawer's URL rule (§3.4): opening PUSHES `param=<id>` with a state mark,
// so Back closes it; closing an entry that open pushed goes Back, so the
// history holds no dead entry and one more Back leaves the page; closing a
// drawer that arrived in the URL (a deep link, a redirect) REPLACES it away
// and stays on the page.

function Harness({ param }: { param: string }) {
  const { id, open, close } = useDrawerParam(param)
  const loc = useLocation()
  const navigate = useNavigate()
  return (
    <>
      <p data-testid="location">{loc.pathname + loc.search}</p>
      <p data-testid="state">{JSON.stringify(loc.state ?? null)}</p>
      <p data-testid="id">{id === null ? 'none' : String(id)}</p>
      <button onClick={() => open(7)}>open</button>
      <button onClick={() => close()}>close</button>
      <button onClick={() => navigate(-1)}>back</button>
    </>
  )
}

function mount(entries: string[], param = 'user') {
  render(
    <MemoryRouter initialEntries={entries} initialIndex={entries.length - 1}>
      <Routes>
        <Route path="/admin/risk" element={<Harness param={param} />} />
        <Route path="/admin/users" element={<><Harness param={param} /><p>users page</p></>} />
        <Route path="/admin/dashboard" element={<p data-testid="location">/admin/dashboard</p>} />
      </Routes>
    </MemoryRouter>,
  )
}

const where = () => screen.getByTestId('location').textContent

afterEach(cleanup)

describe('useDrawerParam', () => {
  it('open pushes the id with a state mark and keeps every other param', () => {
    mount(['/admin/dashboard', '/admin/risk?tab=queue&source=geo'])
    expect(screen.getByTestId('id').textContent).toBe('none')

    fireEvent.click(screen.getByText('open'))

    expect(where()).toBe('/admin/risk?tab=queue&source=geo&user=7')
    expect(screen.getByTestId('id').textContent).toBe('7')
    expect(JSON.parse(screen.getByTestId('state').textContent ?? 'null')).toEqual({ riskDrawer: 'user' })
    // Pushed, not replaced: Back returns to the list without the drawer.
    act(() => { fireEvent.click(screen.getByText('back')) })
    expect(where()).toBe('/admin/risk?tab=queue&source=geo')
  })

  it('close after open goes Back, so ONE more Back leaves the page', () => {
    mount(['/admin/dashboard', '/admin/risk?tab=queue'])
    fireEvent.click(screen.getByText('open'))
    expect(where()).toBe('/admin/risk?tab=queue&user=7')

    act(() => { fireEvent.click(screen.getByText('close')) })
    expect(where()).toBe('/admin/risk?tab=queue')
    expect(screen.getByTestId('id').textContent).toBe('none')

    act(() => { fireEvent.click(screen.getByText('back')) })
    expect(where()).toBe('/admin/dashboard')
  })

  it('a deep link closed replaces the param away and stays on the page', () => {
    mount(['/admin/dashboard', '/admin/risk?tab=queue&user=7'])
    expect(screen.getByTestId('id').textContent).toBe('7')

    fireEvent.click(screen.getByText('close'))
    expect(where()).toBe('/admin/risk?tab=queue')
    expect(screen.getByTestId('id').textContent).toBe('none')

    // Replaced: the entry before the page is still one Back away, and no
    // entry naming the drawer is left behind to reopen it.
    act(() => { fireEvent.click(screen.getByText('back')) })
    expect(where()).toBe('/admin/dashboard')
  })

  it('a state mark for another param does not count as its own push', () => {
    // The Users page opens its drawer under `risk`; a `user` drawer must not
    // take that entry for one it pushed and go Back past it.
    mount(['/admin/dashboard', '/admin/users?risk=7'], 'risk')
    fireEvent.click(screen.getByText('close'))
    expect(where()).toBe('/admin/users')
  })

  it('reads only a positive integer id', () => {
    mount(['/admin/risk?user=abc'])
    expect(screen.getByTestId('id').textContent).toBe('none')
  })
})

describe('parseUserId', () => {
  it.each(['abc', '0', '-7', '7.5', '07x', '', '9007199254740993'])('rejects %j', raw => {
    expect(parseUserId(raw)).toBeNull()
  })

  it('accepts a positive safe integer', () => {
    expect(parseUserId('7')).toBe(7)
    expect(parseUserId(null)).toBeNull()
  })
})
