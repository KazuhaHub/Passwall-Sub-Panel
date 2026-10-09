import { AxiosError, AxiosHeaders, CanceledError, type AxiosAdapter, type AxiosResponse, type InternalAxiosRequestConfig } from 'axios'
import type { AccessControlSettings, DestinationListDetail, DestinationListInput, DestinationListPreview, DestinationListSummary, DestinationPolicyInput, DestinationPolicyOverviewItem, DestinationTestResult, DestinationUserAccessView } from '@/api/accessControl'
import type { RiskUserSummary } from '@/api/riskCenter'
import type { Server } from '@/api/servers'
import { destinationListAvailable } from '@/utils/destinationListAvailability'
import { accessControlFixtureSeed, destinationPolicies, destinationStatus } from '@/test/accessControlFixtures'

export const accessFixtureScenarios = ['normal', 'empty', 'error', 'catalog-missing', 'catalog-failed', 'policy-preview-error', 'policy-over-quota', 'policy-conflict', 'policy-save-pending', 'policy-lists-error', 'policy-groups-error', 'template-over-quota', 'templates-catalog-missing', 'list-original-read-error', 'settings-save-pending', 'list-delete-conflict', 'account-read-error', 'account-cancel-pending', 'node-retry-pending'] as const
export type AccessFixtureScenario = typeof accessFixtureScenarios[number]

function savedScenario(): AccessFixtureScenario {
  try {
    const value = localStorage.getItem('psp_dev_access_state')
    if (accessFixtureScenarios.includes(value as AccessFixtureScenario)) return value as AccessFixtureScenario
  } catch { /* Storage may be unavailable; the normal seed remains usable. */ }
  return 'normal'
}

function fixturePath(config: InternalAxiosRequestConfig): string | null {
  const url = config.url ?? ''
  // Absolute requests belong to their chosen transport, even when their path
  // resembles a fixture endpoint. The shared product client uses a local base.
  if (/^(?:[a-z][\w+.-]*:)?\/\//i.test(url) || /^(?:[a-z][\w+.-]*:)?\/\//i.test(config.baseURL ?? '')) return null
  const path = url.split(/[?#]/, 1)[0]
  if (path.startsWith('/admin/')) return path
  const base = (config.baseURL ?? '/api').replace(/\/$/, '')
  if (path.startsWith(`${base}/admin/`)) return path.slice(base.length)
  return null
}

async function delay(config: InternalAxiosRequestConfig, latency: number) {
  if (config.signal?.aborted) throw new CanceledError('Fixture request canceled', config)
  if (latency <= 0) return
  await new Promise<void>((resolve, reject) => {
    const finish = () => { config.signal?.removeEventListener?.('abort', abort); resolve() }
    const timer = setTimeout(finish, latency)
    const abort = () => {
      clearTimeout(timer)
      config.signal?.removeEventListener?.('abort', abort)
      reject(new CanceledError('Fixture request canceled', config))
    }
    config.signal?.addEventListener?.('abort', abort)
    if (config.signal?.aborted) abort()
  })
}

// In-memory UI acceptance transport; it neither authenticates users nor models
// kernel enforcement. Those checks use the real PSP/Node acceptance gates.
export function createAccessControlMock(fallback: AxiosAdapter, options: { scenario?: AccessFixtureScenario; latency?: number } = {}): AxiosAdapter {
  const scenario = options.scenario ?? savedScenario()
  const seed = accessControlFixtureSeed()
  if (scenario === 'empty') {
    seed.policies = []; seed.lists = []; seed.nodes = []; seed.groups = []; seed.allowlistGroups = []; seed.exemptions = []; seed.hits = []
    for (const value of Object.values(seed.budget)) value.used = 0
  }
  if (scenario === 'template-over-quota') {
    seed.policies = []
    seed.budget.regexps = { used: 250, limit: 256 }
  }
  if (scenario === 'templates-catalog-missing') seed.policies = []
  const servers: Server[] = seed.nodes.map(node => ({
    id: node.panel_id, name: node.panel_name, panel_type: node.kind === 'psp' ? 'psp' : '3xui',
    url: `https://fixture-node-${node.panel_id}.example.invalid`, capabilities: ['inbound.read', 'inbound.update'],
    has_api_token: true, has_password: false, auth_method: 'token', insecure_https: false,
    panel_version: node.agent_version ?? undefined,
    core_engine: node.kind === 'psp' ? node.engine === 'sing-box' ? 'sing-box' : 'xray' : undefined,
    core_version: node.kind === 'psp' ? 'fixture-core' : undefined,
    version_checked_at: new Date().toISOString(), audit_collect: 'off',
  }))
  let generation = 12, publishedGeneration = 12, paused = false, lastWrite = Date.now() - 3_600_000
  let published = structuredClone(seed.policies)
  let conflictInjected = false
  const failedOriginalReads = new Set<number>()
  const failedAccountReads = new Set<number>()
  let catalogAvailable = !['catalog-missing', 'catalog-failed', 'templates-catalog-missing'].includes(scenario)
  let catalogDue = 0, catalogError = ''
  const defaults: AccessControlSettings = { dest_hit_retention_days: 30, dest_trial_retention_days: 7, dest_usage_retention_days: 7, dest_list_refresh_hours: 17, dest_policy_apply_min_seconds: 93 }
  let settings: AccessControlSettings = { ...defaults }
  let freshListReferences = false
  const tick = () => { lastWrite = Math.max(Date.now(), lastWrite + 1); generation++; return lastWrite }
  const publication = () => ({ generation, published_generation: publishedGeneration, paused, publish_error: null })
  const budget = () => ({ ...structuredClone(seed.budget), rules: { used: seed.policies.filter(p => p.enabled).length, limit: seed.budget.rules.limit } })
  const userAccess = (id: number): DestinationUserAccessView => {
    const group = seed.allowlistGroups[id % 2 ? 0 : 1]
    return { group: group ? { id: group.group_id, name: group.name, mode: 'allowlist', stage: group.stage } : null,
      exemption: seed.exemptions.find(e => e.user_id === id) ?? null, hits_available: null, recent_hits: null, usage_available: null, usage_nodes: null }
  }

  return async config => {
    const path = fixturePath(config)
    const owned = path && (/^\/admin\/dest(?:\/|$)/.test(path) || /^\/admin\/risk-center\/users(?:\/|$)/.test(path) || /^\/admin\/groups(?:\/|$)/.test(path) || /^\/admin\/servers(?:\/|$)/.test(path))
    if (!owned) return fallback(config)
    await delay(config, options.latency ?? 250)
    const method = (config.method ?? 'get').toUpperCase()
    const response = (data: unknown, status = 200): AxiosResponse => ({ data: structuredClone(data), status, statusText: String(status), headers: new AxiosHeaders(), config })
    const fail = (status: number, error: string, extra: object = {}): never => {
      throw new AxiosError(error, status >= 500 ? AxiosError.ERR_BAD_RESPONSE : AxiosError.ERR_BAD_REQUEST, config, undefined, response({ error, ...extra }, status))
    }
    const body = (typeof config.data === 'string' ? JSON.parse(config.data || '{}') : config.data ?? {}) as Record<string, unknown>
    if (method === 'GET' && path === '/admin/servers') {
      const keyword = String(config.params?.keyword ?? '').toLowerCase()
      const items = servers.filter(server => `${server.name} ${server.url}`.toLowerCase().includes(keyword))
      const dir = config.params?.sort_dir === 'desc' ? -1 : 1
      items.sort((a, b) => dir * (config.params?.sort_by === 'name' ? a.name.localeCompare(b.name) : a.id - b.id))
      const page = Math.max(1, Math.floor(Number(config.params?.page) || 1))
      const pageSize = Math.min(200, Math.max(1, Math.floor(Number(config.params?.page_size) || 25)))
      return response({ items: items.slice((page - 1) * pageSize, page * pageSize), total: items.length, page, page_size: pageSize })
    }
    if (method === 'POST' && path === '/admin/servers/probe') {
      const node = seed.nodes.find(node => node.panel_id === body.id) ?? fail(404, 'not_found')
      return response({ ok: node.state !== 'offline', error: node.state === 'offline' ? 'Fixture node is offline' : undefined, inbound_count: 1,
        panel_version: node.agent_version ?? undefined })
    }
    if (scenario === 'error') return fail(500, 'fixture_unavailable')
    if (scenario === 'policy-lists-error' && method === 'GET' && path === '/admin/dest/lists' || scenario === 'policy-groups-error' && method === 'GET' && path === '/admin/groups') return fail(503, 'fixture_unavailable')
    if (scenario === 'policy-save-pending' && (method === 'POST' && path === '/admin/dest/policies' || method === 'PUT' && /^\/admin\/dest\/policies\/\d+$/.test(path))) await delay(config, 30_000)
    const summary = (list: DestinationListDetail): DestinationListSummary => {
      const { entries: _entries, source_text: _source, content_sha256: _digest, entry_types: _types, parse_report, ...rest } = list
      const used_by: DestinationListSummary['used_by'] = seed.policies.filter(p => p.list_ids.includes(list.id)).map(p => ({ kind: 'policy', id: p.id, name: p.name }))
      if (list.owner_group_id) used_by.push({ kind: 'group', id: list.owner_group_id, name: seed.groups.find(g => g.id === list.owner_group_id)?.name ?? '' })
      const report = parse_report && { accepted: parse_report.accepted, ignored: parse_report.ignored, ignored_broad: parse_report.ignored_broad, rewritten: parse_report.rewritten }
      return { ...rest, used_by: scenario === 'list-delete-conflict' && list.id === 1 && !freshListReferences ? [] : used_by, parse_report_summary: report }
    }
    const findList = (id: number) => seed.lists.find(l => l.id === id) ?? fail(404, 'not_found')
    const findPolicy = (id: number) => seed.policies.find(p => p.id === id) ?? fail(404, 'not_found')
    const cas = (updatedAt: number, error: 'dest_policy_stale' | 'dest_list_stale') => { if (body.updated_at !== updatedAt) fail(409, error) }
    const preview = (input: DestinationListInput): DestinationListPreview => {
      let entries: string[], report: DestinationListDetail['parse_report']
      if (input.kind === 'geosite') {
        if (!catalogAvailable) fail(503, 'dest_geosite_missing')
        const category = input.geosite_category ?? ''
        if (category === 'category-finance') {
          entries = [...accessControlFixtureSeed().lists[3].entries]
          report = accessControlFixtureSeed().lists[3].parse_report!
        } else if (category === 'category-cryptocurrency' || category === 'category-porn') {
          entries = Array.from({ length: category === 'category-cryptocurrency' ? 235 : 300 }, (_, i) =>
            scenario === 'template-over-quota' && category === 'category-porn' && i < 18 ? `regexp:^category-${i}\\.example$` : `domain:category-${i}.example`)
          report = { accepted: entries.length, ignored: 0, ignored_broad: 0, rewritten: 0, samples: [] }
        } else return fail(422, 'dest_geosite_unknown')
      } else if (input.kind === 'remote') {
        return fail(502, 'dest_list_fetch_failed')
      } else {
        entries = [...new Set((input.text ?? '').split(/\r?\n/).map(s => s.trim()).filter(s => s && !s.startsWith('#')))]
        // Synthetic custom lists accept canonical entries only; actual parsing
        // and public-suffix safety are covered by the real service tests.
        if (entries.some(s => !/^(domain|full|regexp|keyword|cidr):.+$/.test(s))) fail(422, 'dest_list_invalid')
        report = { accepted: entries.length, ignored: 0, ignored_broad: 0, rewritten: 0, samples: [] }
      }
      return { entries: entries.slice(0, 50), entry_count: entries.length, regexp_count: entries.filter(s => s.startsWith('regexp:')).length,
        content_sha256: `fixture-${JSON.stringify(entries)}`, parse_report: report!, http_status: 200, bytes: new TextEncoder().encode(entries.join('\n')).length }
    }
    const newList = (input: DestinationListInput): DestinationListDetail => {
      const result = preview(input)
      if (!input.name?.trim() || !result.entry_count) fail(422, 'dest_list_invalid')
      if (seed.lists.some(l => l.name === input.name)) fail(409, 'already_exists')
      return { id: Math.max(0, ...seed.lists.map(l => l.id)) + 1, name: input.name, kind: input.kind, source_url: input.source_url ?? '',
        geosite_category: input.geosite_category ?? '', geosite_attrs: input.geosite_attrs ?? '', owner_group_id: 0,
        updated_at: lastWrite + 1, state: 'ready', last_fetched_at: Date.now(), last_error: '',
        entries: input.kind === 'custom' ? [...new Set((input.text ?? '').split(/\r?\n/).map(s => s.trim()).filter(s => s && !s.startsWith('#')))] : result.entries,
        entry_count: result.entry_count, regexp_count: result.regexp_count, content_sha256: result.content_sha256, parse_report: result.parse_report,
        source_text: input.kind === 'custom' ? input.text : undefined }
    }
    const validatePolicy = (input: DestinationPolicyInput, id?: number) => {
      if (!input.name?.trim() || !['allow', 'block', 'observe'].includes(input.action)) fail(422, 'dest_policy_invalid')
      if (seed.policies.some(p => p.id !== id && p.name === input.name)) fail(409, 'dest_name_taken')
      if (!Array.isArray(input.list_ids) || input.list_ids.some(id => !seed.lists.some(l => l.id === id))) fail(422, 'dest_list_missing')
      if (input.scope === 'groups' && !input.group_ids?.length) fail(422, 'dest_scope_empty')
    }
    const policyFrom = (input: DestinationPolicyInput, id: number, createdAt: number, priority: number): DestinationPolicyOverviewItem => {
      const { new_list: _newList, ...fields } = input
      return { ...fields, id, priority, created_at: createdAt, updated_at: lastWrite, hits_recent: null, last_hit_at: null, scope_missing: false,
        counts_as_risk: input.action === 'block' && input.counts_as_risk,
        list_states: input.list_ids.map(id => { const list = findList(id); return { id, name: list.name, state: list.state, available: destinationListAvailable(list) } }) }
    }
    const detail = (list: DestinationListDetail) => ({ ...list, entries: list.entries.slice(0, 200),
      source_text: config.params?.text ? list.source_text : undefined })
    if (path === '/admin/groups' && method === 'GET') {
      const page = Math.max(1, Number(config.params?.page ?? 1)), pageSize = Math.min(200, Math.max(1, Number(config.params?.page_size ?? 200)))
      return response({ items: seed.groups.slice((page - 1) * pageSize, page * pageSize), total: seed.groups.length, page, page_size: pageSize })
    }
    const groupMatch = path.match(/^\/admin\/groups\/(\d+)$/)
    if (groupMatch && method === 'GET') return response(seed.groups.find(g => g.id === Number(groupMatch[1])) ?? fail(404, 'not_found'))
    if (path === '/admin/dest/status' && method === 'GET') return response(destinationStatus({ ...publication(), nodes: seed.nodes, last_write_at: lastWrite, apply_eta_ms: defaults.dest_policy_apply_min_seconds * 1000 }))
    if (path === '/admin/dest/settings') {
      if (scenario === 'settings-save-pending' && method === 'PUT') await delay(config, 30_000)
      if (method === 'PUT') { settings = { ...settings, ...(body.settings as Partial<AccessControlSettings>) }; tick() }
      if (method === 'GET' || method === 'PUT') return response({ settings, defaults, effective: Object.fromEntries(Object.entries(settings).map(([k, v]) => [k, v || defaults[k as keyof AccessControlSettings]])) })
    }
    if (path === '/admin/dest/geosite/refresh' && method === 'POST') { if (!catalogDue) { catalogDue = Date.now() + 1500; catalogError = '' }; return response({ queued: true }, 202) }
    if (path === '/admin/dest/geosite/categories' && method === 'GET') {
      if (catalogDue && Date.now() >= catalogDue) { catalogDue = 0; catalogAvailable = scenario !== 'catalog-failed'; catalogError = catalogAvailable ? '' : 'dest_list_fetch_failed' }
      if (!catalogAvailable) return fail(503, 'dest_geosite_missing', { refreshing: !!catalogDue, last_error: catalogError })
      return response({ categories: [
        { name: 'category-finance', count: 612, regexp_count: 0, source_count: 613, ignored_broad_count: 1, attrs: ['cn', '!cn'] },
        { name: 'category-cryptocurrency', count: 235, regexp_count: 0, source_count: 235, ignored_broad_count: 0, attrs: [] },
        { name: 'category-porn', count: 300, regexp_count: scenario === 'template-over-quota' ? 18 : 0, source_count: 300, ignored_broad_count: 0, attrs: [] },
      ], updated_at: Date.now(), refreshing: !!catalogDue, last_error: catalogError })
    }
    if (path === '/admin/dest/lists' && method === 'GET') return response({ items: seed.lists.map(summary), refresh_hours: settings.dest_list_refresh_hours || defaults.dest_list_refresh_hours, budget: budget() })
    if (path === '/admin/dest/lists/preview' && method === 'POST') return response(preview(body as unknown as DestinationListInput))
    if (path === '/admin/dest/lists' && method === 'POST') { const list = newList(body as unknown as DestinationListInput); list.updated_at = tick(); seed.lists.push(list); return response(detail(list), 201) }
    const listMatch = path.match(/^\/admin\/dest\/lists\/(\d+)(?:\/(entries|refresh))?$/)
    if (listMatch) {
      const id = Number(listMatch[1]), list = findList(id), operation = listMatch[2]
      if (!operation && method === 'GET') {
        if (scenario === 'list-original-read-error' && Number(config.params?.text) === 1 && !failedOriginalReads.has(id)) {
          failedOriginalReads.add(id)
          return fail(503, 'dest_list_fetch_failed')
        }
        return response(detail(list))
      }
      if (!operation && method === 'DELETE') {
        if (scenario === 'list-delete-conflict' && id === 1) freshListReferences = true
        const references = summary(list).used_by
        if (references.length) return fail(409, 'dest_list_in_use', { used_by: references })
        seed.lists = seed.lists.filter(l => l.id !== id); tick(); return response(null, 204)
      }
      if (!operation && method === 'PUT') {
        cas(list.updated_at, 'dest_list_stale')
        const input = body as unknown as DestinationListInput
        const result = preview(input)
        if (!input.name?.trim()) fail(422, 'dest_list_invalid')
        Object.assign(list, input, result, { id, updated_at: tick(), source_text: input.kind === 'custom' ? input.text : undefined })
        return response(detail(list))
      }
      if (operation === 'refresh' && method === 'POST') { list.last_error = list.kind === 'remote' ? 'dest_list_fetch_failed' : ''; list.state = list.kind === 'remote' ? 'failed' : 'ready'; list.updated_at = tick(); return response({ queued: true }, 202) }
      if (operation === 'entries' && method === 'POST' && list.kind === 'custom') {
        list.entries = [...new Set([...list.entries.filter(e => !(body.remove as string[] | undefined)?.includes(e)), ...(body.add as string[] ?? [])])]
        list.entry_count = list.entries.length; list.source_text = list.entries.join('\n'); list.updated_at = tick(); return response(detail(list))
      }
    }
    if (path === '/admin/dest/policies' && method === 'GET') {
      const items = seed.policies.map(p => ({ ...p, list_states: p.list_ids.map(id => { const list = seed.lists.find(l => l.id === id); return { id, name: list?.name ?? '', state: list?.state ?? 'missing' as const, available: !!list && destinationListAvailable(list) } }) }))
      return response(destinationPolicies({ published_generation: publishedGeneration, published_has_access_control: published.some(p => p.enabled) || seed.allowlistGroups.length > 0,
        allow: items.filter(p => p.action === 'allow'), block: items.filter(p => p.action === 'block'), observe: items.filter(p => p.action === 'observe'),
        exemptions: { count: seed.exemptions.filter(e => !e.expired).length }, allowlist_groups: seed.allowlistGroups, budget: budget() }))
    }
    if (path === '/admin/dest/policies/preview' && method === 'POST') {
      if (scenario === 'policy-preview-error') return fail(503, 'fixture_unavailable')
      const input = body as unknown as DestinationPolicyInput
      validatePolicy(input, body.id as number | undefined)
      const projected = budget()
      if (scenario === 'policy-over-quota') projected.domains = { used: 50001, limit: 50000 }
      if (scenario === 'template-over-quota' && input.new_list) projected.regexps.used += preview(input.new_list).regexp_count
      return response({ budget: projected, ...(input.new_list ? { new_list_preview: preview(input.new_list) } : {}) })
    }
    if (path === '/admin/dest/policies' && method === 'POST') {
      if (scenario === 'policy-over-quota') return fail(400, 'dest_policy_over_limit')
      const input = structuredClone(body) as unknown as DestinationPolicyInput
      validatePolicy(input)
      if (scenario === 'template-over-quota' && input.new_list && seed.budget.regexps.used + preview(input.new_list).regexp_count > seed.budget.regexps.limit) return fail(400, 'dest_policy_over_limit')
      const list = input.new_list ? newList(input.new_list) : null
      if (list) input.list_ids = [...input.list_ids, list.id]
      const id = Math.max(100, ...seed.policies.map(p => p.id)) + 1
      tick(); if (list) { list.updated_at = lastWrite; seed.lists.push(list) }
      const policy = policyFrom(input, id, lastWrite, seed.policies.filter(p => p.action === input.action).length + 1)
      seed.policies.push(policy); return response(policy, 201)
    }
    if (path === '/admin/dest/policies/order' && method === 'PUT') {
      const ids = body.ids as number[], action = body.action
      const existing = seed.policies.filter(p => p.action === action).map(p => p.id)
      if (!Array.isArray(ids) || ids.length !== existing.length || new Set(ids).size !== ids.length || ids.some(id => !existing.includes(id))) fail(422, 'dest_order_invalid')
      tick(); ids.forEach((id, index) => { const p = findPolicy(id); p.priority = index + 1; p.updated_at = lastWrite }); seed.policies.sort((a, b) => a.priority - b.priority); return response(null, 204)
    }
    const policyMatch = path.match(/^\/admin\/dest\/policies\/(\d+)$/)
    if (policyMatch) {
      const id = Number(policyMatch[1]), policy = findPolicy(id)
      if (method === 'GET') return response(policy)
      if (method === 'PUT') {
        if (scenario === 'policy-conflict' && !conflictInjected) { conflictInjected = true; policy.name = `${policy.name} · concurrent edit`; policy.updated_at = tick() }
        cas(policy.updated_at, 'dest_policy_stale')
        if (scenario === 'policy-over-quota') return fail(400, 'dest_policy_over_limit')
        const input = body as unknown as DestinationPolicyInput
        validatePolicy(input, id); tick(); Object.assign(policy, policyFrom(input, id, policy.created_at, policy.priority)); return response(policy)
      }
      if (method === 'DELETE') { seed.policies = seed.policies.filter(p => p.id !== id); tick(); return response(null, 204) }
    }
    if (path === '/admin/dest/exemptions' && method === 'GET') return response({ items: seed.exemptions })
    const exemptionMatch = path.match(/^\/admin\/dest\/exemptions\/(\d+)$/)
    if (exemptionMatch || path === '/admin/dest/exemptions') {
      const userId = exemptionMatch ? Number(exemptionMatch[1]) : Number(body.user_id)
      const exemption = seed.exemptions.find(e => e.user_id === userId)
      if (exemptionMatch && method === 'GET') return response(exemption ?? fail(404, 'not_found'))
      if (exemptionMatch && method === 'DELETE') {
        if (!exemption) fail(404, 'not_found')
        if (scenario === 'account-cancel-pending') { await delay(config, 30000); return fail(503, 'fixture_unavailable') }
        seed.exemptions = seed.exemptions.filter(e => e.user_id !== userId); tick(); return response(null, 204)
      }
      if (method === 'POST' && !exemptionMatch || method === 'PUT' && exemptionMatch) {
        if (!Number.isSafeInteger(userId) || userId <= 0 || exemptionMatch && body.user_id !== undefined && body.user_id !== userId) fail(400, 'dest_policy_invalid', { field: 'user_id' })
        if (typeof body.reason !== 'string' || !body.reason.trim() || Array.from(body.reason).length > 255) fail(400, 'dest_policy_invalid', { field: 'reason' })
        if (body.expires_at !== undefined && body.expires_at !== null && (!Number.isSafeInteger(body.expires_at) || Number(body.expires_at) <= 0)) fail(400, 'dest_policy_invalid', { field: 'expires_at' })
        const expiresAt = typeof body.expires_at === 'number' ? body.expires_at : null
        if (method === 'POST' && exemption) fail(409, 'dest_exemption_exists')
        if (method === 'PUT' && !exemption) fail(404, 'not_found')
        if (exemption && exemption.reason === body.reason && exemption.expires_at === expiresAt) return response(exemption)
        const writtenAt = tick()
        const value = { user_id: userId, upn: `fixture-${userId}@example.invalid`, reason: String(body.reason),
          created_by: exemption?.created_by ?? 1, created_by_upn: exemption?.created_by_upn ?? 'fixture-admin@example.invalid',
          created_at: exemption?.created_at ?? writtenAt, expires_at: expiresAt, expired: !!expiresAt && expiresAt <= Date.now() }
        seed.exemptions = [...seed.exemptions.filter(e => e.user_id !== userId), value]; return response(value, method === 'POST' ? 201 : 200)
      }
    }
    const userMatch = path.match(/^\/admin\/dest\/users\/(\d+)$/)
    if (userMatch && method === 'GET') {
      const id = Number(userMatch[1])
      if (scenario === 'account-read-error' && !failedAccountReads.has(id)) {
        failedAccountReads.add(id)
        return fail(503, 'fixture_unavailable')
      }
      return response(userAccess(id))
    }
    const riskMatch = path.match(/^\/admin\/risk-center\/users\/(\d+)$/)
    if (riskMatch && method === 'GET') {
      const id = Number(riskMatch[1]), access = userAccess(id)
      const result: RiskUserSummary = {
        user: { id, upn: `fixture-${id}@example.invalid`, display_name: `Fixture · 用户 ${id}`, role: 'user', group_id: access.group?.id ?? 0, group_name: access.group?.name ?? '', enabled: true, traffic_limit_bytes: 0,
          access: { account_state: 'active', service_state: 'active', can_login: true, can_use_portal: true, can_subscribe: true, proxy_enabled: true } },
        attention: [], review: { dismissed: false, dismissed_at_ms: 0, dismissed_by: 0, dismissed_by_upn: '', note: '', levels: {}, reopened: false, lapsed: false, escalated: [], trusted: false, trusted_at_ms: 0, trusted_by: 0, trusted_by_upn: '' },
        geo: null, signals: [], devices: [], device_window_hours: 24, devices_unavailable: false,
        live: { snapshot: { taken_at: null, source: '', age_seconds: 0, stale: true, stale_after_seconds: 60, panels_asked: 0, panels_unread: [], panels_unsupported: [], unreferenced_nodes: 0, users: 0, connections: 0, truncated: 0 }, refresh: { cooldown_seconds: 60, available_in_seconds: 0 }, device_window_hours: 24, devices_unavailable: false, panels: [], items: [], total: 0, page: 1, page_size: 1 },
      }
      return response(result)
    }
    if (path === '/admin/dest/publish' && method === 'POST') { published = structuredClone(seed.policies); publishedGeneration = generation; return response(publication()) }
    if (path === '/admin/dest/pause' && method === 'PUT') { paused = body.paused === true; tick(); return response(publication()) }
    if (path === '/admin/dest/exceptions' && method === 'POST') {
      const target = String(body.target ?? '').trim().toLowerCase()
      if (body.scope !== 'global' || !['site', 'host'].includes(String(body.match)) || !/^[a-z0-9.-]+\.[a-z]+$/.test(target)) fail(422, 'dest_exception_invalid')
      const entry = `${body.match === 'host' ? 'full' : 'domain'}:${target}`
      let list = seed.lists.find(l => l.id === 1 && l.kind === 'custom' && !l.owner_group_id)
      let policy = list && seed.policies.find(p => p.action === 'allow' && p.scope === 'all' && p.list_ids.includes(list!.id))
      let created: { list_id: number; policy_id: number } | undefined
      if (!list || !policy) {
        list = newList({ name: 'Fixture · 全局例外', kind: 'custom', text: entry })
        const id = Math.max(100, ...seed.policies.map(p => p.id)) + 1
        tick(); seed.lists.push(list)
        policy = policyFrom({ name: 'Fixture · 全局例外策略', action: 'allow', scope: 'all', group_ids: [], list_ids: [list.id], inline: {}, enabled: true, counts_as_risk: false, template_key: '' }, id, lastWrite, 0)
        seed.policies.push(policy); created = { list_id: list.id, policy_id: id }
      }
      if (!list.entries.includes(entry)) { list.entries.push(entry); list.entry_count = list.entries.length; list.source_text = list.entries.join('\n'); list.updated_at = tick() }
      return response({ list_id: list.id, policy_id: policy.id, entry, ...(created ? { created } : {}) }, 201)
    }
    const retryMatch = path.match(/^\/admin\/dest\/agents\/([^/]+)\/retry$/)
    if (retryMatch && method === 'POST') {
      const node = seed.nodes.find(n => n.agent_id === decodeURIComponent(retryMatch[1])) ?? fail(404, 'not_found')
      if (scenario === 'node-retry-pending') { await delay(config, 30000); return fail(503, 'fixture_unavailable') }
      node.state = 'pending'; node.pending_since = Date.now(); return response({ retry_requested: true })
    }
    if (path === '/admin/dest/test' && method === 'POST') {
      const steps: DestinationTestResult['steps'] = ['allow', 'exemption', 'block', 'group', 'observe', 'direct'].map(step => ({ step: step as DestinationTestResult['steps'][number]['step'], result: 'miss' }))
      const target = String(body.target ?? '').toLowerCase()
      let verdict: DestinationTestResult['verdict'] = 'direct', terminating: DestinationTestResult['terminating_step'] = 'direct'
      for (const action of ['allow', 'block', 'observe'] as const) {
        const policy = published.find(p => p.enabled && p.action === action && (p.list_ids.some(id => seed.lists.find(l => l.id === id)?.entries.some(e => e === `full:${target}` || e.startsWith('domain:') && (target === e.slice(7) || target.endsWith(`.${e.slice(7)}`)))) || p.inline.ports?.split(',').includes(String(body.port))))
        if (policy) { verdict = action; terminating = action; const step = steps.find(s => s.step === action)!; Object.assign(step, { result: 'hit', policy_id: policy.id, name: policy.name }); break }
      }
      const node = seed.nodes.find(n => n.panel_id === body.panel_id)
      if (node?.state.startsWith('unsupported')) { verdict = 'untestable'; terminating = null }
      const result: DestinationTestResult = { verdict, terminating_step: terminating, steps, notes: [], unpublished: generation !== publishedGeneration, nodes: seed.nodes.map(n => {
        const node = { panel_id: n.panel_id, name: n.panel_name, state: n.state }
        if (n.kind !== 'psp' || n.state === 'unsupported_version') return node
        const confirmed = n.minted_at != null && n.pending_since == null && n.applied_at != null
        const stopped = confirmed && (n.minted_kind === 'empty' || n.minted_kind === 'paused') && ['none', 'paused', 'rejected'].includes(n.state)
        const executing = confirmed && !n.fallback_exhausted && (n.minted_kind === 'desired' || n.minted_kind === 'fallback') && (n.state === 'applied' || n.state === 'rejected' && n.minted_kind === 'fallback')
        return { ...node, execution: { engine: n.engine, minted_kind: n.minted_kind, fallback_exhausted: n.fallback_exhausted, minted_at: n.minted_at, applied_at: n.applied_at, pending_since: n.pending_since, applied_rules: stopped ? 0 : executing ? n.applied_rules : null } }
      }) }
      return response(result)
    }
    // Unknown endpoints in the fixture scope must not mutate the local PSP.
    return fail(501, 'fixture_route_not_implemented')
  }
}
