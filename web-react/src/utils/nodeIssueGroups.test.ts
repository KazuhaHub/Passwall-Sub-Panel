import { describe, expect, it } from 'vitest'
import type { NodeAgentIssue } from '@/api/types'
import { groupNodeIssues } from './nodeIssueGroups'

function issue(id: number, overrides: Partial<NodeAgentIssue> = {}): NodeAgentIssue {
  return {
    id,
    agent_id: 'agt_a',
    code: 'core_telemetry_failed',
    key: 'xray/26.7.28',
    detail: 'collect core telemetry: xray process is starting',
    first_seen_at: '2026-09-14T00:00:00Z',
    last_seen_at: '2026-09-14T01:00:00Z',
    created_at: '2026-09-14T00:00:00Z',
    updated_at: '2026-09-14T01:00:00Z',
    ...overrides,
  }
}

describe('groupNodeIssues', () => {
  it('returns no groups for an empty API page', () => {
    expect(groupNodeIssues([])).toEqual([])
  })

  it('keeps a single issue, its exact identity and metadata', () => {
    const original = issue(10)
    expect(groupNodeIssues([original])).toEqual([{
      id: JSON.stringify(['agt_a', 'statistics']),
      agentID: 'agt_a',
      code: 'core_telemetry_failed',
      category: 'statistics',
      issues: [original],
      lastSeenAt: original.last_seen_at,
      unacknowledgedCount: 1,
    }])
    expect(groupNodeIssues([original])[0].issues[0]).toBe(original)
  })

  it('groups different keys and details without dropping any durable records', () => {
    const items = [
      issue(7, { code: 'object_pending_timeout', key: 'cli_407', detail: 'first pending record' }),
      issue(8, { code: 'object_pending_timeout', key: 'cli_406', detail: 'second pending record' }),
      issue(9, { code: 'object_pending_timeout', key: 'cli_407', detail: 'another detail for same key' }),
    ]
    const groups = groupNodeIssues(items)
    expect(groups).toHaveLength(1)
    expect(groups[0].issues).toEqual(items)
    expect(groups[0].issues.map(item => item.id)).toEqual([7, 8, 9])
    expect(groups[0].issues).not.toBe(items)
  })

  it('does not merge matching categories across agents or different categories on one agent', () => {
    const items = [
      issue(1),
      issue(2, { agent_id: 'agt_b' }),
      issue(3, { code: 'object_pending_timeout' }),
      issue(4, { agent_id: 'agt_b', code: 'object_pending_timeout' }),
    ]
    expect(groupNodeIssues(items).map(group => [group.agentID, group.code, group.issues[0].id])).toEqual([
      ['agt_a', 'core_telemetry_failed', 1],
      ['agt_b', 'core_telemetry_failed', 2],
      ['agt_a', 'object_pending_timeout', 3],
      ['agt_b', 'object_pending_timeout', 4],
    ])
  })

  it('preserves first encounter group order and backend record order, not severity or time order', () => {
    const items = [
      issue(9, { agent_id: 'agt_z', last_seen_at: '2026-09-14T00:00:00Z' }),
      issue(6, { code: 'object_pending_timeout', last_seen_at: '2026-09-14T10:00:00Z' }),
      issue(4, { agent_id: 'agt_z', last_seen_at: '2026-09-14T12:00:00Z' }),
      issue(1, { code: 'object_pending_timeout', last_seen_at: '2026-09-14T08:00:00Z' }),
    ]
    expect(groupNodeIssues(items).map(group => group.issues.map(item => item.id))).toEqual([[9, 4], [6, 1]])
  })

  it('counts mixed acknowledgement states without interpreting acknowledgement as recovery', () => {
    const items = [
      issue(1),
      issue(2, { acknowledged_at: null }),
      issue(3, { acknowledged_at: '' }),
      issue(4, { acknowledged_at: '2026-09-14T01:02:00Z' }),
      issue(5, { acknowledged_at: 'invalid-but-present' }),
    ]
    const [group] = groupNodeIssues(items)
    expect(group.unacknowledgedCount).toBe(3)
    expect(group.issues).toHaveLength(5)
    expect(group).not.toHaveProperty('recovered')
    expect(group).not.toHaveProperty('severity')
  })

  it('counts fully acknowledged and fully unacknowledged groups independently', () => {
    const [reviewed, pending] = groupNodeIssues([
      issue(1, { acknowledged_at: '2026-09-14T01:02:00Z' }),
      issue(2, { acknowledged_at: '2026-09-14T01:03:00Z' }),
      issue(3, { agent_id: 'agt_b' }),
      issue(4, { agent_id: 'agt_b', acknowledged_at: null }),
    ])
    expect(reviewed.unacknowledgedCount).toBe(0)
    expect(pending.unacknowledgedCount).toBe(2)
  })

  it('uses the greatest timestamp rather than the last record encountered', () => {
    const [group] = groupNodeIssues([
      issue(1, { last_seen_at: '2026-09-14T01:00:00Z' }),
      issue(2, { last_seen_at: '2026-09-14T03:00:00Z' }),
      issue(3, { last_seen_at: '2026-09-14T02:00:00Z' }),
    ])
    expect(group.lastSeenAt).toBe('2026-09-14T03:00:00Z')
  })

  it('compares timezone-offset timestamps as instants, retaining the original selected value', () => {
    const [group] = groupNodeIssues([
      issue(1, { last_seen_at: '2026-09-14T10:00:00+08:00' }),
      issue(2, { last_seen_at: '2026-09-13T20:30:00-07:00' }),
      issue(3, { last_seen_at: '2026-09-14T03:15:00Z' }),
    ])
    expect(group.lastSeenAt).toBe('2026-09-13T20:30:00-07:00')
  })

  it('retains the first value when equivalent timestamps use different timezone representations', () => {
    const [group] = groupNodeIssues([
      issue(1, { last_seen_at: '2026-09-14T10:00:00+08:00' }),
      issue(2, { last_seen_at: '2026-09-14T02:00:00Z' }),
    ])
    expect(group.lastSeenAt).toBe('2026-09-14T10:00:00+08:00')
  })

  it('compares subsecond precision', () => {
    const [group] = groupNodeIssues([
      issue(1, { last_seen_at: '2026-09-14T02:00:00.100Z' }),
      issue(2, { last_seen_at: '2026-09-14T02:00:00.900Z' }),
    ])
    expect(group.lastSeenAt).toBe('2026-09-14T02:00:00.900Z')
  })

  it.each(['', 'not-a-time', '9999-99-99T99:99:99Z'])('lets a valid timestamp replace an invalid initial value: %j', invalid => {
    const [group] = groupNodeIssues([
      issue(1, { last_seen_at: invalid }),
      issue(2, { last_seen_at: '2026-09-14T02:00:00Z' }),
    ])
    expect(group.lastSeenAt).toBe('2026-09-14T02:00:00Z')
    expect(group.issues).toHaveLength(2)
  })

  it('ignores invalid later timestamps while keeping their records', () => {
    const [group] = groupNodeIssues([
      issue(1, { last_seen_at: '2026-09-14T02:00:00Z' }),
      issue(2, { last_seen_at: 'zzzz' }),
      issue(3, { last_seen_at: '' }),
    ])
    expect(group.lastSeenAt).toBe('2026-09-14T02:00:00Z')
    expect(group.issues).toHaveLength(3)
  })

  it('falls back to the first unparseable value when every timestamp is invalid', () => {
    const [group] = groupNodeIssues([
      issue(1, { last_seen_at: 'first-invalid' }),
      issue(2, { last_seen_at: 'last-invalid' }),
    ])
    expect(group.lastSeenAt).toBe('first-invalid')
  })

  it('handles valid timestamps before the Unix epoch', () => {
    const [group] = groupNodeIssues([
      issue(1, { last_seen_at: 'invalid' }),
      issue(2, { last_seen_at: '1969-12-31T23:59:59Z' }),
    ])
    expect(group.lastSeenAt).toBe('1969-12-31T23:59:59Z')
  })

  it('creates collision-safe identities even for separator-like and escaped strings', () => {
    const items = [
      issue(1, { agent_id: 'a|b', code: 'c' }),
      issue(2, { agent_id: 'a', code: 'b|c' }),
      issue(3, { agent_id: 'a","b', code: 'c' }),
      issue(4, { agent_id: 'a', code: 'b","c' }),
      issue(5, { agent_id: '__proto__', code: 'constructor' }),
    ]
    const groups = groupNodeIssues(items)
    expect(groups).toHaveLength(4)
    expect(new Set(groups.map(group => group.id)).size).toBe(4)
    groups.forEach(group => expect(JSON.parse(group.id)).toEqual([group.agentID, group.category]))
    expect(groups.flatMap(group => group.issues.map(record => record.id)).sort()).toEqual([1, 2, 3, 4, 5])
  })

  it('merges related synchronization codes without rewriting their identity or diagnostic', () => {
    const items = [issue(1, { code: 'object_pending_timeout' }),
      issue(2, { code: 'core_convergence_failed', detail: 'candidate configuration failed' }),
      issue(3, { code: 'directive_unknown_client', key: 'cli_3' })]
    const [group] = groupNodeIssues(items)
    expect(group.category).toBe('sync')
    expect(group.issues).toEqual(items)
    expect(group.issues.map(record => record.code)).toEqual(items.map(record => record.code))
  })

  it('retains unknown and task safety records instead of suppressing them', () => {
    const items = [issue(1, { code: 'future_issue' }), issue(2, { code: '__proto__' }),
      issue(3, { code: 'task_replay_fenced' }), issue(4, { code: 'task_identity_conflict' }),
      issue(5, { code: 'legacy_task_result_quarantined' })]
    const groups = groupNodeIssues(items)
    expect(groups.map(group => group.category)).toEqual(['other', 'tasks'])
    expect(groups[0].issues.map(record => record.id)).toEqual([1, 2])
    expect(groups[1].issues.map(record => record.id)).toEqual([3, 4, 5])
  })

  it('does not mutate the source page or issue objects, including frozen inputs', () => {
    const items = [
      issue(1, { last_seen_at: '2026-09-14T01:00:00Z' }),
      issue(2, { last_seen_at: '2026-09-14T03:00:00Z', acknowledged_at: '2026-09-14T04:00:00Z' }),
    ]
    const before = JSON.stringify(items)
    items.forEach(Object.freeze)
    Object.freeze(items)
    const groups = groupNodeIssues(items)
    expect(JSON.stringify(items)).toBe(before)
    expect(groups[0].issues[0]).toBe(items[0])
    expect(groups[0].issues[1]).toBe(items[1])
    groups[0].issues.pop()
    expect(items).toHaveLength(2)
  })

  it('does not carry records or counts across separate API page calls', () => {
    const [firstPage] = groupNodeIssues([issue(1), issue(2)])
    const [secondPage] = groupNodeIssues([issue(3)])
    expect(firstPage.id).toBe(secondPage.id)
    expect(firstPage.issues).toHaveLength(2)
    expect(secondPage.issues).toHaveLength(1)
    expect(secondPage.unacknowledgedCount).toBe(1)
  })
})
