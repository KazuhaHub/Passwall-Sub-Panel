/** @vitest-environment jsdom */
import type { ReactNode } from 'react'
import { ThemeProvider } from '@mui/material'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { sessionScope } from '@/query/session'
import GroupNodeCoverageDrawer from './GroupNodeCoverageDrawer'
const read = vi.hoisted(() => vi.fn())
vi.mock('@/query/destinationGroups', () => ({ useDestinationGroup: read }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en-US' } }) }))
const P = 'admin:access_control.group_coverage.'
const scope = sessionScope({ userId: 1, role: 'admin', authEpoch: 1 })
const group = { group_name: 'Guests', mode: 'allowlist', total_nodes: 3, eligible_nodes: 2, applied_nodes: 1, ineligible: [{ panel_id: 2, name: 'Excluded node', reason: 'sniffing' }], readiness: { unapplied_nodes: [{ panel_id: 3, name: 'Pending node' }] } }
const wrapper = ({ children }: { children: ReactNode }) => <ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}>{children}</ThemeProvider>
beforeEach(() => { read.mockReset(); read.mockReturnValue({ data: group, error: null }) })
afterEach(cleanup)
it('reports actual exclusions and unapplied nodes for the selected group', () => {
  render(<GroupNodeCoverageDrawer scope={scope} groupID={7} onClose={vi.fn()} />, { wrapper })
  expect(read).toHaveBeenCalledWith(scope, 7)
  expect(screen.getByText('Excluded node')).toBeTruthy(); expect(screen.getByText('Pending node')).toBeTruthy()
  expect(screen.queryByText(`${P}ready`)).toBeNull()
  expect(screen.getByRole('dialog').getAttribute('aria-labelledby')).toBeTruthy()
})
it('withholds stale coverage after failure and offers retry and close', () => {
  const retry = vi.fn(), close = vi.fn(); read.mockReturnValue({ data: group, error: new Error('offline'), refetch: retry })
  render(<GroupNodeCoverageDrawer scope={scope} groupID={7} onClose={close} />, { wrapper })
  expect(screen.queryByText('Pending node')).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.retry' })); expect(retry).toHaveBeenCalledOnce()
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' })); expect(close).toHaveBeenCalledOnce()
})
it('claims ready only when every eligible node has confirmed and no node is excluded', () => {
  read.mockReturnValue({ data: { ...group, total_nodes: 2, applied_nodes: 2, ineligible: [], readiness: { unapplied_nodes: [] } }, error: null })
  render(<GroupNodeCoverageDrawer scope={scope} groupID={7} onClose={vi.fn()} />, { wrapper })
  expect(screen.getByText(`${P}ready`)).toBeTruthy()
})
