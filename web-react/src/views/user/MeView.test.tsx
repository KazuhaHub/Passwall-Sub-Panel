// @vitest-environment jsdom
import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, mountMe, profile, serve } from './testHarness'
import MeView from './MeView'

describe('MeView server status lines', () => {
  const status = (nodes: unknown[]) => serve({ '/user/me/server-status': { nodes } })
  const badges = () => screen.getAllByTestId('traffic-factor-badge').map(b => b.textContent)

  it('renders relay names with independent only-line options and shared entry statuses', async () => {
    status([{ name: 'Parent', region: 'JP', status: 'ok', show_direct: false,
      relay_statuses: [{ name: 'Relay only', only_line_name: true, status: 'down' }, { name: 'Relay combined', status: 'ok' }] }])
    mountMe(<MeView />, '/?tab=status')
    await screen.findByText('Relay only')
    expect(screen.getByText('Parent Relay combined')).toBeTruthy()
    expect(screen.queryByText('Parent')).toBeNull()
    expect(screen.queryByText('Parent Relay only')).toBeNull()
    expect(screen.getAllByText('Down')).toHaveLength(1)
    expect(screen.getAllByText('Up')).toHaveLength(1)
  })

  it('badges only lines whose factor differs from 1000', async () => {
    status([
      { name: 'Plain', region: 'JP', status: 'ok', show_direct: true, traffic_factor_milli: 1000 },
      { name: 'Pricey', region: 'US', status: 'ok', show_direct: true, traffic_factor_milli: 1500 },
      { name: 'Gratis', region: 'DE', status: 'ok', show_direct: true, traffic_factor_milli: 0 },
    ])
    mountMe(<MeView />, '/?tab=status')
    await screen.findByText('Pricey')
    expect(badges()).toEqual(['×1.5', 'Free'])
  })

  it('badges each direct and relay line by its own factor', async () => {
    status([{ name: 'Parent', region: 'JP', status: 'ok', show_direct: true, traffic_factor_milli: 1000,
      relay_statuses: [{ name: 'Cheap', status: 'ok', traffic_factor_milli: 0 }, { name: 'Dear', status: 'ok', traffic_factor_milli: 2000 }, { name: 'Same', status: 'ok', traffic_factor_milli: 1000 }] }])
    mountMe(<MeView />, '/?tab=status')
    await screen.findByText('Parent Cheap')
    expect(badges()).toEqual(['Free', '×2'])
  })

  it.each([
    ['a relay line', [{ name: 'P', region: 'JP', status: 'ok', show_direct: true, traffic_factor_milli: 1000, relay_statuses: [{ name: 'R', status: 'ok', traffic_factor_milli: 1500 }] }]],
    ['a hidden direct line', [{ name: 'P', region: 'JP', status: 'ok', show_direct: false, traffic_factor_milli: 1500, relay_statuses: [{ name: 'R', status: 'ok', traffic_factor_milli: 1000 }] }]],
    ['a visible node', [{ name: 'Pricey', region: 'US', status: 'ok', show_direct: true, traffic_factor_milli: 1500 }]],
  ])('labels used traffic as charged when %s has a custom factor', async (_name, nodes) => {
    status(nodes)
    mountMe(<MeView />)
    await screen.findByText('Period used (charged)')
  })

  it('uses the plain used-traffic label when every factor is default', async () => {
    status([{ name: 'Plain', region: 'JP', status: 'ok', show_direct: true, traffic_factor_milli: 1000 }])
    mountMe(<MeView />)
    await screen.findByText('Period used')
    expect(screen.queryByText('Period used (charged)')).toBeNull()
  })

  it('names a relay row with the backend-resolved node name', async () => {
    status([{ name: 'P ×1', region: 'JP', status: 'ok', show_direct: false, traffic_factor_milli: 1000,
      relay_statuses: [{ name: 'R', node_name: 'P ×2', status: 'ok', traffic_factor_milli: 2000 }] }])
    mountMe(<MeView />, '/?tab=status')
    await screen.findByText('P ×2 R')
  })

  it('shows the empty state when every entry is hidden or inactive', async () => {
    status([{ name: 'Hidden', region: 'JP', status: 'ok', show_direct: false }])
    mountMe(<MeView />, '/?tab=status')
    await screen.findByText('No nodes to show')
    expect(screen.queryByText('Hidden')).toBeNull()
  })

  it('shows coarse quality and rtt on relay lines only when present', async () => {
    status([{ name: 'P', region: 'JP', status: 'ok', show_direct: true,
      relay_statuses: [
        { name: 'Good', only_line_name: true, status: 'ok', quality: 'ok', rtt_ms: 42.4 },
        { name: 'Poor', only_line_name: true, status: 'ok', quality: 'degraded', rtt_ms: 180 },
        { name: 'Dead', only_line_name: true, status: 'ok', quality: 'down' },
        { name: 'Unknown', only_line_name: true, status: 'ok' },
      ] }])
    mountMe(<MeView />, '/?tab=status')
    const chips = await screen.findAllByTestId('relay-quality')
    expect(chips.map(c => c.dataset.quality)).toEqual(['ok', 'degraded', 'down'])
    expect(chips[0].textContent).toBe('Good42 ms')
    expect(chips[1].textContent).toBe('Poor180 ms')
    expect(chips[2].textContent).toBe('Unavailable')
  })

  it('keeps legacy direct and relay responses working without the new fields', async () => {
    status([{ name: 'Old', region: 'JP', status: 'unknown', relay_statuses: [{ name: 'Relay', status: 'unknown' }] }])
    mountMe(<MeView />, '/?tab=status')
    await screen.findByText('Old Relay')
    expect(screen.getByText('Old')).toBeTruthy()
    expect(screen.queryByTestId('relay-quality')).toBeNull()
  })
})

describe('MeView load failure', () => {
  it('explains itself with a retry instead of rendering a blank page', async () => {
    api.get.mockRejectedValue(new Error('boom'))
    mountMe(<MeView />)
    await screen.findByText('Could not load this page. Please try again.')
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy()
  })
})

// What geoenforce.go writes into service_disable_detail: Chinese, with the minutes the suspension lasts.
const GEO_AUTO_DETAIL = '检测到账号同时在同一国家的 2 个省或州使用，代理服务已临时暂停，约 60 分钟后自动恢复'
const GEO_AUTO_BODY = 'Your account was used from several places at the same time, so proxy service is paused for a while and will resume automatically. Contact an administrator if you think this is a mistake.'
const geoAuto = { service_status: 'manual_suspended', service_disabled_reason: 'geo_auto', service_disable_detail: GEO_AUTO_DETAIL }

describe('status card: the service axis in words', () => {
  const status = () => within(screen.getByTestId('service-status'))

  it('says the service is working for an ordinary account', async () => {
    serve()
    mountMe(<MeView />)
    await screen.findByText('Service active')
    expect(screen.getByTestId('service-status').dataset.tone).toBe('success')
  })

  it.each([
    ['expired', 'Service expired', 'danger', 'You can still sign in to the panel, but subscription and proxy service are paused.'],
    ['traffic_exceeded', 'Traffic exhausted', 'warning', "This period's traffic limit has been reached, so proxy service is paused."],
    ['blocked_client', 'Client blocked', 'danger', 'A blocked client was detected, so subscription service is paused. Switch to an allowed client or contact an administrator to restore service.'],
    ['manual_suspended', 'Service suspended', 'danger', 'An administrator has paused your subscription and proxy service.'],
  ])('explains %s', async (state, title, tone, body) => {
    serve({}, { service_status: state })
    mountMe(<MeView />)
    await screen.findByText(title)
    expect(status().getByText(body)).toBeTruthy()
    expect(screen.getByTestId('service-status').dataset.tone).toBe(tone)
  })

  it('prefers the access snapshot over the flat status', async () => {
    serve({}, { service_status: 'active', access: { account_state: 'active', service_state: 'expired', can_login: true, can_use_portal: true, can_subscribe: false, proxy_enabled: false } })
    mountMe(<MeView />)
    await screen.findByText('Service expired')
  })

  it('warns in words when the account is about to expire, naming the date', async () => {
    const soon = new Date(Date.now() + 3 * 86400000).toISOString()
    serve({}, { expire_at: soon })
    mountMe(<MeView />)
    await screen.findByText('Expiring soon')
    expect(status().getByText(new RegExp(`in 3 day\\(s\\) \\(${new Date(soon).toLocaleDateString().replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\)`))).toBeTruthy()
  })

  it('warns when most of the traffic is used', async () => {
    serve({ '/user/me/traffic': { ...{ user_id: 1, permanent_total_bytes: 0, today_used_bytes: 0 }, period_used_bytes: 95 * 1024 ** 3 } }, { traffic_limit_bytes: 100 * 1024 ** 3 })
    mountMe(<MeView />)
    await screen.findByText('Traffic almost used up')
    expect(screen.getByText(/95% of this period/)).toBeTruthy()
  })

  it('says emergency access is on for that service state', async () => {
    serve({}, { service_status: 'emergency_active' })
    mountMe(<MeView />)
    await screen.findByText('Emergency access is on')
    expect(screen.getByTestId('service-status').dataset.tone).toBe('info')
  })

  it('makes no claim for a service state it does not know', async () => {
    serve({}, { service_status: 'some_future_state' })
    mountMe(<MeView />)
    await screen.findByText('My usage')
    expect(screen.queryByTestId('service-status')).toBeNull()
  })

  it('follows a service hold to the other tabs', async () => {
    serve({}, { service_status: 'expired' })
    mountMe(<MeView />, '/?tab=clients')
    await screen.findByText('Service expired')
  })

  it('does not carry the all-clear to the other tabs', async () => {
    mountMe(<MeView />, '/?tab=clients')
    await screen.findByRole('tab', { name: 'Clients' })
    expect(screen.queryByTestId('service-status')).toBeNull()
  })

  it('gives a non-Chinese UI the localized geo-hold notice, not the stored Chinese detail', async () => {
    serve({}, geoAuto)
    mountMe(<MeView />)
    await screen.findByText('Service temporarily paused')
    expect(screen.getByText(GEO_AUTO_BODY)).toBeTruthy()
    expect(screen.queryByText(GEO_AUTO_DETAIL)).toBeNull()
  })

  it("still shows an admin's own suspension note", async () => {
    serve({}, { service_status: 'manual_suspended', service_disabled_reason: 'service_manual', service_disable_detail: 'Unpaid invoice — contact billing' })
    mountMe(<MeView />)
    await screen.findByText('Service suspended')
    expect(screen.getByText('Unpaid invoice — contact billing')).toBeTruthy()
    expect(screen.queryByText('Service temporarily paused')).toBeNull()
  })
})

describe('status card while usage is unknown', () => {
  it('shows no all-clear until the usage read has answered, but a hold at once', async () => {
    let release!: () => void
    const gate = new Promise<void>(r => { release = r })
    serve({}, { traffic_limit_bytes: 100 * 1024 ** 3 })
    const reads = api.get.getMockImplementation()!
    api.get.mockImplementation(async (url: string) => { if (url === '/user/me/traffic') await gate; return reads(url) })
    mountMe(<MeView />)
    await screen.findByText('My usage')
    expect(screen.queryByTestId('service-status')).toBeNull()
    release()
    await screen.findByText('Service active')
  })

  it('shows a hold at once, before usage answers', async () => {
    const gate = new Promise<void>(() => {})
    serve({}, { service_status: 'expired', traffic_limit_bytes: 100 * 1024 ** 3 })
    const reads = api.get.getMockImplementation()!
    api.get.mockImplementation(async (url: string) => { if (url === '/user/me/traffic') await gate; return reads(url) })
    mountMe(<MeView />)
    await screen.findByText('Service expired')
  })
})

describe('MeView header', () => {
  it('names the account and shows the sign-in name and email under it', async () => {
    serve({}, { email: 'user@mail.test' })
    mountMe(<MeView />)
    await screen.findByRole('heading', { level: 1, name: 'User' })
    expect(screen.getByText(`${profile.upn} · user@mail.test`)).toBeTruthy()
    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/user/me', expect.anything()))
  })
})
