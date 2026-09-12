// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { UISettings } from '@/api/settings'
import { api, installReads, list, mount, snack } from '@/test/adminSaveHarness'
import SettingsView from './SettingsView'

// Only the General tab mounts in these tests. Supply its ordinary controls as
// well as the new policy so an unrelated legacy validator cannot mask a save.
function generalSettings(overrides: Partial<UISettings> = {}): UISettings {
  return {
    sub_base_url: 'https://panel.example.test',
    panel_path: '',
    timezone: 'UTC',
    cron_traffic_pull_minutes: 5,
    cron_reconcile_minutes: 5,
    node_poll_seconds: 30,
    full_report_seconds: 60,
    node_task_offline_reconcile_days: 30,
    node_task_backup_restore_days: 30,
    node_task_result_retention_days: 90,
    max_panel_concurrency: 8,
    audit_retention_days: 90,
    auth_event_retention_days: 90,
    sync_task_retention_days: 90,
    traffic_history_days: 730,
    expire_before_days: 7,
    traffic_remain_percent: 10,
    emergency_access_enabled: false,
    emergency_access_hours: 1,
    emergency_access_max_count: 1,
    emergency_access_quota_gb: 0,
    geo_ip_enabled: false,
    geo_ip_auto_update: false,
    geo_ip_update_source: 'dbip',
    geo_anomaly_max_places: 1,
    geo_anomaly_min_placed_ratio: 0.8,
    geo_anomaly_flag_after_polls: 2,
    geo_anomaly_clear_after_polls: 3,
    geo_anomaly_allow_anywhere: false,
    sub_clients: [],
    quick_links: [],
    ...overrides,
  } as UISettings
}

const policyFields = [
  'node_task_offline_reconcile_days',
  'node_task_backup_restore_days',
  'node_task_result_retention_days',
] as const
type PolicyField = typeof policyFields[number]

function input(field: PolicyField): HTMLInputElement {
  return screen.getByRole('spinbutton', { name: `admin:settings.general.${field}` })
}

function edit(field: PolicyField, value: number | string) {
  fireEvent.change(input(field), { target: { value: String(value) } })
}

function save() {
  fireEvent.click(screen.getByRole('button', { name: 'admin:settings.save' }))
}

async function mountSettings(settings = generalSettings()) {
  installReads({
    '/admin/settings/ui': settings,
    '/admin/groups': list([]),
  })
  mount(<SettingsView />)
  await screen.findByRole('spinbutton', { name: 'admin:settings.general.node_task_offline_reconcile_days' })
}

describe('native-task lifecycle settings', () => {
  it('shows the server defaults with units, bounds, and conservative policy notices', async () => {
    await mountSettings()

    expect(api.get).toHaveBeenCalledWith('/admin/settings/ui')
    expect(input('node_task_offline_reconcile_days').value).toBe('30')
    expect(input('node_task_backup_restore_days').value).toBe('30')
    expect(input('node_task_result_retention_days').value).toBe('90')
    for (const field of policyFields) {
      expect(input(field).min).toBe('1')
      expect(input(field).max).toBe('3650')
      expect(input(field).step).toBe('1')
    }
    expect(screen.getByText('admin:settings.general.section_node_task_lifecycle')).toBeTruthy()
    expect(screen.getByText('admin:settings.general.node_task_lifecycle_hint')).toBeTruthy()
    expect(screen.getByText('admin:settings.general.node_task_policy_safety_hint')).toBeTruthy()
  })

  it('loads configured policy values, sends the exact edited API fields, and adopts the saved response', async () => {
    await mountSettings(generalSettings({
      node_task_offline_reconcile_days: 14,
      node_task_backup_restore_days: 21,
      node_task_result_retention_days: 60,
    }))
    expect(input('node_task_offline_reconcile_days').value).toBe('14')
    expect(input('node_task_backup_restore_days').value).toBe('21')
    expect(input('node_task_result_retention_days').value).toBe('60')
    api.put.mockImplementationOnce(async (_url: string, data: UISettings) => ({
      data: { ...data, node_task_result_retention_days: 180 },
    }))

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
    expect(snack).toHaveBeenCalledWith('admin:settings.saved', 'success')
  })

  it('uses defaults only for keys omitted by an older server, not an explicit zero', async () => {
    const settings = generalSettings({ node_task_offline_reconcile_days: 0 })
    // The HTTP shape of an older server can omit these newer required DTO keys.
    const olderResponse: Partial<UISettings> = { ...settings }
    delete olderResponse.node_task_backup_restore_days
    delete olderResponse.node_task_result_retention_days
    await mountSettings(olderResponse as UISettings)

    expect(input('node_task_offline_reconcile_days').value).toBe('0')
    expect(input('node_task_backup_restore_days').value).toBe('30')
    expect(input('node_task_result_retention_days').value).toBe('90')
    save()
    expect(api.put).not.toHaveBeenCalled()
    expect(snack).toHaveBeenLastCalledWith(expect.stringContaining('admin:settings.general.node_task_days_range'), 'warning')
  })

  it.each(policyFields)('blocks every invalid boundary for %s before any write', async field => {
    await mountSettings()
    for (const invalid of [-1, 0, 1.5, 3651, '']) {
      edit(field, invalid)
      save()
      expect(api.put).not.toHaveBeenCalled()
      expect(snack).toHaveBeenLastCalledWith(
        `admin:settings.general.${field}: admin:settings.general.node_task_days_range`, 'warning',
      )
    }
  })

  it.each([1, 3650])('accepts the inclusive %i-day boundary for all policy fields', async days => {
    await mountSettings()
    api.put.mockImplementationOnce(async (_url: string, data: UISettings) => ({ data }))
    for (const field of policyFields) edit(field, days)
    save()

    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(api.put).toHaveBeenCalledWith('/admin/settings/ui', expect.objectContaining({
      node_task_offline_reconcile_days: days,
      node_task_backup_restore_days: days,
      node_task_result_retention_days: days,
    }))
  })

  it('requires retention to cover each window independently, including across tabs', async () => {
    await mountSettings()
    for (const field of ['node_task_offline_reconcile_days', 'node_task_backup_restore_days'] as const) {
      edit(field, 91)
      save()
      expect(api.put).not.toHaveBeenCalled()
      expect(snack).toHaveBeenLastCalledWith('admin:settings.general.node_task_retention_minimum', 'warning')
      edit(field, 30)
    }
    edit('node_task_result_retention_days', 29)
    fireEvent.click(screen.getByRole('tab', { name: 'admin:settings.tab_subscription' }))
    save()
    expect(api.put).not.toHaveBeenCalled()
    expect(snack).toHaveBeenLastCalledWith('admin:settings.general.node_task_retention_minimum', 'warning')
  })
})
