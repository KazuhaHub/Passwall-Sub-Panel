// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mount } from '@/test/adminSaveHarness'
import { useSiteStore } from '@/stores/site'
import SettingsView from './SettingsView'

const adminLegal = (kind: string) => ({ kind, enabled: false, consent_version: 0, documents: [], versions: [], data_collection: {} })

async function openLegalTab(settings: Record<string, unknown> = {}, reads: Record<string, unknown> = {}) {
  installReads({
    ...reads,
    '/admin/settings/ui': { sub_base_url: 'https://panel.example.test', timezone: 'UTC', node_poll_seconds: 30, full_report_seconds: 60, node_task_offline_reconcile_days: 30, node_task_backup_restore_days: 30, node_task_result_retention_days: 90, max_panel_concurrency: 8, audit_retention_days: 90, auth_event_retention_days: 90, sync_task_retention_days: 90, traffic_history_days: 730, expire_before_days: 7, traffic_remain_percent: 10, emergency_access_hours: 1, emergency_access_max_count: 1, sub_clients: [], quick_links: [], ...settings },
    '/admin/groups': list([]),
    '/admin/legal/terms': adminLegal('terms'),
    '/admin/legal/privacy': adminLegal('privacy'),
  })
  mount(<SettingsView />)
  fireEvent.click(await screen.findByRole('tab', { name: 'admin:settings.tab_legal' }))
  return screen.findByRole('switch', { name: 'admin:settings.legal.enabled' }) as Promise<HTMLInputElement>
}

describe('Settings > Privacy and terms tab', () => {
  it('is a tab after SSO, and its switches ride the page\'s save bar', async () => {
    const tabs = (await (async () => { await openLegalTab(); return screen.getAllByRole('tab').map(t => t.textContent) })())
    expect(tabs.slice(-2)).toEqual(['admin:settings.tab_sso', 'admin:settings.tab_legal'])

    const enabled = screen.getByRole('switch', { name: 'admin:settings.legal.enabled' }) as HTMLInputElement
    const usage = screen.getByRole('switch', { name: 'admin:settings.legal.disclose_usage' }) as HTMLInputElement
    expect(enabled.checked).toBe(false)
    expect(usage.checked).toBe(false)
    fireEvent.click(enabled)
    fireEvent.click(usage)
    api.put.mockImplementationOnce(async (_url: string, data: unknown) => ({ data }))
    fireEvent.click(screen.getByRole('button', { name: 'admin:settings.save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/settings/ui', expect.objectContaining({ legal_enabled: true, legal_disclose_usage: true })))
  })

  it('shows the stored switches', async () => {
    const enabled = await openLegalTab({ legal_enabled: true, legal_disclose_usage: true })
    expect(enabled.checked).toBe(true)
    expect((screen.getByRole('switch', { name: 'admin:settings.legal.disclose_usage' }) as HTMLInputElement).checked).toBe(true)
  })

  it('after a save the footer links and the consent dialog follow the server\'s own answer (/auth/methods), not the raw switch', async () => {
    useSiteStore.setState({ legalEnabled: false, legalConsentVersion: 0 })
    await openLegalTab({}, { '/auth/methods': { legal: { enabled: true, consent_version: 4 } } })
    fireEvent.click(screen.getByRole('switch', { name: 'admin:settings.legal.enabled' }))
    api.put.mockImplementationOnce(async (_url: string, data: unknown) => ({ data }))
    fireEvent.click(screen.getByRole('button', { name: 'admin:settings.save' }))
    await waitFor(() => expect(useSiteStore.getState().legalEnabled).toBe(true))
    expect(useSiteStore.getState().legalConsentVersion).toBe(4)
    // A failing re-read is not worth a toast: the save already said what it did.
    expect(api.get).toHaveBeenCalledWith('/auth/methods', expect.objectContaining({ _skipErrorToast: true }))
  })

  it('switched on with nothing published, the server says the page is not live, so no link to a 404 appears', async () => {
    useSiteStore.setState({ legalEnabled: false, legalConsentVersion: 0 })
    await openLegalTab({}, { '/auth/methods': { legal: { enabled: false, consent_version: 0 } } })
    fireEvent.click(screen.getByRole('switch', { name: 'admin:settings.legal.enabled' }))
    api.put.mockImplementationOnce(async (_url: string, data: unknown) => ({ data }))
    fireEvent.click(screen.getByRole('button', { name: 'admin:settings.save' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/auth/methods', expect.anything()))
    await new Promise(r => setTimeout(r, 20))
    expect(useSiteStore.getState().legalEnabled).toBe(false)
  })

  it('a failed re-read leaves what the site store had, rather than guessing from the switch', async () => {
    useSiteStore.setState({ legalEnabled: false, legalConsentVersion: 0 })
    await openLegalTab()
    fireEvent.click(screen.getByRole('switch', { name: 'admin:settings.legal.enabled' }))
    api.put.mockImplementationOnce(async (_url: string, data: unknown) => ({ data }))
    fireEvent.click(screen.getByRole('button', { name: 'admin:settings.save' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/auth/methods', expect.anything()))
    await new Promise(r => setTimeout(r, 20))
    expect(useSiteStore.getState().legalEnabled).toBe(false)
  })
})
