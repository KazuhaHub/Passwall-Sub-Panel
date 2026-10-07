import type { DestinationBudget, DestinationExemptionView, DestinationListDetail, DestinationNodeStatus, DestinationPoliciesView, DestinationPolicyOverviewItem, DestinationStatus } from '@/api/accessControl'
import type { Group } from '@/api/types'

export const destinationBudget: DestinationBudget = {
  rules: { used: 1, limit: 256 }, domains: { used: 0, limit: 50000 }, regexps: { used: 0, limit: 256 },
  cidrs: { used: 0, limit: 20000 }, subjects: { used: 0, limit: 10000 }, bytes: { used: 0, limit: 4194304 },
}
export const samplePolicy: DestinationPolicyOverviewItem = {
  id: 12, name: 'No mail', action: 'block', list_ids: [], inline: { ports: '25,465,587', network: 'tcp' },
  scope: 'all', group_ids: [], enabled: true, counts_as_risk: true, template_key: '', priority: 0,
  created_at: 1000, updated_at: 2000, hits_recent: null, last_hit_at: null, list_states: [], scope_missing: false,
}
export function destinationPolicies(over: Partial<DestinationPoliciesView> = {}): DestinationPoliciesView {
  return { published_generation: 1, published_has_access_control: true, allow: [], block: [structuredClone(samplePolicy)], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget: structuredClone(destinationBudget), ...over }
}
export function destinationNode(over: Partial<DestinationNodeStatus> = {}): DestinationNodeStatus {
  return {
    panel_id: 1, agent_id: 'node-one', panel_name: 'Tokyo', kind: 'psp', engine: 'xray', agent_version: '4.0.1',
    supports: { policy: true, hits: false, usage: false }, collect: 'hits', collect_effective: '', collecting: false,
    state: 'applied', fallback_reason: '', fallback_exhausted: false, minted_kind: 'desired', losses: null, over_limit: null,
    sniffing_insufficient: [], minted_at: 1000, pending_since: null, applied_at: 2000, applied_rules: 1,
    allowlist_groups: [], last_report_at: 2000, hits_24h: null, ...over,
  }
}
export function destinationStatus(over: Partial<Omit<DestinationStatus, 'nodes'>> & { nodes?: readonly DestinationNodeStatus[] } = {}): DestinationStatus {
  const nodes = over.nodes ? [...over.nodes] : [destinationNode()]
  const totals = Object.fromEntries(['none', 'paused', 'unsupported_kind', 'unsupported_version', 'pending', 'applied', 'rejected', 'over_limit', 'sniffing', 'offline'].map(state => [state, nodes.filter(node => node.state === state).length])) as DestinationStatus['totals']
  return { generation: 1, published_generation: 1, paused: false, publish_error: null, last_write_at: 1000,
    next_publish_at: null, apply_eta_ms: 120000, totals: { ...totals, total: nodes.length, collecting: 0 }, ...over, nodes }
}

// Shared acceptance seed. It is imported by the DEV adapter, never the product
// entry point. Future hits views can use these samples without inventing an API
// before its transport contract is implemented.
export interface AccessControlFixtureHit {
  id: number
  user_id: number
  policy_id: number
  policy_name: string | null
  domain: string
  action: 'block' | 'observe'
  count: number
  at_ms: number
}

export function accessControlFixtureSeed(now = Date.now()) {
  const at = now - 3_600_000
  const budget: DestinationBudget = {
    rules: { used: 8, limit: 256 }, domains: { used: 846, limit: 50000 }, regexps: { used: 0, limit: 128 },
    cidrs: { used: 2, limit: 20000 }, subjects: { used: 2, limit: 10000 }, bytes: { used: 34182, limit: 4194304 },
  }
  const group = (id: number, name: string): Group => ({
    id, name, slug: `fixture-${id}`, tag_filter: { all: true, tags: [] },
    layout: { separators: [], sort: [], default_sort_strategy: '' }, members: 3,
  })
  const groups = [group(3, 'Fixture · 试运行分组'), group(9, 'Fixture · 执行中分组')]
  const allowlistGroups: DestinationPoliciesView['allowlist_groups'] = [
    { group_id: 3, name: groups[0].name, stage: 'trial', stage_days: 2 },
    { group_id: 9, name: groups[1].name, stage: 'enforce', stage_days: 14 },
  ]
  const list = (id: number, name: string, entries: string[], over: Partial<DestinationListDetail> = {}): DestinationListDetail => ({
    id, name, kind: 'custom', source_url: '', geosite_category: '', geosite_attrs: '', entry_count: entries.length,
    regexp_count: 0, state: 'ready', last_fetched_at: at, last_error: '', owner_group_id: 0, updated_at: at,
    content_sha256: `fixture-digest-${id}`, parse_report: { accepted: entries.length, rewritten: 0, ignored: 0, ignored_broad: 0, samples: [] },
    entries, source_text: entries.join('\n'), ...over,
  })
  const finance = Array.from({ length: 612 }, (_, i) => `${i < 604 ? 'domain' : 'full'}:finance-${i}.example`)
  const lists = [
    list(1, 'Fixture · 全局例外', ['domain:trusted.example', 'full:login.example']),
    list(2, 'Fixture · 上次成功仍可用', ['domain:blocked.example', 'full:mail.example', 'domain:legacy.example', 'full:static.example'], {
      kind: 'remote', source_url: 'https://lists.example.invalid/blocked.txt', state: 'failed', last_error: 'dest_list_fetch_failed',
    }),
    list(3, 'Fixture · 等待首次下载', [], { kind: 'geosite', geosite_category: 'category-porn', state: 'pending', last_fetched_at: null, parse_report: null }),
    list(4, 'Fixture · 金融分类解析报告', finance, {
      kind: 'geosite', geosite_category: 'category-finance', source_text: undefined,
      entry_types: { domain: 604, full: 8, keyword: 0, regexp: 0, cidr: 0 },
      parse_report: { accepted: 612, ignored: 1, ignored_broad: 1, rewritten: 0, samples: [{ line: 172, text: 'domain:hsbc', reason: 'broad_entry' }] },
    }),
    list(5, 'Fixture · 分组自有列表', ['domain:workspace.example', 'full:service.example'], { owner_group_id: 9 }),
  ]
  const policy = (id: number, name: string, action: DestinationPolicyOverviewItem['action'], ids: number[], over: Partial<DestinationPolicyOverviewItem> = {}): DestinationPolicyOverviewItem => ({
    ...structuredClone(samplePolicy), id, name, action, list_ids: ids, inline: {},
    counts_as_risk: action === 'block', priority: id - 100, created_at: at, updated_at: at,
    list_states: ids.map(id => { const item = lists.find(l => l.id === id)!; return { id, name: item.name, state: item.state } }), ...over,
  })
  const policies = [
    policy(101, 'Fixture · 全局可信站点', 'allow', [1]),
    policy(102, 'Fixture · 本地网络例外', 'allow', [], { inline: { private: true } }),
    policy(103, 'Fixture · 禁止邮件端口', 'block', [], { inline: { ports: '25,465,587', network: 'tcp' } }),
    policy(104, 'Fixture · 已缓存的远程名单', 'block', [2]),
    policy(105, 'Fixture · 分类尚未就绪', 'block', [3]),
    policy(106, 'Fixture · 金融分类观察', 'observe', [4], { scope: 'groups', group_ids: [3] }),
    policy(107, 'Fixture · 已停用的观察', 'observe', [], { enabled: false, inline: { ports: '443', network: 'udp' } }),
    policy(108, 'Fixture · 网段观察', 'observe', [], { inline: { cidrs: ['192.0.2.0/24', '2001:db8::/32'] } }),
  ]
  const states: DestinationNodeStatus['state'][] = ['none', 'paused', 'unsupported_kind', 'unsupported_version', 'pending', 'applied', 'rejected', 'over_limit', 'sniffing', 'offline', 'rejected', 'rejected']
  const nodes = states.map((state, i) => destinationNode({
    panel_id: i + 1, agent_id: `fixture-agent-${i + 1}`, panel_name: `Fixture · 节点 ${i + 1}`, state,
    kind: state === 'unsupported_kind' ? '3xui' : 'psp', engine: state === 'unsupported_kind' ? null : 'xray',
    agent_version: state === 'unsupported_kind' ? null : state === 'unsupported_version' ? '3.0.0' : '4.0.1',
    supports: { policy: !state.startsWith('unsupported'), hits: false, usage: false },
    collect: 'hits', collect_effective: '', collecting: false, hits_24h: null,
    minted_kind: state === 'paused' ? 'paused' : i === 11 ? 'empty' : state === 'rejected' ? 'fallback' : state.startsWith('unsupported') || state === 'none' ? '' : 'desired',
    fallback_reason: state === 'rejected' ? 'kernel_rejected' : '', fallback_exhausted: i === 11,
    pending_since: state === 'pending' || i === 6 ? now - 240_000 : null,
    applied_at: state === 'applied' || i === 10 ? at : null, applied_rules: state === 'applied' || i === 10 ? 8 : 0,
    last_report_at: state === 'offline' ? now - 86_400_000 : now - 10_000,
    losses: null, over_limit: state === 'over_limit' ? { kind: 'regexps', used: 130, limit: 128, field: 'regexps' } : null,
    sniffing_insufficient: state === 'sniffing' ? [{ listener: 'fixture-inbound', label: 'Fixture · TLS 入口', node_id: 1 }] : [],
    allowlist_groups: i === 5 ? [{ id: 9, name: groups[1].name }] : [],
  }))
  const hits: AccessControlFixtureHit[] = Array.from({ length: 300 }, (_, i) => ({
    id: i + 1, user_id: i % 2 + 1, policy_id: i === 0 ? 999 : i % 2 ? 104 : 106,
    policy_name: i === 0 ? null : policies[i % 2 ? 3 : 5].name,
    domain: i === 1 ? `${'long-label.'.repeat(20)}service.example` : `service-${i}.example`,
    action: i % 2 ? 'block' : 'observe', count: i % 9 + 1, at_ms: now - i * 60_000,
  }))
  const exemptions: DestinationExemptionView[] = [
    { user_id: 1, upn: 'fixture-1@example.invalid', reason: 'Fixture · 临时排障例外', created_by: 1, created_by_upn: 'fixture-admin@example.invalid', created_at: at, expires_at: now + 86_400_000, expired: false },
    { user_id: 2, upn: 'fixture-2@example.invalid', reason: 'Fixture · 已到期例外', created_by: 1, created_by_upn: 'fixture-admin@example.invalid', created_at: at, expires_at: now - 60_000, expired: true },
  ]
  return { policies, lists, groups, allowlistGroups, nodes, hits, budget, exemptions }
}
