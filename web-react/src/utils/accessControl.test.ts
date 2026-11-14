import { describe, expect, it } from 'vitest'

import type { DestListsView, DestNodeStatus, DestPolicies, DestPolicy, DestStatus } from '@/api/accessControl'
import { createAppTheme, DEFAULT_PRESET_HEX } from '@/theme'
import {
  ACCESS_TONE_KINDS, accessTone, enforcingNodeCount, limitKindKey, accessVerdict, applyMinutes, budgetLevel, clampDays, destStatusActive,
  needsFirstPublishConfirm, nodeStateKey, NODE_STATE_FILTER, NODE_STATE_TONE, PENDING_STUCK_MS, pipelineSteps,
  visibleBudget, type NodeStateKey,
  collectingNodeCount, enabledPolicies, reportNotDeployedCount, nodeRecording, policyHitsView, policyStateOptions, policyStateTone, reorderWithin,
  splitBlockByState,
} from './accessControl'

const NOW = 1_700_000_000_000

function node(over: Partial<DestNodeStatus> = {}): DestNodeStatus {
  return {
    panel_id: 1, agent_id: 'a1', panel_name: 'Tokyo 01', kind: 'psp', engine: 'xray', agent_version: '4.1.2',
    supports: { policy: true, hits: false, usage: false }, collect: '', collect_effective: '', collecting: false,
    state: 'applied', fallback_reason: '', over_limit: null, sniffing_insufficient: [], minted_at: null,
    pending_since: null, applied_at: NOW - 60_000, applied_rules: 3, last_report_at: NOW - 5_000,
    rules_data_version: null, geoip_unavailable: false, allowlist_groups: [], hits_24h: null, ...over,
  }
}

function status(nodes: DestNodeStatus[], over: Partial<DestStatus> = {}): DestStatus {
  return {
    generation: 3, published_generation: 3, last_write_at: null, next_publish_at: null, apply_eta_ms: 0, paused: false,
    publish_error: null, publish_error_at: null, geosite_release: '', geoip_unavailable: false, totals: {}, nodes,
    policy_notes: {}, ...over,
  }
}

function policy(over: Partial<DestPolicy> = {}): DestPolicy {
  return {
    id: 1, name: 'No mail', description: '', action: 'block', state: 'on', list_ids: [], inline: {}, scope: 'all',
    group_ids: [], panel_ids: [], priority: 1, template_key: '', created_at: 0, updated_at: 0, list_states: [], note: null,
    scope_missing: false, hits_recent: null, would_block_recent: null, last_hit_at: null, ...over,
  }
}

const budget = (): DestPolicies['budget'] => ({
  rules: { used: 1, limit: 256 }, domains: { used: 0, limit: 50000 }, regexps: { used: 0, limit: 256 },
  cidrs: { used: 0, limit: 20000 }, source_cidrs: { used: 0, limit: 60000 }, subjects: { used: 0, limit: 5000 }, bytes: { used: 10, limit: 1000 },
})

function pols(...p: DestPolicy[]): DestPolicies {
  return { allow: p.filter(x => x.action === 'allow'), block: p.filter(x => x.action === 'block'), observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget: budget() }
}

describe('accessVerdict: the worst finding wins', () => {
  const on = pols(policy())
  const cases: { name: string; st: DestStatus; policies?: DestPolicies; lists?: DestListsView; kind: string; tone: string }[] = [
    { name: 'paused beats everything', st: status([node({ state: 'rejected' })], { paused: true }), policies: on, kind: 'paused', tone: 'failing' },
    { name: 'a refused publish', st: status([node()], { publish_error: { kind: 'domains', used: 57312, limit: 50000 } }), policies: on, kind: 'publish_refused', tone: 'failing' },
    { name: 'a node refused the policy', st: status([node({ state: 'rejected' })]), policies: on, kind: 'rejected', tone: 'failing' },
    { name: 'a node with no sniffing', st: status([node({ state: 'sniffing', sniffing_insufficient: [{ listener: 'l1', label: 'vless-443', node_id: 4 }] })]), policies: on, kind: 'sniffing', tone: 'failing' },
    { name: 'a node in effect with inbounds that cannot sniff is attention, not a failure', st: status([node({ sniffing_insufficient: [{ listener: 'l1', label: 'vless-443', node_id: 4 }] })]), policies: on, kind: 'sniffing_partial', tone: 'attention' },
    { name: 'a node over its limit', st: status([node({ state: 'over_limit', over_limit: { kind: 'subjects', used: 9, limit: 5 } })]), policies: on, kind: 'over_limit', tone: 'failing' },
    { name: 'enabled policies and no node can run them', st: status([node({ state: 'unsupported_kind', kind: 'x-ui' })]), policies: on, kind: 'no_coverage', tone: 'failing' },
    { name: 'a native node too old', st: status([node(), node({ panel_id: 2, state: 'unsupported_version' })]), policies: on, kind: 'upgrade', tone: 'attention' },
    { name: 'a native node offline', st: status([node(), node({ panel_id: 2, state: 'offline' })]), policies: on, kind: 'offline', tone: 'attention' },
    { name: 'a saved change waiting to go out', st: status([node()], { generation: 4 }), policies: on, kind: 'waiting', tone: 'measuring' },
    { name: 'nodes applying', st: status([node(), node({ panel_id: 2, state: 'pending', pending_since: NOW - 1000 })]), policies: on, kind: 'rolling', tone: 'measuring' },
    { name: 'a node applying for too long', st: status([node({ state: 'pending', pending_since: NOW - PENDING_STUCK_MS - 1 })]), policies: on, kind: 'rolling_slow', tone: 'attention' },
    { name: 'everything applied', st: status([node()]), policies: on, kind: 'applied', tone: 'ok' },
    { name: 'nothing enabled is quiet, never green', st: status([node({ state: 'none' })]), policies: pols(policy({ state: 'off' })), kind: 'idle', tone: 'quiet' },
  ]
  it.each(cases)('$name', ({ st, policies, lists, kind, tone }) => {
    const v = accessVerdict(st, { policies, lists, now: NOW })
    expect(v.kind).toBe(kind)
    expect(v.tone).toBe(tone)
  })

  it('3X-UI and S-UI not enforcing is by design: it is listed in the detail and never changes the colour', () => {
    const v = accessVerdict(status([node(), node({ panel_id: 2, kind: 'x-ui', state: 'unsupported_kind' })]), { policies: on, now: NOW })
    expect(v.tone).toBe('ok')
    expect(v.skipped.unsupportedKind).toBe(1)
  })

  it('a list that failed to refresh and is used by an enabled policy is attention; one that never downloaded likewise', () => {
    const lists = (state: 'failed' | 'pending', fetched: number | null): DestListsView => ({
      refresh_hours: 24, budget: budget(),
      items: [{
        id: 9, name: 'Scam list', kind: 'remote', side: 'dest', entry_count: 0, regexp_count: 0, state, empty: false,
        last_fetched_at: fetched, last_error: 'HTTP 404', owner_group_id: 0, updated_at: 0,
        used_by: [{ kind: 'policy', id: 1, name: 'No mail' }],
      }],
    })
    expect(accessVerdict(status([node()]), { policies: on, lists: lists('failed', NOW - 1), now: NOW })).toMatchObject({ kind: 'list_failed', tone: 'attention', name: 'Scam list' })
    expect(accessVerdict(status([node()]), { policies: on, lists: lists('pending', null), now: NOW })).toMatchObject({ kind: 'list_pending', tone: 'attention' })
  })

  it('a native panel with no agent is not enrolled, not an upgrade', () => {
    const v = accessVerdict(status([node(), node({ panel_id: 2, state: 'unsupported_version', agent_id: '', agent_version: '' })]), { policies: on, now: NOW })
    expect(v.kind).toBe('applied')
    expect(v.skipped.upgrade).toBe(0)
  })

  it('not-enrolled native panels are counted in the summary, quietly, and never change the tone', () => {
    const v = accessVerdict(status([node(), node({ panel_id: 2, state: 'unsupported_version', agent_id: '' }), node({ panel_id: 3, state: 'unsupported_version', agent_id: '' })]), { policies: on, now: NOW })
    expect(v.skipped.notEnrolled).toBe(2)
    expect(v).toMatchObject({ kind: 'applied', tone: 'ok' })
  })

  it('the server\'s own not_enrolled state: quiet, counted, and never an executor that has not applied', () => {
    const v = accessVerdict(status([node(), node({ panel_id: 2, state: 'not_enrolled', agent_id: '', agent_version: '' })]), { policies: on, now: NOW })
    expect(v.skipped.notEnrolled).toBe(1)
    expect(v.skipped.upgrade).toBe(0)
    expect(v).toMatchObject({ kind: 'applied', tone: 'ok', executors: 1, total: 1 })
  })

  it('source rules alone count as something to enforce', () => {
    const v = accessVerdict(status([node()]), { policies: pols(), hasDefinitions: true, now: NOW })
    expect(v.kind).toBe('applied')
  })
})

describe('node states: one table', () => {
  const states: { node: Partial<DestNodeStatus>; key: NodeStateKey }[] = [
    { node: { state: 'applied' }, key: 'applied' },
    { node: { state: 'applied', engine: 'sing-box' }, key: 'applied_exec_only' },
    { node: { state: 'applied', sniffing_insufficient: [{ listener: 'l1', label: 'v', node_id: 4 }] }, key: 'applied_sniffing' },
    { node: { state: 'pending', sniffing_insufficient: [{ listener: 'l1', label: 'v', node_id: 4 }], pending_since: NOW - 5000 }, key: 'pending' },
    { node: { state: 'pending', pending_since: NOW - 5000 }, key: 'pending' },
    { node: { state: 'pending', pending_since: NOW - PENDING_STUCK_MS - 5 }, key: 'pending_long' },
    { node: { state: 'rejected' }, key: 'rejected' },
    { node: { state: 'sniffing' }, key: 'sniffing' },
    { node: { state: 'over_limit' }, key: 'over_limit' },
    { node: { state: 'unsupported_version' }, key: 'unsupported_version' },
    { node: { state: 'unsupported_version', agent_id: '' }, key: 'not_enrolled' },
    { node: { state: 'not_enrolled' }, key: 'not_enrolled' },
    { node: { state: 'unsupported_version', agent_id: 'a1' }, key: 'unsupported_version' },
    { node: { state: 'unsupported_kind' }, key: 'unsupported_kind' },
    { node: { state: 'offline' }, key: 'offline' },
    { node: { state: 'none' }, key: 'none' },
    { node: { state: 'paused' }, key: 'paused' },
  ]
  it.each(states)('$key', ({ node: n, key }) => {
    expect(nodeStateKey(node(n), NOW)).toBe(key)
    expect(NODE_STATE_TONE[key]).toBeTruthy()
    expect(NODE_STATE_FILTER[key]).toBeTruthy()
  })
})

describe('the closed colour table', () => {
  for (const mode of ['light', 'dark'] as const) {
    it(`${mode}: no two rows share a fill, words and icon`, () => {
      const theme = createAppTheme({ mode, sourceColor: DEFAULT_PRESET_HEX, language: 'en-US' })
      const seen = new Map<string, string>()
      for (const kind of ACCESS_TONE_KINDS) {
        const t = accessTone(theme, kind)
        const sig = [t.bg, t.text ?? t.fg, t.fg, t.Icon.displayName ?? t.Icon.name ?? String(t.Icon)].join('|')
        // Icons are distinct component objects; identify by reference through a per-run list.
        const key = [t.bg, t.text ?? t.fg, t.fg].join('|') + '|' + iconId(t.Icon)
        expect(seen.get(key), `${kind} repeats ${seen.get(key)} (${sig})`).toBeUndefined()
        seen.set(key, kind)
      }
      expect(seen.size).toBe(ACCESS_TONE_KINDS.length)
    })
  }
})

const icons = new Map<unknown, number>()
function iconId(icon: unknown): number {
  if (!icons.has(icon)) icons.set(icon, icons.size)
  return icons.get(icon)!
}

describe('shared node counts and limit kinds', () => {
  it('counts nodes an apply reaches, once', () => {
    const st = status([node(), node({ panel_id: 2, state: 'offline' }), node({ panel_id: 3, state: 'unsupported_version' }), node({ panel_id: 4, kind: 'x-ui' }), node({ panel_id: 5, supports: { policy: false, hits: false, usage: false } })])
    expect(enforcingNodeCount(st)).toBe(1)
    expect(enforcingNodeCount(undefined)).toBeNull()
  })
  it('maps every limit kind the server names', () => {
    expect(limitKindKey('entries')).toBe('entries'); expect(limitKindKey('remote_bytes')).toBe('bytes')
    expect(limitKindKey('source_cidrs')).toBe('cidrs'); expect(limitKindKey('zzz')).toBe('invalid')
  })
})

describe('small decisions', () => {
  it('the rail numbers itself', () => {
    expect(pipelineSteps().map(s => s.id)).toEqual(['allow', 'exempt', 'block', 'rest'])
    expect(pipelineSteps({ report: true, allowlist: true }).map(s => `${s.n}:${s.id}`))
      .toEqual(['1:allow', '2:exempt', '3:block', '4:allowlist', '5:report', '6:rest'])
    // Report-only is phase 5 of the compile: after every enforced step, and before the rest.
    expect(pipelineSteps({ report: true }).map(s => `${s.n}:${s.id}`)).toEqual(['1:allow', '2:exempt', '3:block', '4:report', '5:rest'])
  })

  it('budget levels: 80% warns, over 100% blocks', () => {
    expect(budgetLevel({ used: 79, limit: 100 })).toBe('ok')
    expect(budgetLevel({ used: 80, limit: 100 })).toBe('near')
    expect(budgetLevel({ used: 100, limit: 100 })).toBe('near')
    expect(budgetLevel({ used: 101, limit: 100 })).toBe('over')
    const b = budget()
    expect(visibleBudget(b)).toEqual(['rules', 'domains', 'regexps', 'cidrs'])
    b.bytes = { used: 900, limit: 1000 }
    expect(visibleBudget(b)).toEqual(['rules', 'domains', 'regexps', 'cidrs', 'bytes'])
  })

  it('polls while a node applies or a change waits, and not otherwise', () => {
    expect(destStatusActive(undefined)).toBe(false)
    expect(destStatusActive(status([node()]))).toBe(false)
    expect(destStatusActive(status([node({ state: 'pending' })]))).toBe(true)
    expect(destStatusActive(status([node()], { generation: 4 }))).toBe(true)
    expect(destStatusActive(status([node()], { generation: 4, publish_error: { kind: 'rules' } }))).toBe(false)
    expect(destStatusActive(status([node({ state: 'pending' })], { paused: true }))).toBe(false)
  })

  it('the first publish asks, from every entry point, only when nothing was enforced and something now is', () => {
    expect(needsFirstPublishConfirm({ policiesOn: 0, sourceRulesOn: 0 }, { turnsOn: true })).toBe(true)
    expect(needsFirstPublishConfirm({ policiesOn: 1, sourceRulesOn: 0 }, { turnsOn: true })).toBe(false)
    expect(needsFirstPublishConfirm({ policiesOn: 0, sourceRulesOn: 1 }, { turnsOn: true })).toBe(false)
    expect(needsFirstPublishConfirm({ policiesOn: 0, sourceRulesOn: 0 }, { turnsOn: false })).toBe(false)
  })

  it('minutes come from the server, rounded up; unknown reads null', () => {
    expect(applyMinutes({ apply_eta_ms: 61_000 })).toBe(2)
    expect(applyMinutes({ apply_eta_ms: 1 })).toBe(1)
    expect(applyMinutes({ apply_eta_ms: 0 })).toBeNull()
    expect(applyMinutes(undefined)).toBeNull()
  })

  it('a fixed window is clamped to the retention', () => {
    expect(clampDays(7, 3)).toBe(3)
    expect(clampDays(7, 30)).toBe(7)
    expect(clampDays(7, undefined)).toBe(7)
  })
})

describe('the three states', () => {
  it('an allow policy offers off and on only; a block policy and a source rule all three', () => {
    expect(policyStateOptions('allow')).toEqual(['off', 'on'])
    expect(policyStateOptions('block')).toEqual(['off', 'report', 'on'])
  })

  it('every state has a tone from the closed table: report is the "would be denied" row (ScienceOutlined), never the allow or block row', () => {
    expect(policyStateTone('off')).toBe('paused')
    expect(policyStateTone('report')).toBe('would_deny')
    expect(policyStateTone('on')).toBeNull()
  })

  it('a report-only policy counts as deployed (nodes run it) for what "enabled" means', () => {
    const on = pols(policy({ id: 1, state: 'on' }), policy({ id: 2, state: 'report' }), policy({ id: 3, state: 'off' }))
    expect(enabledPolicies(on).map(p => p.id)).toEqual([1, 2])
  })

  it('a deployed report-only policy needs a node that can run it: no coverage is failing, as for an enforced one', () => {
    const v = accessVerdict(status([node({ state: 'unsupported_kind', kind: 'x-ui' })]), { policies: pols(policy({ state: 'report' })), now: NOW })
    expect(v.kind).toBe('no_coverage')
  })

  it('splits the block section into the enforced-or-off rows and the report-only rows, each in the server order', () => {
    const a = policy({ id: 1, state: 'on' }); const b = policy({ id: 2, state: 'report' }); const c = policy({ id: 3, state: 'off' }); const d = policy({ id: 4, state: 'report' })
    const { enforced, report } = splitBlockByState([a, b, c, d])
    expect(enforced.map(p => p.id)).toEqual([1, 3])
    expect(report.map(p => p.id)).toEqual([2, 4])
  })

  it('moving within a section rewrites only that section\'s slots of the whole order', () => {
    // block order [1 on, 2 report, 3 off, 4 report]: report section is [2, 4].
    expect(reorderWithin([1, 2, 3, 4], [2, 4], 4, 'up')).toEqual([1, 4, 3, 2])
    expect(reorderWithin([1, 2, 3, 4], [2, 4], 2, 'bottom')).toEqual([1, 4, 3, 2])
    expect(reorderWithin([1, 2, 3, 4], [1, 3], 3, 'top')).toEqual([3, 2, 1, 4])
    expect(reorderWithin([1, 2, 3, 4], [2, 4], 2, 'up')).toBeNull()
    expect(reorderWithin([1, 2, 3, 4], [2, 4], 4, 'down')).toBeNull()
  })
})

describe('report-only rules some nodes do not run', () => {
  it('counts the nodes the server says were sent the policy without its report-only rules', () => {
    const n = (v: 'sniffing' | 'limit' | null | undefined) => ({ report_only_not_deployed: v }) as never
    expect(reportNotDeployedCount(undefined)).toBe(0)
    expect(reportNotDeployedCount({ nodes: [n(null), n('sniffing'), n('limit'), n(undefined)] })).toBe(2)
  })
  it('for a policy, counts only the nodes it is sent to: all, its panels, or every node for a group scope', () => {
    const n = (panel_id: number, v: 'sniffing' | 'limit' | null): Pick<DestNodeStatus, 'panel_id' | 'report_only_not_deployed'> => ({ panel_id, report_only_not_deployed: v })
    const st = { nodes: [n(1, 'sniffing'), n(2, 'limit'), n(3, null)] }
    expect(reportNotDeployedCount(st, { scope: 'all', panel_ids: [], group_ids: [] })).toBe(2)
    expect(reportNotDeployedCount(st, { scope: 'panels', panel_ids: [2, 3], group_ids: [] })).toBe(1)
    expect(reportNotDeployedCount(st, { scope: 'panels', panel_ids: [3], group_ids: [] })).toBe(0)
    // Which nodes a group's members reach is not in the status: the fleet count is the upper bound.
    expect(reportNotDeployedCount(st, { scope: 'groups', panel_ids: [], group_ids: [4] })).toBe(2)
  })
  it('a report-only "nothing would be blocked" or count carries how many nodes are not running it', () => {
    expect(policyHitsView(policy({ state: 'report', would_block_recent: 0, last_hit_at: null }), 7, 2)).toEqual({ kind: 'would_block_none', days: 7, notDeployed: 2 })
    expect(policyHitsView(policy({ state: 'report', would_block_recent: 5, last_hit_at: 1 }), 7, 1)).toEqual({ kind: 'would_block', count: 5, days: 7, last: 1, notDeployed: 1 })
    expect(policyHitsView(policy({ state: 'report', would_block_recent: 0, last_hit_at: null }), 7, 0)).toEqual({ kind: 'would_block_none', days: 7 })
  })
})

describe('what a policy row says of its hits', () => {
  const win = 7
  it('an allow policy records nothing: no hit line at all', () => {
    expect(policyHitsView(policy({ action: 'allow' }), win)).toEqual({ kind: 'none' })
  })
  it('null is "no node records", never 0', () => {
    expect(policyHitsView(policy({ hits_recent: null }), win)).toEqual({ kind: 'unavailable' })
    expect(policyHitsView(policy({ state: 'report', would_block_recent: null }), win)).toEqual({ kind: 'unavailable' })
  })
  it('a block policy: the count and the last hit, "no hits in the window", or "none within the retention"', () => {
    expect(policyHitsView(policy({ hits_recent: 214, last_hit_at: 5 }), win)).toEqual({ kind: 'count', count: 214, days: 7, last: 5 })
    expect(policyHitsView(policy({ hits_recent: 0, last_hit_at: 9 }), win)).toEqual({ kind: 'zero', days: 7, last: 9 })
    expect(policyHitsView(policy({ hits_recent: 0, last_hit_at: null }), win)).toEqual({ kind: 'never' })
  })
  it('a report-only policy speaks of what it would have blocked, from would_block_recent and not the block count', () => {
    expect(policyHitsView(policy({ state: 'report', hits_recent: 0, would_block_recent: 1032, last_hit_at: 5 }), win))
      .toEqual({ kind: 'would_block', count: 1032, days: 7, last: 5 })
    expect(policyHitsView(policy({ state: 'report', hits_recent: 0, would_block_recent: 0, last_hit_at: null }), win))
      .toEqual({ kind: 'would_block_none', days: 7 })
  })
})

describe('recording', () => {
  it('counts the nodes that record now, by the derived flag and never by the setting', () => {
    expect(collectingNodeCount(undefined)).toBe(0)
    expect(collectingNodeCount(status([node({ collecting: true }), node({ panel_id: 2, collect: 'hits', collecting: false }), node({ panel_id: 3, collecting: true })]))).toBe(2)
  })

  it('a node line: off, hits, cannot (too old), sing-box executes only, none for a non-native node', () => {
    expect(nodeRecording(node({ supports: { policy: true, hits: true, usage: false }, collect: 'hits', collect_effective: 'hits', collecting: true, hits_24h: 1204 })))
      .toEqual({ mode: 'hits', hits24h: 1204 })
    expect(nodeRecording(node({ supports: { policy: true, hits: true, usage: false }, collect: 'off', collect_effective: 'off' })))
      .toEqual({ mode: 'off', hits24h: null })
    expect(nodeRecording(node({ supports: { policy: true, hits: false, usage: false }, collect: 'hits' }))).toEqual({ mode: 'too_old', hits24h: null })
    expect(nodeRecording(node({ engine: 'sing-box', supports: { policy: true, hits: false, usage: false } }))).toEqual({ mode: 'exec_only', hits24h: null })
    expect(nodeRecording(node({ kind: 'x-ui', state: 'unsupported_kind' }))).toBeNull()
  })

  it('reads what the node was told (collect_effective), not what the panel has stored', () => {
    const hitsNode = { supports: { policy: true, hits: true, usage: false }, hits_24h: null }
    expect(nodeRecording(node({ ...hitsNode, collect: 'hits', collect_effective: 'off' }))).toEqual({ mode: 'off', hits24h: null })
    expect(nodeRecording(node({ ...hitsNode, collect: 'off', collect_effective: 'hits' }))).toEqual({ mode: 'hits', hits24h: null })
    // Nothing delivered yet: the stored choice stands in.
    expect(nodeRecording(node({ ...hitsNode, collect: 'off', collect_effective: '' }))).toEqual({ mode: 'off', hits24h: null })
  })

  it('a node that was told to record but is not recording yet is "hits" with a null 24h, not 0', () => {
    expect(nodeRecording(node({ supports: { policy: true, hits: true, usage: false }, collect: 'hits', collect_effective: 'hits', collecting: false, hits_24h: null })))
      .toEqual({ mode: 'hits', hits24h: null })
  })
})
