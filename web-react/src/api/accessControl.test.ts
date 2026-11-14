import { beforeEach, describe, expect, it, vi } from 'vitest'

const http = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('./client', () => ({ client: http }))

import * as api from './accessControl'

// A Go nil slice is JSON null. The page maps over these everywhere, so the API
// layer turns every null or missing array into [] (review blocker 1).
beforeEach(() => vi.clearAllMocks())
const ok = (data: unknown) => ({ data })

describe('null arrays become []', () => {
  it('policies and their nested arrays', async () => {
    const p = { id: 1, list_ids: null, group_ids: null, panel_ids: null, list_states: null, inline: { cidrs: null, protocols: null } }
    http.get.mockResolvedValue(ok({ allow: null, block: [p], observe: null, allowlist_groups: null, budget: {} }))
    const r = await api.getDestPolicies()
    expect(r.allow).toEqual([]); expect(r.observe).toEqual([]); expect(r.allowlist_groups).toEqual([])
    expect(r.block[0]).toMatchObject({ list_ids: [], group_ids: [], panel_ids: [], list_states: [] })
    expect(r.block[0].inline.cidrs).toEqual([]); expect(r.block[0].inline.protocols).toEqual([])
  })
  it('lists, used_by, attrs, detail samples and reports', async () => {
    http.get.mockResolvedValueOnce(ok({ items: [{ id: 1, used_by: null, geosite_attrs: null }] }))
    const l = await api.getDestLists()
    expect(l.items[0].used_by).toEqual([]); expect(l.items[0].geosite_attrs).toEqual([])
    http.get.mockResolvedValueOnce(ok({ list: { id: 1, used_by: null }, samples: null }))
    expect((await api.getDestList(1)).samples).toEqual([])
    http.post.mockResolvedValueOnce(ok({ report: { samples: null, warnings: null }, entries: null }))
    const pv = await api.previewDestList({ kind: 'custom' })
    expect(pv.entries).toEqual([]); expect(pv.report?.samples).toEqual([]); expect(pv.report?.warnings).toEqual([])
    http.post.mockResolvedValueOnce(ok({ list: { id: 1, used_by: null }, report: { samples: null, warnings: null } }))
    const w = await api.createDestList({ kind: 'custom' })
    expect(w.list.used_by).toEqual([]); expect(w.report?.samples).toEqual([])
  })
  it('catalog, exemptions, source rules, categories, geosite results', async () => {
    http.get.mockResolvedValueOnce(ok({ templates: [{ key: 'a', geosite: null, protocols: null }], unavailable: null }))
    const c = await api.getDestCatalog()
    expect(c.templates[0].geosite).toEqual([]); expect(c.templates[0].protocols).toEqual([]); expect(c.unavailable).toEqual([])
    http.get.mockResolvedValueOnce(ok({ items: null }))
    expect(await api.getDestExemptions()).toEqual([])
    http.get.mockResolvedValueOnce(ok({ items: [{ countries: null, cidrs: null }] }))
    const s = await api.getDestSourceRules()
    expect(s.items[0].countries).toEqual([]); expect(s.items[0].cidrs).toEqual([])
    http.get.mockResolvedValueOnce(ok({ rule: { countries: null, cidrs: null }, native: true }))
    expect((await api.getDestSourceRule(1)).rule.cidrs).toEqual([])
    http.get.mockResolvedValueOnce(ok({ categories: [{ name: 'x', attrs: null }] }))
    expect((await api.getGeositeCategories()).categories[0].attrs).toEqual([])
    http.post.mockResolvedValueOnce(ok({ categories: null, to: {} }))
    expect((await api.updateGeosite()).categories).toEqual([])
    http.post.mockResolvedValueOnce(ok({ categories: null, to: {} }))
    expect((await api.rollbackGeosite()).categories).toEqual([])
  })
  it('status, test result and user view', async () => {
    http.get.mockResolvedValueOnce(ok({ nodes: [{ sniffing_insufficient: null, allowlist_groups: null }], policy_notes: null, totals: null }))
    const st = await api.getDestStatus()
    expect(st.nodes[0].sniffing_insufficient).toEqual([]); expect(st.nodes[0].allowlist_groups).toEqual([]); expect(st.policy_notes).toEqual({})
    http.post.mockResolvedValueOnce(ok({ steps: null, notes: null, nodes: null }))
    const t = await api.testDestination({ target: 'a' })
    expect(t.steps).toEqual([]); expect(t.notes).toEqual([]); expect(t.nodes).toEqual([])
    http.get.mockResolvedValueOnce(ok({ user_id: 1, exemption: null, group: null }))
    expect((await api.getDestUser(1)).user_id).toBe(1)
  })
})

describe('the hostname-matching setting', () => {
  it('PUT /servers/:id carries only dest_resolve, and the recording mode only audit_collect', async () => {
    http.put.mockResolvedValue(ok({}))
    await api.setDestResolve(5, 'on_demand')
    expect(http.put).toHaveBeenLastCalledWith('/admin/servers/5', { dest_resolve: 'on_demand' }, expect.anything())
    await api.setAuditCollect(5, 'off')
    expect(http.put).toHaveBeenLastCalledWith('/admin/servers/5', { audit_collect: 'off' }, expect.anything())
  })
  it('the test request carries resolved_ips as given', async () => {
    http.post.mockResolvedValue(ok({ steps: [], notes: ['hostname_needs_resolution'], nodes: [] }))
    const r = await api.testDestination({ target: 'a.example', resolved_ips: ['10.0.0.1'] })
    expect(http.post).toHaveBeenLastCalledWith('/admin/dest/test', { target: 'a.example', resolved_ips: ['10.0.0.1'] }, expect.anything())
    expect(r.notes).toEqual(['hostname_needs_resolution'])
  })
})
