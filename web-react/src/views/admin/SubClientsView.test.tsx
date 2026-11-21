// @vitest-environment jsdom
import { QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ConfirmHost from '@/components/ConfirmHost'
import i18n from '@/test/initI18n'
import { flatten, type Nested } from '@/i18n/options'
import enAdmin from '@/locales/en-US/admin.json'
import enNav from '@/locales/en-US/nav.json'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import SubClientsView from './SubClientsView'
import { CLIENT_PRESETS } from './subclients/clientRegistry'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
i18n.addResourceBundle('en-US', 'admin', flatten(enAdmin as Nested), true, true)
i18n.addResourceBundle('en-US', 'nav', flatten(enNav as Nested), true, true)

// A server record with the shapes a Go service really sends: null arrays on a detection-only family and on an app,
// and a field the page does not edit, which must come back unchanged in the saved body.
const stored = () => ({
  site_title: 'PSP',
  sub_client_filter_mode: null,
  sub_clients: [
    { name: 'Alpha', keywords: ['alpha'], render_format: 'mihomo', enabled: true, apps: null },
    { name: 'Beta', keywords: null, render_format: 'sing-box', enabled: false, apps: [{ name: 'B app', platforms: null, import_url_template: 'b://', install_url: '', enabled: true, sort: 10, recommended_for: null }] },
    { name: 'Gamma', keywords: ['gamma'], render_format: 'uri-list', enabled: true, apps: [] },
  ],
})

function mount() {
  const router = createMemoryRouter([{ path: '/admin/sub-clients', element: <SubClientsView /> }], { initialEntries: ['/admin/sub-clients'] })
  render(<QueryClientProvider client={makeTestQueryClient()}><ConfirmHost /><RouterProvider router={router} /></QueryClientProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  api.get.mockResolvedValue({ data: stored() })
  api.put.mockImplementation(async (_url: string, body: unknown) => ({ data: body }))
})
afterEach(cleanup)

const sentBody = () => api.put.mock.calls.at(-1)![1] as { sub_clients: { name: string }[] }

describe('SubClientsView', () => {
  it('reports a failed read instead of spinning forever', async () => {
    // The loader had no catch: `loading` went false but `settings` stayed null,
    // and the render guard is `loading || !settings` — so a failed read left the
    // page on a spinner that could never resolve.
    api.get.mockRejectedValue(new Error('offline'))
    mount()
    await waitFor(() => expect(screen.getByText('Settings are temporarily unavailable')).toBeTruthy())
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy()
  })

  it('says on the page that the order decides which family matches', async () => {
    mount()
    expect((await screen.findByText(/Order matters/)).textContent).toMatch(/first match wins/)
  })

  it('saves the whole settings object with only the two edited fields changed', async () => {
    mount()
    fireEvent.click(await screen.findByText('Alpha'))
    const keywords = await screen.findByLabelText(/Keywords/)
    // Typing keeps what was typed: a comma or space is not eaten while the next word has not arrived.
    fireEvent.change(keywords, { target: { value: 'a,' } })
    expect((keywords as HTMLInputElement).value).toBe('a,')
    fireEvent.change(keywords, { target: { value: 'a, b' } })
    expect((keywords as HTMLInputElement).value).toBe('a, b')
    fireEvent.click(screen.getByRole('button', { name: 'Done' }))
    fireEvent.change(screen.getByLabelText('Client filter mode'), { target: { value: 'whitelist' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][0]).toBe('/admin/settings/ui')
    expect(api.put.mock.calls[0][1]).toEqual({
      site_title: 'PSP',
      sub_client_filter_mode: 'whitelist',
      sub_clients: [
        { name: 'Alpha', keywords: ['a', 'b'], render_format: 'mihomo', enabled: true, apps: [] },
        { name: 'Beta', keywords: [], render_format: 'sing-box', enabled: false, apps: [{ name: 'B app', platforms: [], import_url_template: 'b://', install_url: '', enabled: true, sort: 10, recommended_for: [] }] },
        { name: 'Gamma', keywords: ['gamma'], render_format: 'uri-list', enabled: true, apps: [] },
      ],
    })
  })

  it('sends the families in the order the admin dragged them into', async () => {
    mount()
    const handle = await screen.findByRole('button', { name: 'Drag to reorder: Alpha' })
    handle.focus()
    fireEvent.keyDown(handle, { key: ' ' })
    fireEvent.keyDown(handle, { key: 'ArrowDown' })
    fireEvent.keyDown(handle, { key: ' ' })
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(sentBody().sub_clients.map(f => f.name)).toEqual(['Beta', 'Alpha', 'Gamma'])
  })

  it('appends a new family at the end and a new app after the last one', async () => {
    mount()
    fireEvent.click(await screen.findByRole('button', { name: 'Add detection family' }))
    fireEvent.change(await screen.findByLabelText('Name'), { target: { value: 'Delta' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add app' }))
    fireEvent.click(screen.getByRole('button', { name: 'Done' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    const sent = sentBody().sub_clients as unknown as { name: string; apps: { sort: number }[] }[]
    expect(sent.map(f => f.name)).toEqual(['Alpha', 'Beta', 'Gamma', 'Delta'])
    expect(sent[3].apps.map(a => a.sort)).toEqual([40])
  })

  it('asks before resetting to the presets, and replaces the whole list when confirmed', async () => {
    mount()
    await screen.findByText('Alpha')
    const more = screen.getAllByRole('button', { name: 'More' })[0]
    fireEvent.pointerDown(more, { button: 0, pointerType: 'mouse' })
    fireEvent.mouseDown(more, { button: 0 })
    fireEvent.click(more)
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Reset to defaults' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText('Reset one-click import clients?')).toBeTruthy()
    // Cancel is where focus starts: an Enter typed too early cannot confirm it.
    expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Cancel' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Reset' }))
    await waitFor(() => expect(screen.queryByText('Alpha')).toBeNull())
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(sentBody().sub_clients).toEqual(CLIENT_PRESETS)
  })
})
