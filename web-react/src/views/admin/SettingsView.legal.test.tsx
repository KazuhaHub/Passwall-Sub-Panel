// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
// The harness first: it registers the request-client mock, which must precede anything that imports the client.
import { api, mountAdmin } from './adminTestHarness'
import { useSiteStore } from '@/stores/site'
import { reads } from './settings/testSupport'
import SettingsView from './SettingsView'

async function openLegalTab(settings: Record<string, unknown> = {}, extra: Record<string, unknown> = {}) {
  reads(extra, settings)
  mountAdmin(<SettingsView />, '/admin/settings?tab=legal')
  return screen.findByRole('switch', { name: 'Enable the privacy and terms page' })
}
const save = () => fireEvent.click(within(screen.getByRole('region', { name: 'You have unsaved changes' })).getByRole('button', { name: 'Save' }))
const echo = () => api.put.mockImplementationOnce(async (_url: string, data: unknown) => ({ data }))

describe('Settings > Privacy and terms section', () => {
  it('is the section after SSO, and its switches ride the section\'s save bar', async () => {
    const enabled = await openLegalTab()
    const names = within(screen.getByRole('navigation', { name: 'Settings sections' })).getAllByRole('button').map(b => b.textContent)
    expect(names.slice(-2)).toEqual(['SSO', 'Privacy and terms'])

    const usage = screen.getByRole('switch', { name: /Disclose per-site usage/ })
    expect(enabled.getAttribute('aria-checked')).toBe('false')
    expect(usage.getAttribute('aria-checked')).toBe('false')
    fireEvent.click(enabled)
    fireEvent.click(usage)
    echo()
    save()
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/settings/ui', expect.objectContaining({ legal_enabled: true, legal_disclose_usage: true })))
  })

  it('shows the stored switches', async () => {
    const enabled = await openLegalTab({ legal_enabled: true, legal_disclose_usage: true })
    expect(enabled.getAttribute('aria-checked')).toBe('true')
    expect(screen.getByRole('switch', { name: /Disclose per-site usage/ }).getAttribute('aria-checked')).toBe('true')
  })

  it("after a save the footer links and the consent dialog follow the server's own answer (/auth/methods), not the raw switch", async () => {
    useSiteStore.setState({ legalEnabled: false, legalConsentVersion: 0 })
    const enabled = await openLegalTab({}, { '/auth/methods': { legal: { enabled: true, consent_version: 4 } } })
    fireEvent.click(enabled)
    echo()
    save()
    await waitFor(() => expect(useSiteStore.getState().legalEnabled).toBe(true))
    expect(useSiteStore.getState().legalConsentVersion).toBe(4)
    // A failing re-read is not worth a toast: the save already said what it did.
    expect(api.get).toHaveBeenCalledWith('/auth/methods', expect.objectContaining({ _skipErrorToast: true }))
  })

  it('switched on with nothing published, the server says the page is not live, so no link to a 404 appears', async () => {
    useSiteStore.setState({ legalEnabled: false, legalConsentVersion: 0 })
    fireEvent.click(await openLegalTab({}, { '/auth/methods': { legal: { enabled: false, consent_version: 0 } } }))
    echo()
    save()
    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/auth/methods', expect.anything()))
    await new Promise(r => setTimeout(r, 20))
    expect(useSiteStore.getState().legalEnabled).toBe(false)
  })

  it('a failed re-read leaves what the site store had, rather than guessing from the switch', async () => {
    useSiteStore.setState({ legalEnabled: false, legalConsentVersion: 0 })
    fireEvent.click(await openLegalTab())
    echo()
    save()
    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/auth/methods', expect.anything()))
    await new Promise(r => setTimeout(r, 20))
    expect(useSiteStore.getState().legalEnabled).toBe(false)
  })
})
