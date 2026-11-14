import type { DestGroup, DestGroupDetail, DestGroupPreview, DestGroupReport, DestNodeStatus, DestStatus } from '@/api/accessControl'

// FIXTURES FOR THE ALLOWLIST-GROUP SCREENS' TESTS: one group as the server
// sends it, with every field, and the overrides a test cares about.

export const DAY = 86_400_000

export const group = (over: Partial<DestGroup> = {}): DestGroup => ({
  group_id: 1, group_name: 'Guests', members: 51, state: 'report', state_changed_at: Date.now() - 2 * DAY - 60_000, updated_at: 100,
  list_ids: [10], lists: [{ id: 10, name: 'Guest list', entry_count: 40, state: 'ready' }], base_list_id: 11, extra_list_id: 12, base_count: 1,
  eligible_nodes: 8, total_nodes: 12,
  ineligible: [{ panel_id: 5, name: 'US West A', reason: 'xui' }, { panel_id: 6, name: 'Osaka 05', reason: 'capability' }],
  applied_nodes: 6, pending_nodes: [{ panel_id: 7, name: 'Tokyo 07' }, { panel_id: 8, name: 'Seoul 02' }], not_ready_lists: [],
  would_deny_recent: { days: 7, sites: 128, count: 9420 }, denied_24h: null, ...over,
})

export const detail = (over: Partial<DestGroupDetail> = {}): DestGroupDetail => ({
  ...group(), readiness: { lists_ready: true, not_ready_lists: [], unapplied_nodes: 0, base_nonempty: true, remaining_sites_24h: 0, top_sites: [] }, ...over,
})

export const preview = (over: Partial<DestGroupPreview> = {}): DestGroupPreview => ({
  members: 51, eligible_nodes: 8, total_nodes: 12,
  ineligible: [{ panel_id: 5, name: 'US West A', reason: 'xui' }, { panel_id: 6, name: 'Osaka 05', reason: 'capability' }],
  nodes_lost: [{ panel_id: 5, name: 'US West A', reason: 'xui' }, { panel_id: 6, name: 'Osaka 05', reason: 'capability' }],
  non_recording_nodes: 0, not_ready_lists: [], base_hosts: ['dns.example.net'], base_hosts_source: 'template', would_deny_recent: null, ...over,
})

export const report = (over: Partial<DestGroupReport> = {}): DestGroupReport => ({
  effective_days: 7, state: 'report', since_at: Date.now() - 3 * DAY, dropped_in_range: 0,
  items: [
    { site: 'gstatic.com', count: 2210, last_at: Date.now() - 5 * 60_000, added: false },
    { site: 'figma.com', count: 1402, last_at: Date.now() - 3_600_000, added: false },
    { site: 'notion.so', count: 800, last_at: Date.now() - 7_200_000, added: true },
    { site: '(ip)', count: 302, last_at: Date.now() - 7_200_000, added: false },
  ], ...over,
})

export const node = (over: Partial<DestNodeStatus> = {}): DestNodeStatus => ({
  panel_id: 1, agent_id: 'a1', panel_name: 'Tokyo 01', kind: 'psp', engine: 'xray', agent_version: '4.2.0', supports: { policy: true, hits: true, usage: false },
  collect: 'hits', collect_effective: 'hits', collecting: true, state: 'applied', fallback_reason: '', over_limit: null, sniffing_insufficient: [],
  minted_at: null, pending_since: null, applied_at: Date.now() - 60_000, applied_rules: 5, last_report_at: null, rules_data_version: null,
  geoip_unavailable: false, allowlist_groups: [], sniffing_groups_dropped: [], hits_24h: null, ...over,
})

export const status = (nodes: DestStatus['nodes'] = [node()], over: Partial<DestStatus> = {}): DestStatus => ({
  generation: 1, published_generation: 1, last_write_at: null, next_publish_at: null, apply_eta_ms: 120_000, paused: false, publish_error: null,
  publish_error_at: null, geosite_release: '', geoip_unavailable: false, totals: {}, policy_notes: {}, nodes, ...over,
})
