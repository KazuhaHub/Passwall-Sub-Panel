// @vitest-environment jsdom
import '@/test/initI18n'
import { act, cleanup, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ConfirmHost from '@/components/ConfirmHost'
import { renderInRouter } from '@/test/routerHarness'
import { useLeaveGuard } from './useLeaveGuard'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

const copy = (message: string) => ({ title: 'Discard?', message, confirmText: 'Discard', cancelText: 'Stay' })
const leavesPage = (next: { pathname: string }, cur: { pathname: string }) => next.pathname !== cur.pathname

function Guard({ dirty, message }: { dirty: boolean; message: string }) {
  useLeaveGuard(dirty, copy(message), leavesPage)
  return null
}
const Page = ({ a, b }: { a: boolean; b: boolean }) => <><ConfirmHost /><Guard dirty={a} message="page edits" /><Guard dirty={b} message="sheet edits" /></>

describe('several guards mounted at once', () => {
  it('asks once, naming both, when either is dirty, and registers a single blocker', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    const { router } = renderInRouter(<Page a b />)
    let nav!: Promise<void>
    await act(async () => { nav = router.navigate('/elsewhere') })
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog.textContent).toContain('page edits')
    expect(dialog.textContent).toContain('sheet edits')
    expect(screen.getAllByRole('alertdialog')).toHaveLength(1)
    expect(router.state.blockers.size).toBe(1)
    await act(async () => { screen.getByRole('button', { name: 'Stay' }).click() })
    await nav
    expect(router.state.location.pathname).toBe('/')
    const logged = [...warn.mock.calls, ...error.mock.calls].map(c => String(c[0]))
    expect(logged.filter(m => /one blocker/.test(m))).toEqual([])
  })

  it('asks when only the second guard is dirty', async () => {
    const { router } = renderInRouter(<Page a={false} b />)
    await act(async () => { void router.navigate('/elsewhere') })
    expect((await screen.findByRole('alertdialog')).textContent).toContain('sheet edits')
  })

  it('does not ask when both are clean', async () => {
    const { router } = renderInRouter(<Page a={false} b={false} />)
    await act(async () => { await router.navigate('/elsewhere') })
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(router.state.location.pathname).toBe('/elsewhere')
  })

  it('leaves when the answer is yes', async () => {
    const { router } = renderInRouter(<Page a b={false} />)
    await act(async () => { void router.navigate('/elsewhere') })
    await act(async () => { (await screen.findByRole('button', { name: 'Discard' })).click() })
    expect(router.state.location.pathname).toBe('/elsewhere')
  })

  it('stops blocking when the guards unmount', async () => {
    const { router } = renderInRouter(<Page a b />)
    await act(async () => { void router.navigate('/first') })
    await act(async () => { (await screen.findByRole('button', { name: 'Stay' })).click() })
    cleanup()
    await act(async () => { await router.navigate('/second') })
    expect(router.state.location.pathname).toBe('/second')
  })

  it('warns on reload once, while any guard is dirty', () => {
    renderInRouter(<Page a={false} b />)
    const ev = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(true)
  })
})

describe('under a plain router', () => {
  it('mounts without a blocker and does not throw', () => {
    expect(() => render(<MemoryRouter><Guard dirty message="x" /></MemoryRouter>)).not.toThrow()
  })
})
