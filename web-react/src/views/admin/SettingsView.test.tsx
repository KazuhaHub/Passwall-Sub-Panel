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
    geo_anomaly_scope: '',
    geo_anomaly_max_places: 1,
    geo_anomaly_max_regions: 0,
    geo_anomaly_max_cities: 0,
    geo_anomaly_min_placed_ratio: 0.8,
    geo_anomaly_flag_after_polls: 2,
    geo_anomaly_clear_after_polls: 3,
    geo_anomaly_co_travel: '',
    geo_anomaly_allow_anywhere: false,
    geo_anomaly_ignore_addresses: '',
    geo_anomaly_ban_enabled: false,
    geo_anomaly_ban_max_countries: 0,
    geo_anomaly_ban_max_regions: 0,
    geo_anomaly_ban_max_cities: 0,
    geo_anomaly_ban_after_polls: 0,
    geo_anomaly_ban_duration_minutes: 0,
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

    // The read now goes through the query cache, so it carries an AbortSignal
    // config. The assertion is about the endpoint being read, not the arity.
    expect(api.get).toHaveBeenCalledWith('/admin/settings/ui', expect.anything())
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

describe('concurrent-location settings', () => {
  const geo = (name: string) => screen.getByRole('spinbutton', { name: `admin:settings.geo_anomaly.${name}` }) as HTMLInputElement

  it('shows the ignore-list and auto-suspension fields in All users', async () => {
    await mountSettings(generalSettings({
      geo_anomaly_max_regions: 2,
      geo_anomaly_max_cities: 3,
      geo_anomaly_ignore_addresses: '203.0.113.7\n198.51.100.0/24 # office exit',
      geo_anomaly_ban_enabled: true,
      geo_anomaly_ban_max_countries: 1,
      geo_anomaly_ban_max_regions: 4,
      geo_anomaly_ban_max_cities: 5,
      geo_anomaly_ban_after_polls: 8,
      geo_anomaly_ban_duration_minutes: 90,
    }))

    // An unset scope ('') is judged as city; the select says so rather than
    // showing blank, which would read as "off".
    expect(screen.queryByText('admin:settings.geo_anomaly.scope_city')).not.toBeNull()
    expect(geo('max_regions')?.value).toBe('2')
    expect(geo('max_cities')?.value).toBe('3')
    expect(screen.queryByText('admin:settings.geo_anomaly.effective')).not.toBeNull()

    // The ignore list is a multi-line field: one entry per line, # comments.
    const ignore = screen.getByRole('textbox', { name: 'admin:settings.geo_anomaly.ignore_addresses' }) as HTMLTextAreaElement
    expect(ignore.tagName).toBe('TEXTAREA')
    expect(ignore.value).toBe('203.0.113.7\n198.51.100.0/24 # office exit')

    expect(screen.queryByText('admin:settings.geo_anomaly.ban_section')).not.toBeNull()
    expect(screen.queryByText('admin:settings.geo_anomaly.ban_hint')).not.toBeNull()
    expect((screen.getByRole('switch', { name: 'admin:settings.geo_anomaly.ban_enabled' }) as HTMLInputElement).checked).toBe(true)
    expect(geo('ban_max_countries').value).toBe('1')
    expect(geo('ban_max_regions').value).toBe('4')
    expect(geo('ban_max_cities').value).toBe('5')
    expect(geo('ban_after').value).toBe('8')
    expect(geo('ban_duration').value).toBe('90')
    // The server clamps to 7 days; the field says where the ceiling is.
    expect(geo('ban_duration').max).toBe('10080')
    expect(screen.queryByText('admin:settings.geo_anomaly.ban_effective')).not.toBeNull()
  })

  it.each(['country', 'region', 'off'] as const)('states no three-tier caption under scope %s', async scope => {
    // The captions name all three tiers. Only the (default) city scope judges
    // all three, so under any other scope they would promise a region or city
    // line the detector never draws.
    await mountSettings(generalSettings({ geo_anomaly_scope: scope }))
    expect(screen.queryByText('admin:settings.geo_anomaly.effective')).toBeNull()
    expect(screen.queryByText('admin:settings.geo_anomaly.ban_effective')).toBeNull()
  })

  it('sends the edited geo fields on save', async () => {
    await mountSettings()
    api.put.mockImplementationOnce(async (_url: string, data: UISettings) => ({ data }))

    fireEvent.change(screen.getByRole('textbox', { name: 'admin:settings.geo_anomaly.ignore_addresses' }),
      { target: { value: '203.0.113.7' } })
    fireEvent.click(screen.getByRole('switch', { name: 'admin:settings.geo_anomaly.ban_enabled' }))
    fireEvent.change(geo('max_cities'), { target: { value: '4' } })
    fireEvent.change(geo('ban_duration'), { target: { value: '120' } })
    save()

    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(api.put).toHaveBeenCalledWith('/admin/settings/ui', expect.objectContaining({
      geo_anomaly_ignore_addresses: '203.0.113.7',
      geo_anomaly_ban_enabled: true,
      geo_anomaly_max_cities: 4,
      geo_anomaly_ban_duration_minutes: 120,
    }))
  })
})

describe('read failure', () => {
  it('reports a failed settings read instead of spinning forever', async () => {
    // The loader had no catch: `loading` went false but `settings` stayed null,
    // and the render guard is `loading || !settings` — so a failed read left the
    // page on a spinner that could never resolve.
    api.get.mockRejectedValue(new Error('offline'))
    mount(<SettingsView />)

    await waitFor(() => expect(screen.getByText('admin:settings.load_failed')).toBeTruthy())
  })

  it('reports a failed mail read instead of spinning forever', async () => {
    // Same shape inside the Mail tab: `loading || !mail` with an uncatch'd loader.
    installReads({
      '/admin/settings/ui': generalSettings(),
      '/admin/groups': list([]),
    })
    api.get.mockImplementation(async (url: string) => {
      if (url === '/admin/settings/mail') throw new Error('offline')
      if (url === '/admin/settings/ui') return { data: generalSettings() }
      if (url === '/admin/groups') return { data: list([]) }
      throw new Error(`Unexpected GET ${url}`)
    })
    mount(<SettingsView />)
    fireEvent.click(await screen.findByRole('tab', { name: 'admin:settings.tab_mail' }))

    await waitFor(() => expect(screen.getByText('admin:settings.mail_load_failed')).toBeTruthy())
  })

  it('reports a failed SSO read instead of spinning forever', async () => {
    // Both SSO halves share the shape: `loading || !cfg` with an uncatch'd load.
    const reads: Record<string, unknown> = {
      '/admin/settings/ui': generalSettings(),
      '/admin/groups': list([]),
    }
    api.get.mockImplementation(async (url: string) => {
      if (url in reads) return { data: reads[url] }
      if (url === '/admin/settings/saml' || url === '/admin/settings/oidc') throw new Error('offline')
      throw new Error(`Unexpected GET ${url}`)
    })
    mount(<SettingsView />)
    fireEvent.click(await screen.findByRole('tab', { name: 'admin:settings.tab_sso' }))

    await waitFor(() => expect(screen.getByText('admin:settings.saml_load_failed')).toBeTruthy())
  })
})
