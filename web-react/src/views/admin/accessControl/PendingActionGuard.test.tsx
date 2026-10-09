/** @vitest-environment jsdom */
import { useState } from 'react'
import { createMemoryRouter, useNavigate } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import AppRouter from '@/router/AppRouter'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }))
import PendingActionGuard, { PendingActionScope } from './PendingActionGuard'
afterEach(() => { cleanup(); confirmation.mockReset() })
it.each([false, true])('keeps idle guards out of the router so a dirty editor still owns departure confirmation (shared=%s)', async shared => {
  confirmation.mockResolvedValue(false)
  function Editor() {
    const navigate = useNavigate()
    useLeaveGuard(true, { title: 'Keep draft?', message: 'Discard draft?' })
    return <button onClick={() => void navigate('/away')}>Leave</button>
  }
  const page = <><Editor /><PendingActionGuard hold={false} /><PendingActionGuard hold={false} /></>
  const router = createMemoryRouter([{ path: '/', element: shared ? <PendingActionScope>{page}</PendingActionScope> : page }, { path: '/away', element: <p>Destination</p> }])
  render(<AppRouter router={router} />); fireEvent.click(screen.getByText('Leave'))
  await waitFor(() => expect(confirmation).toHaveBeenCalledOnce())
  expect(router.state.location.pathname).toBe('/'); expect(screen.queryByText('Destination')).toBeNull()
})
it.each([false, true])('releases a settled guard completely before a later dirty editor takes ownership (shared=%s)', async shared => {
  confirmation.mockResolvedValue(false)
  function Editor() {
    const navigate = useNavigate(); useLeaveGuard(true, { title: 'Keep draft?', message: 'Discard draft?' })
    return <button onClick={() => void navigate('/away')}>Leave draft</button>
  }
  function Page() {
    const [held, setHeld] = useState(true), [editor, setEditor] = useState(false)
    return <><PendingActionGuard hold={held} /><button onClick={() => setHeld(false)}>Finish</button><button onClick={() => setEditor(true)}>Edit</button>{editor && <Editor />}</>
  }
  const router = createMemoryRouter([{ path: '/', element: shared ? <PendingActionScope><Page /></PendingActionScope> : <Page /> }, { path: '/away', element: <p>Destination</p> }])
  render(<AppRouter router={router} />); fireEvent.click(screen.getByText('Finish'))
  await waitFor(() => expect(router.state.blockers.size).toBe(0))
  fireEvent.click(screen.getByText('Edit')); fireEvent.click(screen.getByText('Leave draft'))
  await waitFor(() => expect(confirmation).toHaveBeenCalledOnce())
  expect(router.state.location.pathname).toBe('/')
})
