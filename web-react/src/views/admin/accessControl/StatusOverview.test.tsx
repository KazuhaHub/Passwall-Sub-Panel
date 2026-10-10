/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { destinationNode, destinationPolicies, destinationStatus } from '@/test/accessControlFixtures'
import { accessVerdict } from '@/utils/accessControl'
import StatusOverview from './StatusOverview'
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string, options?: Record<string, unknown>) => key + (options?.count !== undefined ? ` ${options.count}` : '') }) }))
const P = 'admin:access_control.'
afterEach(cleanup)
function props() {
  const data = destinationStatus()
  return { data, verdict: accessVerdict(data, destinationPolicies()), failed: false, refreshing: false, readAt: 1000, busy: false, onRetry: vi.fn().mockResolvedValue({}), onOpenNodes: vi.fn(), onOpenLists: vi.fn(), onPublish: vi.fn().mockResolvedValue(undefined), onPause: vi.fn().mockResolvedValue(undefined) }
}
const wrapper = ({ children }: { children: React.ReactNode }) => <ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}>{children}</ThemeProvider>
it('distinguishes first loading from a first read failure without claiming execution', async () => {
  const actions = props()
  const view = render(<StatusOverview {...actions} data={undefined} verdict={null} />, { wrapper })
  expect(screen.getByRole('progressbar', { name: `${P}status_unknown` })).toBeTruthy()
  expect(screen.queryByRole('status')).toBeNull()
  expect(screen.queryByRole('button', { name: `${P}publish` })).toBeNull()
  view.rerender(<StatusOverview {...actions} data={undefined} verdict={null} failed />)
  expect(screen.getByRole('alert').textContent).toContain(`${P}status_failed`)
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.retry' }))
  await waitFor(() => expect(actions.onRetry).toHaveBeenCalledOnce())
})
it('keeps a stale read verdict and excludes changing read time from the live announcement', () => {
  const actions = props()
  const view = render(<StatusOverview {...actions} />, { wrapper })
  const live = screen.getByRole('status'), initial = live.textContent
  view.rerender(<StatusOverview {...actions} failed readAt={2000} />)
  expect(live.textContent).toBe(initial)
  expect(screen.getByText(`${P}status_stale`)).toBeTruthy()
  expect(screen.getByText(`${P}read_at`).getAttribute('aria-hidden')).toBe('true')
  expect(live.textContent).not.toContain('read_at')
  expect(live.textContent).not.toContain('status_stale')
})
it('keeps third-party exclusion quiet and opens its coverage filter', () => {
  const actions = props(), data = destinationStatus({ nodes: [destinationNode(), destinationNode({ panel_id: 2, kind: '3xui', state: 'unsupported_kind' })] })
  const verdict = accessVerdict(data, destinationPolicies())
  expect(verdict?.kind).toBe('applied')
  render(<StatusOverview {...actions} data={data} verdict={verdict} />, { wrapper })
  expect(screen.getByRole('status').textContent).toBe(`${P}verdict.applied 1`)
  fireEvent.click(screen.getByRole('button', { name: `${P}not_executing.third_party 1` }))
  expect(actions.onOpenNodes).toHaveBeenCalledExactlyOnceWith('excluded')
  expect(screen.queryByText(`${P}not_executing.upgrade 0`)).toBeNull()
})
it('opens upgrade and offline filters from their actual independent counts', () => {
  const actions = props(), data = destinationStatus({ nodes: [destinationNode(), destinationNode({ panel_id: 2, state: 'unsupported_version', supports: { policy: false, hits: false, usage: false } }), destinationNode({ panel_id: 3, state: 'offline' }), destinationNode({ panel_id: 4, kind: 'sui', state: 'offline' })] })
  render(<StatusOverview {...actions} data={data} verdict={accessVerdict(data, destinationPolicies())} />, { wrapper })
  fireEvent.click(screen.getByRole('button', { name: `${P}not_executing.upgrade 1` }))
  expect(actions.onOpenNodes).toHaveBeenLastCalledWith('upgrade')
  fireEvent.click(screen.getByRole('button', { name: `${P}not_executing.offline 1` }))
  expect(actions.onOpenNodes).toHaveBeenLastCalledWith('excluded')
})
it('does not duplicate an explicit publication while its promise is pending', async () => {
  const actions = props(); let finish!: () => void
  actions.onPublish.mockImplementation(() => new Promise<void>(resolve => { finish = resolve }))
  render(<StatusOverview {...actions} />, { wrapper })
  const publish = screen.getByRole('button', { name: `${P}publish` })
  fireEvent.click(publish); fireEvent.click(publish)
  expect(actions.onPublish).toHaveBeenCalledOnce()
  expect(publish.getAttribute('aria-busy')).toBe('true')
  finish(); await waitFor(() => expect(publish.getAttribute('aria-busy')).toBeNull())
})
it('keeps the countdown hidden from the live region with an absolute scheduled-time label', () => {
  const actions = props(), data = destinationStatus({ generation: 2, next_publish_at: Date.now() + 60000 })
  render(<StatusOverview {...actions} data={data} verdict={accessVerdict(data, destinationPolicies())} />, { wrapper })
  expect(screen.getByLabelText(`${P}publish_at`).getAttribute('aria-hidden')).toBe('true')
  expect(screen.getByRole('status').textContent).not.toContain('countdown')
})
