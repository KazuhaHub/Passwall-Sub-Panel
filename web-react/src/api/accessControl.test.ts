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

describe('the allowlist groups API', () => {
  it('reads every group, with the nil arrays of a group that is off made []', async () => {
    http.get.mockResolvedValueOnce(ok({
      items: [{
        group_id: 3, group_name: 'Guests', state: 'off', list_ids: null, lists: null, ineligible: null, pending_nodes: null,
        not_ready_lists: null, would_deny_recent: null, denied_24h: null, updated_at: null,
      }],
    }))
    const r = await api.getDestGroups()
    expect(http.get).toHaveBeenLastCalledWith('/admin/dest/groups', expect.anything())
    expect(r[0]).toMatchObject({ list_ids: [], lists: [], ineligible: [], pending_nodes: [], not_ready_lists: [], would_deny_recent: null })
    http.get.mockResolvedValueOnce(ok({ items: null }))
    expect(await api.getDestGroups()).toEqual([])
  })

  it('one group carries its readiness, nulls made []', async () => {
    http.get.mockResolvedValueOnce(ok({ group_id: 3, lists: null, readiness: { not_ready_lists: null, top_sites: null } }))
    const g = await api.getDestGroup(3)
    expect(http.get).toHaveBeenLastCalledWith('/admin/dest/groups/3', expect.anything())
    expect(g.readiness.not_ready_lists).toEqual([]); expect(g.readiness.top_sites).toEqual([])
  })

  it('PUT sends the state and the lists, and base_hosts only when given', async () => {
    http.put.mockResolvedValue(ok({ group_id: 3, lists: null }))
    await api.putDestGroup(3, { state: 'report', list_ids: [1, 2], base_hosts: ['dns.example'] })
    expect(http.put).toHaveBeenLastCalledWith('/admin/dest/groups/3', { state: 'report', list_ids: [1, 2], base_hosts: ['dns.example'] }, expect.anything())
    await api.putDestGroup(3, { state: 'on', list_ids: [1], updated_at: 99 })
    expect(http.put).toHaveBeenLastCalledWith('/admin/dest/groups/3', { state: 'on', list_ids: [1], updated_at: 99 }, expect.anything())
  })

  it('the preview carries the state and the comma-joined lists, nulls made []', async () => {
    http.get.mockResolvedValueOnce(ok({ members: 5, ineligible: null, nodes_lost: null, not_ready_lists: null, base_hosts: null, base_hosts_source: 'none', would_deny_recent: { days: 7, sites: 1, count: 2, top: null } }))
    const p = await api.previewDestGroup(3, { state: 'report', list_ids: [1, 2] })
    expect(http.get).toHaveBeenLastCalledWith('/admin/dest/groups/3/preview', expect.objectContaining({ params: { state: 'report', list_ids: '1,2' } }))
    expect(p).toMatchObject({ ineligible: [], nodes_lost: [], not_ready_lists: [], base_hosts: [] })
    expect(p.would_deny_recent?.top).toEqual([])
  })

  it('the report asks for days only when given, nulls made []', async () => {
    http.get.mockResolvedValueOnce(ok({ effective_days: 3, items: null, dropped_in_range: 0 }))
    const r = await api.getDestGroupReport(3, 3)
    expect(http.get).toHaveBeenLastCalledWith('/admin/dest/groups/3/report', expect.objectContaining({ params: { days: 3 } }))
    expect(r.items).toEqual([])
    http.get.mockResolvedValueOnce(ok({ effective_days: 7, items: null }))
    await api.getDestGroupReport(3)
    expect(http.get).toHaveBeenLastCalledWith('/admin/dest/groups/3/report', expect.objectContaining({ params: {} }))
  })

  it('a node status names the groups it dropped for weak sniffing, null made []', async () => {
    http.get.mockResolvedValueOnce(ok({ nodes: [{ sniffing_insufficient: null, allowlist_groups: null, sniffing_groups_dropped: null }] }))
    expect((await api.getDestStatus()).nodes[0].sniffing_groups_dropped).toEqual([])
  })

  it('a group exception names the group, and the global one still says global', async () => {
    http.post.mockResolvedValue(ok({ entry: 'domain:a.com', created: null, group_id: 3, list_id: 9 }))
    await api.addDestException({ target: 'a.com', match: 'site', group_id: 3 })
    expect(http.post).toHaveBeenLastCalledWith('/admin/dest/exceptions', { target: 'a.com', match: 'site', scope: 'group', group_id: 3 }, expect.anything())
    await api.addDestException({ target: 'a.com', match: 'site' })
    expect(http.post).toHaveBeenLastCalledWith('/admin/dest/exceptions', { target: 'a.com', match: 'site', scope: 'global' }, expect.anything())
  })
})
