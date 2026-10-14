/** @vitest-environment jsdom */
import { useState } from 'react'
import { createMemoryRouter, RouterProvider, useNavigate } from 'react-router'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const confirm = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm }))
import { useLeaveGuard } from './useLeaveGuard'

beforeEach(() => confirm.mockReset())
afterEach(cleanup)

function mount(includeTab = false) {
  function Editor() {
    const navigate = useNavigate()
    const [dirty, setDirty] = useState(true)
    useLeaveGuard(dirty, { title: 'Unsaved', message: 'Discard?' }, includeTab
      ? (next, current) => next.pathname !== current.pathname || new URLSearchParams(next.search).get('tab') !== 'edit'
      : undefined)
    return <>
      <button onClick={() => { void navigate('/editor?tab=edit&sheet=12') }}>Drawer</button>
      <button onClick={() => { void navigate('/editor?tab=records') }}>Records</button>
      <button onClick={() => { void navigate('/away') }}>Away</button>
      <button onClick={() => setDirty(false)}>Save</button>
    </>
  }
  const router = createMemoryRouter([{ path: '/editor', element: <Editor /> }, { path: '/away', element: <p>Destination</p> }], { initialEntries: ['/editor?tab=edit'] })
  render(<RouterProvider router={router} />)
  return router
}

it('allows drawer changes, cancels a page departure and proceeds only after confirmation', async () => {
  const router = mount()
  fireEvent.click(screen.getByText('Drawer'))
  await waitFor(() => expect(router.state.location.search).toContain('sheet=12'))
  expect(confirm).not.toHaveBeenCalled()
  confirm.mockResolvedValueOnce(false)
  fireEvent.click(screen.getByText('Away'))
  await waitFor(() => expect(router.state.blockers.get('1')?.state).toBe('unblocked'))
  expect(router.state.location.pathname).toBe('/editor')
  expect(confirm).toHaveBeenCalledTimes(1)
  confirm.mockResolvedValueOnce(true)
  fireEvent.click(screen.getByText('Away'))
  await screen.findByText('Destination')
  expect(confirm).toHaveBeenCalledTimes(2)
})

it('accepts an editor-specific tab boundary', async () => {
  const router = mount(true)
  confirm.mockResolvedValueOnce(false)
  fireEvent.click(screen.getByText('Records'))
  await waitFor(() => expect(confirm).toHaveBeenCalledTimes(1))
  await waitFor(() => expect(router.state.location.search).toBe('?tab=edit'))
  fireEvent.click(screen.getByText('Save'))
  fireEvent.click(screen.getByText('Records'))
  await waitFor(() => expect(router.state.location.search).toBe('?tab=records'))
  expect(confirm).toHaveBeenCalledTimes(1)
})

it('blocks browser unload while dirty and removes the guard when saved or unmounted', () => {
  mount()
  const dirty = new Event('beforeunload', { cancelable: true })
  act(() => { window.dispatchEvent(dirty) })
  expect(dirty.defaultPrevented).toBe(true)
  fireEvent.click(screen.getByText('Save'))
  const saved = new Event('beforeunload', { cancelable: true })
  act(() => { window.dispatchEvent(saved) })
  expect(saved.defaultPrevented).toBe(false)
  cleanup()
  const unmounted = new Event('beforeunload', { cancelable: true })
  window.dispatchEvent(unmounted)
  expect(unmounted.defaultPrevented).toBe(false)
})
