import axios, { AxiosHeaders, type AxiosAdapter } from 'axios'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { accessControlFixtureSeed } from '@/test/accessControlFixtures'
import { createAccessControlMock } from './accessControlMock'
import { policyTemplates } from '@/views/admin/accessControl/policies/templates'

function harness(scenario: 'normal' | 'empty' | 'error' | 'catalog-missing' | 'catalog-failed' = 'normal') {
  const fallback = vi.fn<AxiosAdapter>(async config => ({ data: 'live', status: 200, statusText: 'OK', headers: new AxiosHeaders(), config }))
  const adapter = createAccessControlMock(fallback, { scenario, latency: 0 })
  const client = axios.create({ baseURL: '/panel/api', adapter })
  return { client, fallback }
}

afterEach(() => vi.useRealTimers())

describe('reproducible access-control acceptance fixtures', () => {
  it('covers the final-plan matrix without sharing mutable data between sessions', () => {
    const seed = accessControlFixtureSeed(1_800_000_000_000)
    expect(seed.policies).toHaveLength(8)
    expect(new Set(seed.policies.map(p => p.action))).toEqual(new Set(['allow', 'block', 'observe']))
    expect(seed.policies.some(p => !p.enabled)).toBe(true)
    expect(seed.policies.some(p => p.list_states.some(l => l.state === 'pending'))).toBe(true)
    expect(seed.lists).toHaveLength(5)
    expect(seed.lists.map(l => l.state)).toEqual(expect.arrayContaining(['pending', 'failed']))
    expect(seed.allowlistGroups.map(g => g.stage)).toEqual(['trial', 'enforce'])
    expect(seed.hits).toHaveLength(300)
    expect(seed.hits.some(h => h.policy_id === 999 && h.policy_name === null)).toBe(true)
    expect(seed.hits.some(h => h.domain.length > 200)).toBe(true)
    expect(seed.nodes).toHaveLength(12)
    expect(new Set(seed.nodes.map(n => n.state)).size).toBe(10)
    seed.lists[0].entries.push('domain:changed.example')
    expect(accessControlFixtureSeed().lists[0].entries).not.toContain('domain:changed.example')
  })

  it('serves the product templates actual category names', async () => {
    const { client } = harness()
    const catalog = (await client.get('/admin/dest/geosite/categories')).data
    for (const template of policyTemplates) {
      if (!('category' in template)) continue
      expect(catalog.categories.some((c: { name: string }) => c.name === template.category)).toBe(true)
      const preview = await client.post('/admin/dest/lists/preview', { kind: 'geosite', geosite_category: template.category })
      expect(preview.data.entry_count).toBeGreaterThan(0)
    }
  })

  it('passes authentication and unrelated requests through, but never unknown fixture writes', async () => {
    const { client, fallback } = harness()
    await client.post('/auth/local/login', { upn: 'local', password: 'disposable' })
    await client.get('/admin/users')
    await client.get('https://elsewhere.example/api/admin/dest/status')
    expect(fallback).toHaveBeenCalledTimes(3)
    await expect(client.post('/admin/dest/unknown', {})).rejects.toMatchObject({ response: { status: 501 } })
    await expect(client.put('/admin/groups/3', {})).rejects.toMatchObject({ response: { status: 501 } })
    await expect(client.post('/admin/risk-center/users/1/dismiss', {})).rejects.toMatchObject({ response: { status: 501 } })
    expect(fallback).toHaveBeenCalledTimes(3)
    expect((await client.get('/admin/dest/status')).data.nodes).toHaveLength(12)
    expect((await client.get('/panel/api/admin/dest/status')).data.nodes).toHaveLength(12)
  })

  it('binds read-only server fixtures to the same node IDs and prevents real server writes', async () => {
    const { client, fallback } = harness()
    const status = (await client.get('/admin/dest/status')).data
    const servers = (await client.get('/admin/servers', { params: { page: 1, page_size: 25 } })).data
    expect(servers.items).toHaveLength(12)
    for (const server of servers.items) {
      const node = status.nodes.find((n: { panel_id: number }) => n.panel_id === server.id)
      expect(server.name).toBe(node.panel_name)
      expect(server.panel_type).toBe(node.kind === 'psp' ? 'psp' : '3xui')
    }
    const filtered = await client.get('/admin/servers', { params: { keyword: '节点 11' } })
    expect(filtered.data.items.map((s: { id: number }) => s.id)).toEqual([11])
    expect((await client.post('/admin/servers/probe', { id: 11 })).data.ok).toBe(true)
    expect((await client.post('/admin/servers/probe', { id: 10 })).data.ok).toBe(false)
    await expect(client.post('/admin/servers', {})).rejects.toMatchObject({ response: { status: 501 } })
    await expect(client.put('/admin/servers/11', {})).rejects.toMatchObject({ response: { status: 501 } })
    await expect(client.delete('/admin/servers/11')).rejects.toMatchObject({ response: { status: 501 } })
    await expect(client.post('/admin/servers/11/rotate-credential', {})).rejects.toMatchObject({ response: { status: 501 } })
    expect(fallback).not.toHaveBeenCalled()
  })

  it('keeps the server list usable during the synthetic destination-service failure', async () => {
    const { client, fallback } = harness('error')
    expect((await client.get('/admin/servers')).data.items).toHaveLength(12)
    await expect(client.get('/admin/dest/status')).rejects.toMatchObject({ response: { status: 500 } })
    expect(fallback).not.toHaveBeenCalled()
  })

  it('returns fresh responses and keeps previews read-only, with compare-and-swap writes', async () => {
    const { client, fallback } = harness()
    const before = (await client.get('/admin/dest/policies')).data
    const policy = before.block[0]
    const input = { ...policy, name: 'Changed fixture' }
    await client.post('/admin/dest/policies/preview', input)
    expect((await client.get('/admin/dest/policies')).data).toEqual(before)
    const updated = (await client.put(`/admin/dest/policies/${policy.id}`, input)).data
    expect(updated.updated_at).toBeGreaterThan(policy.updated_at)
    await expect(client.put(`/admin/dest/policies/${policy.id}`, input)).rejects.toMatchObject({ response: { status: 409 } })
    before.block.length = 0
    expect((await client.get('/admin/dest/policies')).data.block.length).toBe(3)
    expect(fallback).not.toHaveBeenCalled()
  })

  it('keeps drafts unpublished until the explicit publish action', async () => {
    const { client } = harness()
    const before = (await client.get('/admin/dest/status')).data
    const policy = (await client.get('/admin/dest/policies')).data.block[0]
    await client.put(`/admin/dest/policies/${policy.id}`, { ...policy, enabled: false })
    const pending = (await client.get('/admin/dest/status')).data
    expect(pending.generation).toBeGreaterThan(before.generation)
    expect(pending.published_generation).toBe(before.published_generation)
    const published = (await client.post('/admin/dest/publish')).data
    expect(published.generation).toBe(published.published_generation)
  })

  it('rejects duplicate exemption creation and missing edits/deletes without mutating definitions', async () => {
    const { client, fallback } = harness()
    const before = (await client.get('/admin/dest/exemptions')).data
    const generation = (await client.get('/admin/dest/status')).data.generation
    await expect(client.post('/admin/dest/exemptions', { user_id: 1, reason: 'Duplicate', expires_at: null }))
      .rejects.toMatchObject({ response: { status: 409, data: { error: 'dest_exemption_exists' } } })
    await expect(client.put('/admin/dest/exemptions/99', { reason: 'Missing', expires_at: null })).rejects.toMatchObject({ response: { status: 404 } })
    await expect(client.delete('/admin/dest/exemptions/99')).rejects.toMatchObject({ response: { status: 404 } })
    expect((await client.get('/admin/dest/exemptions')).data).toEqual(before)
    expect((await client.get('/admin/dest/status')).data.generation).toBe(generation)
    expect(fallback).not.toHaveBeenCalled()
  })

  it('preserves exemption attribution on edits and does not publish no-op edits', async () => {
    const { client } = harness()
    const before = (await client.get('/admin/dest/exemptions/1')).data
    const input = { reason: 'Edited diagnostics', expires_at: null }
    const updated = (await client.put('/admin/dest/exemptions/1', input)).data
    expect(updated).toMatchObject({ ...input, created_at: before.created_at, created_by: before.created_by, created_by_upn: before.created_by_upn })
    const generation = (await client.get('/admin/dest/status')).data.generation
    expect((await client.put('/admin/dest/exemptions/1', input)).data).toEqual(updated)
    expect((await client.get('/admin/dest/status')).data.generation).toBe(generation)
  })

  it('validates exemption reason and expiry like the administrator API', async () => {
    const { client, fallback } = harness('empty')
    for (const [input, field] of [
      [{ user_id: 1, reason: '😀'.repeat(256), expires_at: null }, 'reason'],
      [{ user_id: 1, reason: 'Reason', expires_at: 'tomorrow' }, 'expires_at'],
      [{ user_id: 1, reason: 'Reason', expires_at: 1.5 }, 'expires_at'],
    ] as const) {
      await expect(client.post('/admin/dest/exemptions', input)).rejects.toMatchObject({ response: { status: 400, data: { error: 'dest_policy_invalid', field } } })
    }
    expect((await client.get('/admin/dest/exemptions')).data.items).toHaveLength(0)
    expect(fallback).not.toHaveBeenCalled()
  })

  it('shows explicit queued catalog refresh and its success or failure without real requests', async () => {
    vi.useFakeTimers()
    for (const scenario of ['catalog-missing', 'catalog-failed'] as const) {
      const { client, fallback } = harness(scenario)
      await expect(client.get('/admin/dest/geosite/categories')).rejects.toMatchObject({ response: { status: 503, data: { refreshing: false } } })
      expect((await client.post('/admin/dest/geosite/refresh')).status).toBe(202)
      await expect(client.get('/admin/dest/geosite/categories')).rejects.toMatchObject({ response: { data: { refreshing: true } } })
      vi.advanceTimersByTime(2_000)
      if (scenario === 'catalog-missing') {
        expect((await client.get('/admin/dest/geosite/categories')).data.categories.length).toBeGreaterThan(0)
      } else {
        await expect(client.get('/admin/dest/geosite/categories')).rejects.toMatchObject({ response: { data: { refreshing: false, last_error: 'dest_list_fetch_failed' } } })
      }
      expect(fallback).not.toHaveBeenCalled()
    }
  })

  it('cancels before mutating fixtures and does not leave cancellation timers behind', async () => {
    vi.useFakeTimers()
    const fallback = vi.fn<AxiosAdapter>()
    const adapter = createAccessControlMock(fallback, { latency: 300, scenario: 'normal' })
    const client = axios.create({ adapter })
    const controller = new AbortController()
    const request = client.delete('/admin/dest/policies/101', { signal: controller.signal })
    const rejected = expect(request).rejects.toMatchObject({ code: 'ERR_CANCELED' })
    controller.abort()
    await rejected
    expect(vi.getTimerCount()).toBe(0)
    const read = client.get('/admin/dest/policies')
    await vi.advanceTimersByTimeAsync(300)
    expect((await read).data.allow.some((p: { id: number }) => p.id === 101)).toBe(true)
  })

  it('provides bounded list/report details and isolated empty/error states', async () => {
    const normal = harness().client
    const detail = (await normal.get('/admin/dest/lists/4')).data
    expect(detail.entry_count).toBe(612)
    expect(detail.entries.length).toBeLessThanOrEqual(200)
    expect(detail.parse_report.ignored_broad).toBe(1)
    expect(detail.entries).not.toContain('domain:hsbc')
    expect((await normal.get('/admin/risk-center/users/1')).data.user.upn).toBe('fixture-1@example.invalid')
    const empty = harness('empty').client
    expect((await empty.get('/admin/dest/status')).data.nodes).toEqual([])
    expect((await empty.get('/admin/dest/lists')).data.items).toEqual([])
    expect((await empty.get('/admin/groups')).data.total).toBe(0)
    const error = harness('error')
    await expect(error.client.get('/admin/dest/status')).rejects.toMatchObject({ response: { status: 500, data: { error: 'fixture_unavailable' } } })
    expect(error.fallback).not.toHaveBeenCalled()
  })

  it('saves global exceptions entirely within the seed and creates a first-use pair atomically', async () => {
    const { client, fallback } = harness('empty')
    const saved = (await client.post('/admin/dest/exceptions', { target: 'trusted.example', scope: 'global', match: 'site' })).data
    expect(saved.created).toEqual({ list_id: saved.list_id, policy_id: saved.policy_id })
    const policies = (await client.get('/admin/dest/policies')).data
    expect(policies.allow).toHaveLength(1)
    expect(policies.allow[0].list_ids).toEqual([saved.list_id])
    expect((await client.get(`/admin/dest/lists/${saved.list_id}`)).data.entries).toContain('domain:trusted.example')
    await client.post('/admin/dest/exceptions', { target: 'trusted.example', scope: 'global', match: 'site' })
    expect((await client.get('/admin/dest/policies')).data.allow).toHaveLength(1)
    expect(fallback).not.toHaveBeenCalled()
  })
})
