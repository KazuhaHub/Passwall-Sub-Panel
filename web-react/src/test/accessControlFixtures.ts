import type { DestinationBudget, DestinationNodeStatus, DestinationPoliciesView, DestinationPolicyOverviewItem, DestinationStatus } from '@/api/accessControl'

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
