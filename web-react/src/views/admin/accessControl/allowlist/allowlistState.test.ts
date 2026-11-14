import { describe, expect, it } from 'vitest'

import type { DestGroup } from '@/api/accessControl'
import { blockedByLists, groupHasAllowlist, reportDayOptions, stateDay } from './allowlistState'

const DAY = 86_400_000
const g = (over: Partial<DestGroup>): DestGroup => ({
  group_id: 1, group_name: 'Guests', members: 3, state: 'report', state_changed_at: null, updated_at: 1, list_ids: [], lists: [], base_list_id: 0,
  extra_list_id: 0, base_count: 0, eligible_nodes: 0, total_nodes: 0, ineligible: [], applied_nodes: 0, pending_nodes: [], not_ready_lists: [],
  would_deny_recent: null, denied_24h: null, ...over,
})

describe('the day a state is on', () => {
  it('counts from 1 on the day it began', () => {
    const now = 10 * DAY
    expect(stateDay(now - 1000, now)).toBe(1)
    expect(stateDay(now - 2 * DAY - 1000, now)).toBe(3)
  })
  it('is null when the server did not say when', () => {
    expect(stateDay(null, 5)).toBeNull()
  })
})

describe('the report ranges', () => {
  it('offers 1, 3 and 7 days, minus what the retention does not keep', () => {
    expect(reportDayOptions(7)).toEqual([1, 3, 7])
    expect(reportDayOptions(30)).toEqual([1, 3, 7])
    expect(reportDayOptions(3)).toEqual([1, 3])
    expect(reportDayOptions(1)).toEqual([1])
  })
  it('offers all three while the retention is unknown', () => {
    expect(reportDayOptions(undefined)).toEqual([1, 3, 7])
  })
})

describe('which groups have an allowlist', () => {
  it('a group that is off and has no row has none; off with a row keeps its lists', () => {
    expect(groupHasAllowlist(g({ state: 'off', updated_at: null }))).toBe(false)
    expect(groupHasAllowlist(g({ state: 'off', updated_at: 5 }))).toBe(true)
    expect(groupHasAllowlist(g({ state: 'on' }))).toBe(true)
  })
})

describe('what stops a turn-on', () => {
  it('only a list that is not ready does', () => {
    expect(blockedByLists([])).toBe(false)
    expect(blockedByLists([{ id: 2, name: 'x' }])).toBe(true)
  })
})
