// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { UISettings } from '@/api/settings'
import { api, mountAdmin } from './adminTestHarness'
import { echoPut, group, reads, uiSettings } from './settings/testSupport'
import SettingsView from './SettingsView'

const policyFields = [
  ['node_task_offline_reconcile_days', 'Offline auto-reconciliation window'],
  ['node_task_backup_restore_days', 'Maximum backup restore age'],
  ['node_task_result_retention_days', 'Full task-result retention'],
] as const
type PolicyField = typeof policyFields[number][0]
const labelOf = (field: PolicyField) => policyFields.find(f => f[0] === field)![1]

const box = (name: string) => screen.getByRole('textbox', { name }) as HTMLInputElement
const input = (field: PolicyField) => box(labelOf(field))
function edit(field: PolicyField, value: number | string) {
  fireEvent.change(input(field), { target: { value: String(value) } })
}
const saveBar = () => screen.queryByRole('region', { name: 'You have unsaved changes' })
const saveButton = () => within(screen.getByRole('region', { name: 'You have unsaved changes' })).getByRole('button', { name: 'Save' })
const save = () => fireEvent.click(saveButton())

async function mountSettings(settings: Partial<UISettings> = {}, route = '/admin/settings') {
  reads({}, settings)
  const view = mountAdmin(<SettingsView />, route)
  await screen.findByRole('textbox', { name: labelOf('node_task_offline_reconcile_days') })
  return view
}

describe('native-task lifecycle settings', () => {
  it('shows the server defaults with their unit and the conservative policy notices', async () => {
    await mountSettings()

    // The read goes through the query cache, so it carries an AbortSignal config: the assertion is about the endpoint.
    expect(api.get).toHaveBeenCalledWith('/admin/settings/ui', expect.anything())
    expect(input('node_task_offline_reconcile_days').value).toBe('30')
    expect(input('node_task_backup_restore_days').value).toBe('30')
    expect(input('node_task_result_retention_days').value).toBe('90')
    expect(screen.getByText('Native-task lifecycle policy')).toBeTruthy()
    expect(screen.getByText(/Global policy configuration in days/)).toBeTruthy()
    expect(screen.getByText(/Smaller windows affect new task policies only/)).toBeTruthy()
    expect(saveBar()).toBeNull()
  })

  it('loads configured policy values, sends the exact edited API fields, and adopts the saved response', async () => {
    await mountSettings({ node_task_offline_reconcile_days: 14, node_task_backup_restore_days: 21, node_task_result_retention_days: 60 })
    expect(input('node_task_offline_reconcile_days').value).toBe('14')
    expect(input('node_task_backup_restore_days').value).toBe('21')
    expect(input('node_task_result_retention_days').value).toBe('60')
    api.put.mockImplementationOnce(async (_url: string, data: UISettings) => ({ data: { ...data, node_task_result_retention_days: 180 } }))

    edit('node_task_offline_reconcile_days', 45)
    edit('node_task_backup_restore_days', 60)
    edit('node_task_result_retention_days', 120)
    save()

    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(api.put).toHaveBeenCalledWith('/admin/settings/ui', expect.objectContaining({
      node_task_offline_reconcile_days: 45,
      node_task_backup_restore_days: 60,
      node_task_result_retention_days: 120,
    }))
    await waitFor(() => expect(input('node_task_result_retention_days').value).toBe('180'))
  })

  it('uses defaults only for keys omitted by an older server, and keeps an explicit zero as it is, flagged', async () => {
    const stored: Partial<UISettings> = { node_task_offline_reconcile_days: 0 }
    reads({ '/admin/settings/ui': (() => { const s = uiSettings(stored) as Partial<UISettings>; delete s.node_task_backup_restore_days; delete s.node_task_result_retention_days; return s })() })
    mountAdmin(<SettingsView />, '/admin/settings')
    await screen.findByRole('textbox', { name: labelOf('node_task_offline_reconcile_days') })

    expect(input('node_task_offline_reconcile_days').value).toBe('0')
    expect(input('node_task_backup_restore_days').value).toBe('30')
    expect(input('node_task_result_retention_days').value).toBe('90')
    // The zero is shown, not replaced, and it is what the form reports as wrong.
    expect(input('node_task_offline_reconcile_days').getAttribute('aria-invalid')).toBe('true')
    // Any other edit then cannot be saved until it is fixed.
    fireEvent.change(box('Reconcile interval'), { target: { value: '9' } })
    save()
    expect(api.put).not.toHaveBeenCalled()
  })

  it.each(policyFields)('blocks every out-of-range or unreadable value for %s before any write', async field => {
    await mountSettings()
    for (const bad of [-1, 0, 3651]) {
      edit(field, bad)
      save()
      expect(api.put).not.toHaveBeenCalled()
      expect(input(field).getAttribute('aria-invalid')).toBe('true')
    }
    // A fraction or an empty box is not even handed to the form: it keeps the last number and says why.
    for (const bad of [1.5, '']) {
      edit(field, bad)
      expect(input(field).getAttribute('aria-invalid')).toBe('true')
    }
    expect(api.put).not.toHaveBeenCalled()
  })

  it.each([1, 3650])('accepts the inclusive %i-day boundary for all policy fields', async days => {
    await mountSettings()
    echoPut()
    for (const [field] of policyFields) edit(field, days)
    save()

    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(api.put).toHaveBeenCalledWith('/admin/settings/ui', expect.objectContaining({
      node_task_offline_reconcile_days: days,
      node_task_backup_restore_days: days,
      node_task_result_retention_days: days,
    }))
  })

  it('requires retention to cover each window independently', async () => {
    await mountSettings()
    for (const field of ['node_task_offline_reconcile_days', 'node_task_backup_restore_days'] as const) {
      edit(field, 91)
      save()
      expect(api.put).not.toHaveBeenCalled()
      expect(screen.getAllByText(/must cover both the offline reconciliation window/).length).toBeGreaterThan(0)
      edit(field, 30)
    }
    edit('node_task_result_retention_days', 29)
    save()
    expect(api.put).not.toHaveBeenCalled()
    expect(screen.getAllByText(/must cover both the offline reconciliation window/).length).toBeGreaterThan(0)
  })
})

// The detector policy moved to the risk center's policy tab, which owns it on the server too (the settings PUT keeps
// whatever policy it loaded). A copy of those fields left here would be a second editor whose saves are silently
// discarded, so the page keeps none, only the way there.
describe('the risk policy lives in the risk center', () => {
  it('shows no detector section in General, only a pointer to the policy tab', async () => {
    await mountSettings()

    for (const section of ['Concurrent-location detection', 'Risk signals', 'Risk center']) {
      expect(screen.queryByText(section), section).toBeNull()
    }
    expect(screen.getByText('Concurrent-location and risk-signal settings are in Risk center › Policy.')).toBeTruthy()
    expect(screen.getByText('Open policy').closest('a')?.getAttribute('href')).toBe('/admin/risk?tab=policy')
  })

  it("offers only notify and emergency in a group's General audience", async () => {
    reads({
      '/admin/groups/1/scope-settings': {
        overrides: {},
        overridable: ['notify.expire_before_days', 'security.emergency_access_enabled', 'geo_anomaly.max_places', 'geo_anomaly.ban_enabled', 'risk.min_days', 'risk.usage_ratio'],
      },
    })
    mountAdmin(<SettingsView />, '/admin/settings')
    const audience = await screen.findByRole('combobox', { name: 'Audience' })
    await screen.findByRole('option', { name: group.name })
    fireEvent.change(audience, { target: { value: String(group.id) } })

    expect(await screen.findByText('Notification thresholds')).toBeTruthy()
    expect(screen.getByText('Emergency access (over-limit break-glass)')).toBeTruthy()
    for (const text of ['Concurrent-location detection', 'Concurrent locations · auto-suspension', 'Risk signals (observe only)']) {
      expect(screen.queryByText(text), text).toBeNull()
    }
  })
})

describe('read failure', () => {
  it('reports a failed settings read instead of spinning forever', async () => {
    api.get.mockRejectedValue(new Error('offline'))
    mountAdmin(<SettingsView />, '/admin/settings')
    expect(await screen.findByText('Settings are temporarily unavailable')).toBeTruthy()
    expect(screen.getByRole('button', { name: /retry/i })).toBeTruthy()
  })

  it('reports a failed mail read instead of spinning forever', async () => {
    reads({ '/admin/settings/mail': new Error('offline') })
    mountAdmin(<SettingsView />, '/admin/settings?tab=mail')
    expect(await screen.findByText('Mail settings are temporarily unavailable')).toBeTruthy()
  })

  it('reports a failed SSO read instead of spinning forever', async () => {
    reads({ '/admin/settings/saml': new Error('offline'), '/admin/settings/oidc': new Error('offline') })
    mountAdmin(<SettingsView />, '/admin/settings?tab=sso')
    expect(await screen.findByText('SAML configuration is temporarily unavailable')).toBeTruthy()
  })
})

describe('Cloudflare auto-fetch setting', () => {
  it('reads as on when the server does not say, and sends the switch the admin sets', async () => {
    await mountSettings()
    const toggle = screen.getByRole('switch', { name: 'Fetch Cloudflare address ranges automatically' })
    expect(toggle.getAttribute('aria-checked')).toBe('true')
    echoPut()
    fireEvent.click(toggle)
    save()
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(api.put).toHaveBeenCalledWith('/admin/settings/ui', expect.objectContaining({ cloudflare_ips_auto_fetch: false }))
  })
})

describe('the page frame', () => {
  it('keeps the section in ?tab= and the old values still open their sections', async () => {
    for (const [tab, heading] of [['security', 'Account lockout'], ['brand', 'Site text'], ['subscription', 'Abuse protection'], ['portal', 'Quick links']] as const) {
      const { unmount } = await (async () => { reads(); const v = mountAdmin(<SettingsView />, `/admin/settings?tab=${tab}`); await screen.findByText(heading); return v })()
      unmount()
    }
  })

  it('shows the sections in the order of the navigation', async () => {
    reads(); mountAdmin(<SettingsView />, '/admin/settings')
    await screen.findByRole('textbox', { name: labelOf('node_task_offline_reconcile_days') })
    const names = within(screen.getByRole('navigation', { name: 'Settings sections' })).getAllByRole('button').map(b => b.textContent)
    expect(names).toEqual(['General', 'Login & Security', 'Brand', 'Subscription', 'User portal', 'Mail', 'SSO', 'Privacy and terms'])
  })

  it('asks once when leaving with unsaved changes and names the sections', async () => {
    const { router } = await mountSettings()
    edit('node_task_offline_reconcile_days', 31)
    // Another section is edited too.
    fireEvent.click(within(screen.getByRole('navigation', { name: 'Settings sections' })).getByRole('button', { name: /^Brand/ }))
    fireEvent.change(await screen.findByRole('textbox', { name: 'Site title' }), { target: { value: 'New' } })
    void router.navigate('/elsewhere')
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog.textContent).toContain('General, Brand')
  })
})
