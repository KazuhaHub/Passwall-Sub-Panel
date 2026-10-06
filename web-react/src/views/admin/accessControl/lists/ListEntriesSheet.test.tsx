/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
vi.mock('@/api/client', () => ({ client: {} }))
vi.mock('@/query/accessControl', () => ({ useDestinationList: () => ({ data: { name: 'List', kind: 'custom', entries: ['domain:one.example'], entry_count: 271, entry_types: { domain: 271, full: 0, keyword: 0, regexp: 0, cidr: 0 }, parse_report: null } }) }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }))
import ListEntriesSheet from './ListEntriesSheet'
afterEach(cleanup)
it('overrides the default dialog margin width so the phone drawer can cover its viewport', () => {
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}><QueryClientProvider client={new QueryClient()}><ListEntriesSheet id={7} busy={false} onClose={vi.fn()} onEdit={vi.fn()} onRefresh={vi.fn()} /></QueryClientProvider></ThemeProvider>)
  expect(['100vw', `${window.innerWidth}px`]).toContain(getComputedStyle(screen.getByRole('dialog')).maxWidth)
  expect(screen.getByRole('button', { name: 'domain 271' })).toBeTruthy()
  expect(screen.getByText('admin:access_control.list_entries.bounded_totals')).toBeTruthy()
})
