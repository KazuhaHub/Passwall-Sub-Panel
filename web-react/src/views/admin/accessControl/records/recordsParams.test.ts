import { describe, expect, it } from 'vitest'

import {
  parseRecordsParams, recordsApiParams, recordsFilterCount, recordsSearch, REC_DEFAULT_SINCE, relativeSince,
  type RecordsFilter,
} from './recordsParams'

const NOW = Date.UTC(2026, 10, 13, 12, 30, 0)
const NEXT_HOUR = Date.UTC(2026, 10, 13, 13, 0, 0)
const HOUR = 3_600_000
const DAY = 24 * HOUR
const P = (qs: string) => new URLSearchParams(qs)

describe('the records tab URL grammar', () => {
  it('defaults are not written: an empty query is the default filter', () => {
    const f = parseRecordsParams(P(''))
    expect(f).toEqual({ since: REC_DEFAULT_SINCE, group_by: 'none', page: 1, page_size: 50 })
    expect(REC_DEFAULT_SINCE).toBe('24h')
    expect(recordsSearch(P('tab=records'), { since: '24h', group_by: 'none', page_size: 50 }).toString()).toBe('tab=records')
  })

  it('round-trips every param', () => {
    const f: RecordsFilter = { user_id: 7, panel_id: 3, source: 'p12', action: 'observe', since: '7d', group_by: 'site', page: 3, page_size: 100 }
    const url = recordsSearch(P('tab=records'), f)
    expect(url.get('rec_user')).toBe('7')
    expect(url.get('rec_panel')).toBe('3')
    expect(url.get('rec_source')).toBe('p12')
    expect(url.get('rec_action')).toBe('observe')
    expect(url.get('rec_since')).toBe('7d')
    expect(url.get('rec_group_by')).toBe('site')
    expect(url.get('rec_page')).toBe('3')
    expect(url.get('rec_size')).toBe('100')
    // The page is a view, not a filter: reading it back gives the same filter.
    expect(parseRecordsParams(url)).toEqual(f)
  })

  it('a filter change goes back to page 1, a page change keeps the filter', () => {
    const at3 = recordsSearch(P('tab=records&rec_page=3&rec_user=7'), { page: 3 })
    expect(at3.get('rec_page')).toBe('3')
    const changed = recordsSearch(at3, { source: 'p1' })
    expect(changed.get('rec_page')).toBeNull()
    expect(changed.get('rec_user')).toBe('7')
  })

  it('what is not a legal value falls back to the default and nothing else changes', () => {
    const f = parseRecordsParams(P('rec_action=zzz&rec_group_by=nope&rec_user=-3&rec_source=drop%20table&rec_since=yesterday&rec_size=7&rec_page=0&rec_panel=x'))
    expect(f).toEqual({ since: '24h', group_by: 'none', page: 1, page_size: 50 })
  })

  it('a deny or trial value is not offered yet: it reads as no filter', () => {
    expect(parseRecordsParams(P('rec_action=deny&rec_trial=1')).action).toBeUndefined()
  })

  it('a source is a policy or an inbound id only (p12, s4); a group (g) is a later phase', () => {
    expect(parseRecordsParams(P('rec_source=s4')).source).toBe('s4')
    expect(parseRecordsParams(P('rec_source=g2')).source).toBeUndefined()
  })

  it('there is no search param: nothing destination-like is ever written to the URL (R12)', () => {
    const url = recordsSearch(P(''), { user_id: 1, source: 'p1', action: 'block', since: '1h', group_by: 'user', page: 2, page_size: 25 } as RecordsFilter)
    expect([...url.keys()].some(k => /q$|search|dest|keyword/i.test(k))).toBe(false)
    expect(url.toString()).not.toMatch(/binance|example|\./)
    // And a stray rec_q in a pasted link is ignored, not read into the filter.
    expect(JSON.stringify(parseRecordsParams(P('rec_q=binance.com')))).not.toContain('binance')
  })

  it('an absolute range is browser-local datetime text, with its own until', () => {
    const f = parseRecordsParams(P('rec_since=2026-11-10T08%3A00&rec_until=2026-11-12T09%3A30'))
    expect(f.since).toBe('2026-11-10T08:00')
    expect(f.until).toBe('2026-11-12T09:30')
    // A bare until without a start is dropped: it bounds nothing.
    expect(parseRecordsParams(P('rec_until=2026-11-12T09%3A30')).until).toBeUndefined()
  })

  it('counts the filters in force for the phone badge, never counting the view, the page or the range', () => {
    expect(recordsFilterCount(parseRecordsParams(P('rec_user=1&rec_source=p1&rec_panel=2&rec_action=block&rec_group_by=user&rec_page=2')))).toBe(4)
  })
})

describe('what the server is asked', () => {
  const base = (over: Partial<RecordsFilter> = {}): RecordsFilter => ({ since: '24h', group_by: 'none', page: 1, page_size: 50, ...over })

  it('a relative range is counted back from now; the hour is the server\'s to round', () => {
    expect(recordsApiParams(base(), { now: NOW, retentionDays: 30 })).toMatchObject({ since: NOW - 24 * HOUR })
    expect(recordsApiParams(base({ since: '1h' }), { now: NOW, retentionDays: 30 }).since).toBe(NOW - HOUR)
    expect(recordsApiParams(base({ since: '7d' }), { now: NOW, retentionDays: 30 }).since).toBe(NOW - 7 * DAY)
  })

  it('a fixed window is clamped to the retention, and to the server\'s 31 days (R14)', () => {
    expect(recordsApiParams(base({ since: '7d' }), { now: NOW, retentionDays: 3 }).since).toBe(NOW - 3 * DAY)
    expect(recordsApiParams(base({ since: '90d' }), { now: NOW, retentionDays: 365 }).since).toBe(NEXT_HOUR - 31 * DAY)
  })

  it('a relative range ends at the next hour boundary: the current bucket is in range and the span never exceeds 31 days', () => {
    const p = recordsApiParams(base({ since: '90d' }), { now: NOW, retentionDays: 365 })
    expect(p.until).toBe(NEXT_HOUR)
    expect(p.until! - p.since!).toBe(31 * DAY)
    expect(recordsApiParams(base({ since: '7d' }), { now: NOW, retentionDays: 30 }).until).toBe(NEXT_HOUR)
  })

  it('the 7-day option is min(7, retention), spelled with the days it really is', () => {
    expect(relativeSince(7, 3)).toBe('3d')
    expect(relativeSince(7, 30)).toBe('7d')
    expect(relativeSince(1, 30)).toBe('24h')
  })

  it('observe asks for the report-only hits, block for the enforced ones, and no action for both', () => {
    expect(recordsApiParams(base({ action: 'observe' }), { now: NOW, retentionDays: 30 }).action).toBe('observe')
    expect(recordsApiParams(base({ action: 'block' }), { now: NOW, retentionDays: 30 }).action).toBe('block')
    expect(recordsApiParams(base(), { now: NOW, retentionDays: 30 }).action).toBeUndefined()
  })

  it('an absolute range is converted from browser time and held to 31 days', () => {
    const p = recordsApiParams(base({ since: '2026-09-01T00:00', until: '2026-11-12T09:30' }), { now: NOW, retentionDays: 365 })
    expect(p.until).toBe(Date.parse('2026-11-12T09:30'))
    expect(p.until! - p.since!).toBe(31 * DAY)
  })

  it('an absolute range with no end always sends until (the next hour boundary), so the server does not default it to now + 1h', () => {
    const p = recordsApiParams(base({ since: '2026-11-01T00:00' }), { now: NOW, retentionDays: 365 })
    expect(p.until).toBe(NEXT_HOUR)
    expect(p.until! - p.since!).toBeLessThanOrEqual(31 * DAY)
    const far = recordsApiParams(base({ since: '2026-01-01T00:00' }), { now: NOW, retentionDays: 365 })
    expect(far.until).toBe(NEXT_HOUR)
    expect(far.until! - far.since!).toBe(31 * DAY)
  })

  it('the keyword goes to the request only, trimmed, and an empty one is no parameter', () => {
    expect(recordsApiParams(base(), { now: NOW, retentionDays: 30, q: '  binance ' }).q).toBe('binance')
    expect('q' in recordsApiParams(base(), { now: NOW, retentionDays: 30, q: '  ' })).toBe(false)
  })

  it('the view, the page and the size are sent when they are not the default', () => {
    const d = recordsApiParams(base(), { now: NOW, retentionDays: 30 })
    expect(d.group_by).toBeUndefined()
    expect(d.page).toBeUndefined()
    const g = recordsApiParams(base({ group_by: 'user', page: 3, page_size: 25 }), { now: NOW, retentionDays: 30 })
    expect(g).toMatchObject({ group_by: 'user', page: 3, page_size: 25 })
  })

  it('account, node and source filter exactly', () => {
    expect(recordsApiParams(base({ user_id: 7, panel_id: 3, source: 'p12' }), { now: NOW, retentionDays: 30 })).toMatchObject({ user_id: 7, panel_id: 3, source: 'p12' })
  })
})
