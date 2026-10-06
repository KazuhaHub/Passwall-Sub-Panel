/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import type { DestinationReference } from '@/api/accessControl'
import UsedByPopover from './UsedByPopover'
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string, options?: Record<string, unknown>) => key + (options?.name ? ` ${options.name}` : options?.count !== undefined ? ` ${options.count}` : '') }) }))
const P = 'admin:access_control.lists.'
afterEach(cleanup)
function mount(references: DestinationReference[], ownerGroupId = 0, onOpenGroup?: (id: number) => void) {
  const onOpenPolicy = vi.fn()
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}><UsedByPopover name="Finance" references={references} ownerGroupId={ownerGroupId} onOpenPolicy={onOpenPolicy} onOpenGroup={onOpenGroup} /></ThemeProvider>)
  return onOpenPolicy
}
it('shows unused lists without opening an empty popover', () => {
  mount([])
  expect(screen.getByText(`${P}unused`)).toBeTruthy()
  expect(screen.queryByRole('button')).toBeNull()
})
it('opens the actual policy reference and dismisses the popover', async () => {
  const onOpenPolicy = mount([{ kind: 'policy', id: 12, name: 'No mail' }, { kind: 'group', id: 4, name: 'Guests' }])
  fireEvent.click(screen.getByRole('button', { name: `${P}open_references Finance` }))
  const popover = screen.getByRole('dialog', { name: `${P}references_title Finance` })
  expect(within(popover).getByText('Guests')).toBeTruthy()
  expect(within(popover).queryByRole('button', { name: /Guests/ })).toBeNull()
  fireEvent.click(within(popover).getByRole('button', { name: `${P}open_policy No mail` }))
  expect(onOpenPolicy).toHaveBeenCalledExactlyOnceWith(12)
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})
it('keeps owned groups readable and enables group navigation only when supplied', async () => {
  const onOpenGroup = vi.fn()
  mount([{ kind: 'group', id: 4, name: 'Guests' }], 4, onOpenGroup)
  expect(screen.getByText(`${P}owner Guests`)).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: `${P}open_references Finance` }))
  fireEvent.click(screen.getByRole('button', { name: `${P}open_group Guests` }))
  expect(onOpenGroup).toHaveBeenCalledExactlyOnceWith(4)
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})
it('shows an owner without inventing a reference when the server returns none', () => {
  mount([], 4)
  expect(screen.getByText(`${P}owner #4`)).toBeTruthy()
  expect(screen.queryByRole('button')).toBeNull()
})
