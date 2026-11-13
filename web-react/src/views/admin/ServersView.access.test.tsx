// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as acApi from '@/api/accessControl'
import { renderAccess } from '@/test/accessControlHarness'
import ServersView from './ServersView'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})
const servers = vi.hoisted(() => ({ list: vi.fn(), data: undefined as unknown }))
vi.mock('@/api/servers', async importOriginal => ({ ...await importOriginal<typeof import('@/api/servers')>(), listServers: servers.list }))
vi.mock('@/query/servers', async () => ({
  useServersList: () => ({ data: servers.data, isPending: false, isError: false, isFetching: false, refetch: vi.fn() }),
}))
vi.mock('./ServerAccessDialog', () => ({
  default: ({ node, serverName }: { node: unknown; serverName: string }) => node ? <p data-testid="access-dialog">{serverName}</p> : null,
}))

const row = (id: number, panel_type: string, name: string) => ({
  id, name, url: `https://${name}.example`, panel_type, auth_method: 'token', has_api_token: true, capabilities: [], enabled: true,
})
const node = (panel_id: number, state = 'applied') => ({
  panel_id, agent_id: `a${panel_id}`, panel_name: 'x', kind: 'psp', engine: 'xray', agent_version: '4', supports: { policy: true, hits: false, usage: false },
  collect: '', collect_effective: '', collecting: false, state, fallback_reason: '', over_limit: null, sniffing_insufficient: [],
  minted_at: null, pending_since: null, applied_at: 1, applied_rules: 2, last_report_at: 1, rules_data_version: null, geoip_unavailable: false,
  allowlist_groups: [], hits_24h: null,
})
const st = (over = {}) => ({ generation: 2, published_generation: 2, apply_eta_ms: 0, paused: false, publish_error: null, nodes: [node(1)], policy_notes: {}, totals: {}, ...over }) as unknown as acApi.DestStatus

beforeEach(() => {
  vi.clearAllMocks()
  servers.data = { items: [row(1, 'psp', 'tokyo'), row(2, '3xui', 'usw')], total: 2, page: 1, page_size: 25 }
  vi.mocked(acApi.getDestStatus).mockResolvedValue(st())
})

describe('Servers page: access-control line', () => {
  it('shows on a native row only, and opens the dialog', async () => {
    renderAccess(<ServersView />, { route: '/admin/servers' })
    const line = await screen.findByTestId('access-line')
    expect(screen.getAllByTestId('access-line')).toHaveLength(1)
    expect(line.textContent).toBe('访问控制：已生效')
    fireEvent.click(line)
    expect((await screen.findByTestId('access-dialog')).textContent).toBe('tokyo')
  })

  it('shows nothing when the status cannot be read', async () => {
    vi.mocked(acApi.getDestStatus).mockRejectedValue(new Error('503'))
    renderAccess(<ServersView />, { route: '/admin/servers' })
    await screen.findByText('tokyo')
    await waitFor(() => expect(acApi.getDestStatus).toHaveBeenCalled())
    expect(screen.queryByTestId('access-line')).toBeNull()
  })

  it('shows nothing while the fleet has no policy', async () => {
    vi.mocked(acApi.getDestStatus).mockResolvedValue(st({ generation: 0, published_generation: 0 }))
    renderAccess(<ServersView />, { route: '/admin/servers' })
    await screen.findByText('tokyo')
    await waitFor(() => expect(acApi.getDestStatus).toHaveBeenCalled())
    expect(screen.queryByTestId('access-line')).toBeNull()
  })
})
