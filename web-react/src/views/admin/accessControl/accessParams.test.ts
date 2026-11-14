import { describe, expect, it } from 'vitest'

import { ACCESS_TABS, parseAccessTab, parseAllowlistParams, allowlistSearch } from './accessParams'

const P = (q: string) => new URLSearchParams(q)

describe('the access-control tabs', () => {
  it('has an allowlist tab between lists and records', () => {
    expect(ACCESS_TABS).toEqual(['policies', 'lists', 'allowlist', 'records'])
    expect(parseAccessTab('allowlist')).toBe('allowlist')
    expect(parseAccessTab('zzz')).toBe('policies')
  })
})

describe('the allowlist tab URL', () => {
  it('reads the group, the report view, the group to enable and the days', () => {
    expect(parseAllowlistParams(P('tab=allowlist'))).toEqual({ group: null, report: false, enable: null, days: null })
    expect(parseAllowlistParams(P('al_group=4&al_view=report&al_days=3'))).toEqual({ group: 4, report: true, enable: null, days: 3 })
    expect(parseAllowlistParams(P('al_enable=9'))).toEqual({ group: null, report: false, enable: 9, days: null })
  })

  it('a report view needs a group, and anything that is not legal reads as absent', () => {
    expect(parseAllowlistParams(P('al_view=report')).report).toBe(false)
    expect(parseAllowlistParams(P('al_group=-2&al_enable=x&al_days=0&al_view=trial'))).toEqual({ group: null, report: false, enable: null, days: null })
  })

  it('writes only what differs from the bare tab, and never a destination', () => {
    expect(allowlistSearch(P('tab=allowlist'), { group: 4, report: true, days: 3 }).toString()).toBe('tab=allowlist&al_group=4&al_view=report&al_days=3')
    expect(allowlistSearch(P('tab=allowlist&al_group=4&al_view=report'), { group: null, report: false }).toString()).toBe('tab=allowlist')
    expect(allowlistSearch(P('tab=allowlist&al_enable=9'), { enable: null }).toString()).toBe('tab=allowlist')
  })
})
