/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
vi.mock('@/api/client', () => ({ client: {} }))
const query = vi.hoisted(() => ({ data: { name: 'List', kind: 'remote', source_url: 'https://example.org/list', entries: ['domain:one.example'], entry_count: 271, entry_types: { domain: 271, full: 0, keyword: 0, regexp: 0, cidr: 0 }, parse_report: null } as Record<string, unknown> | undefined, error: null as Error | null, refetch: vi.fn() }))
vi.mock('@/query/accessControl', () => ({ useDestinationList: () => query }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }))
import ListEntriesSheet from './ListEntriesSheet'
const initial = query.data
beforeEach(() => { query.data = initial; query.error = null; vi.clearAllMocks() })
afterEach(cleanup)
function mount() {
  const onRefresh = vi.fn()
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}><QueryClientProvider client={new QueryClient()}><ListEntriesSheet id={7} busy={false} onClose={vi.fn()} onEdit={vi.fn()} onRefresh={onRefresh} onTest={vi.fn()} /></QueryClientProvider></ThemeProvider>)
  return { onRefresh }
}
it('bounds the page drawer to its viewport while retaining full-content totals', () => {
  mount()
  expect(['100vw', `${window.innerWidth}px`]).toContain(getComputedStyle(screen.getByRole('dialog')).maxWidth)
  expect(screen.getByRole('button', { name: 'domain 271' })).toBeTruthy()
  expect(screen.getByText('admin:access_control.list_entries.bounded_totals')).toBeTruthy()
})
it('uses the page-drawer layer below editors with accessible named headings and touch actions', () => {
  mount()
  expect(document.querySelector('.MuiDrawer-root')).not.toBeNull()
  expect(screen.getByRole('dialog', { name: 'List' }).querySelector('h2')?.querySelector('button')).toBeNull()
  for (const button of screen.getAllByRole('button')) expect(parseFloat(getComputedStyle(button).minHeight)).toBeGreaterThanOrEqual(44)
})
it('exposes the selected type and toggles filtering without refreshing the list', () => {
  const { onRefresh } = mount()
  const full = screen.getByRole('button', { name: 'full 0' })
  expect(full.getAttribute('aria-pressed')).toBe('false')
  fireEvent.click(full)
  expect(full.getAttribute('aria-pressed')).toBe('true')
  expect(screen.queryByText('domain:one.example')).toBeNull()
  fireEvent.click(full)
  expect(full.getAttribute('aria-pressed')).toBe('false')
  expect(screen.getByText('domain:one.example')).toBeTruthy()
  expect(onRefresh).not.toHaveBeenCalled()
})
it.each([false, true])('offers a touch-sized read retry without issuing refresh (cached=%s)', cached => {
  query.error = new Error('Read failed'); if (!cached) query.data = undefined
  const { onRefresh } = mount()
  const retry = screen.getByRole('button', { name: 'common:actions.retry' })
  expect(parseFloat(getComputedStyle(retry).minHeight)).toBeGreaterThanOrEqual(44)
  fireEvent.click(retry)
  expect(query.refetch).toHaveBeenCalledTimes(1)
  expect(onRefresh).not.toHaveBeenCalled()
})
