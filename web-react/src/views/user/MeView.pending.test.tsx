// @vitest-environment jsdom
import { StrictMode } from 'react'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api, mountMe, profile, serve, snack, zeroUsage } from './testHarness'
import MeView from './MeView'

// The page's own confirmation is the subject here, so it is replaced by a spy; the real dialog has its own tests.
const confirmMock = vi.hoisted(() => vi.fn(async (_opts: Record<string, unknown>) => true))
vi.mock('@/components/ConfirmHost', () => ({ default: () => null, confirm: confirmMock }))

// A deferred promise the test resolves on its own schedule, standing in for a slow (3-10s) upstream response.
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}

const tenGB = { traffic_limit_bytes: 10 * 1024 ** 3, traffic_reset_period: 'monthly' }

describe('usage card while the usage read is still in flight', () => {
  it('shows a loading state instead of 0.00/X GB and 0%', async () => {
    const pendingUsage = deferred<{ data: typeof zeroUsage }>()
    serve({}, tenGB)
    const reads = api.get.getMockImplementation()!
    api.get.mockImplementation(async (url: string) => (url === '/user/me/traffic' ? pendingUsage.promise : reads(url)))

    mountMe(<StrictMode><MeView /></StrictMode>)

    // The profile gates the page-level skeleton; the usage card must not lie about a zero reading while ITS OWN request is still in flight.
    await screen.findByText('My usage')
    expect(screen.queryByText('0.00 / 10 GB')).toBeNull()
    expect(screen.queryByText('0.0%')).toBeNull()
    expect(screen.getAllByText('Loading').length).toBeGreaterThan(0)

    pendingUsage.resolve({ data: { ...zeroUsage, period_used_bytes: 2 * 1024 ** 3, today_used_bytes: 5 * 1024 * 1024 } })

    await waitFor(() => expect(screen.getByText('2.00 / 10 GB')).toBeTruthy())
    expect(screen.getByText('20.0%')).toBeTruthy()
    expect(screen.getByText('8.00 GB left')).toBeTruthy()
    expect(screen.getByText('5.00 MB')).toBeTruthy()
    expect(screen.queryByText('Loading')).toBeNull()
  })

  it('prints an unlimited account as used traffic with no bar and no "left"', async () => {
    serve({ '/user/me/traffic': { ...zeroUsage, period_used_bytes: 3 * 1024 ** 3 } })
    mountMe(<MeView />)
    await screen.findByText('3.00 GB')
    expect(screen.getByText('Unlimited')).toBeTruthy()
    expect(screen.queryByRole('progressbar')).toBeNull()
    expect(screen.queryByText(/left$/)).toBeNull()
  })
})

describe('reset credentials button', () => {
  it('confirms by naming the consequence, stays busy until the reset settles, and ignores a second click', async () => {
    serve({}, tenGB)
    const pending = deferred<{ data: { sub_token: string; sub_url: string; uuid: string } }>()
    api.post.mockImplementation(async (url: string) => {
      if (url === '/user/me/reset-credentials') return pending.promise
      throw new Error(`Unexpected POST ${url}`)
    })

    mountMe(<StrictMode><MeView /></StrictMode>)

    const button = await screen.findByRole('button', { name: 'Reset credentials' })
    fireEvent.click(button)

    // Busy at once, before the confirm's own promise has settled, let alone the request.
    expect((button as HTMLButtonElement).disabled).toBe(true)
    expect(button.getAttribute('aria-busy')).toBe('true')

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/user/me/reset-credentials'))
    // A second click while busy must not ask again or send a second reset.
    fireEvent.click(button)
    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(button.querySelector('svg.animate-spin')).toBeTruthy()

    // The confirmation says what breaks, names the verb, and starts on Cancel.
    expect(confirmMock.mock.calls[0][0]).toMatchObject({
      title: 'Reset credentials', confirmText: 'Reset credentials', destructive: true, initialFocus: 'cancel',
      message: expect.stringContaining('invalidates the old subscription URL'),
    })

    pending.resolve({ data: { sub_token: 'tok2', sub_url: 'https://sub.example.test/new', uuid: 'uuid-2' } })
    await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false))
    expect(snack).toHaveBeenCalledWith('Credentials reset. New UUID: uuid-2', 'success')
  })

  it('sends nothing when the confirmation is declined', async () => {
    serve({}, tenGB)
    confirmMock.mockResolvedValueOnce(false)
    mountMe(<MeView />)
    fireEvent.click(await screen.findByRole('button', { name: 'Reset credentials' }))
    await waitFor(() => expect((screen.getByRole('button', { name: 'Reset credentials' }) as HTMLButtonElement).disabled).toBe(false))
    expect(api.post).not.toHaveBeenCalled()
  })
})

describe('page frame', () => {
  it('pads itself, as the shell gives its content no padding', async () => {
    serve({}, { ...profile })
    const { container } = mountMe(<MeView />)
    await screen.findByText('My usage')
    expect(container.firstElementChild!.className).toContain('p-4')
    expect(container.firstElementChild!.className).toContain('sm:p-6')
  })
})
