import { describe, expect, it, vi } from 'vitest'

// The wire module the filters are checked against imports the shared axios
// client, which reads the document at import time. Nothing here makes a
// request.
vi.mock('@/api/client', () => ({ client: {} }))

import {
  legacyRedirect, liveSearch, parseLiveParams, parseQueueParams, parseRecordsParams, parseRiskTab, parseUserId,
  queueSearch, recordsSearch, RISK_TABS,
} from './riskParams'

const q = (search: string) => new URLSearchParams(search)

describe('parseRiskTab', () => {
  it('lists the tabs in order', () => {
    expect(RISK_TABS).toEqual(['queue', 'live', 'records'])
  })

  // The default is the queue: the page opens on what needs a look.
  it.each([null, '', 'bogus', 'geo', 'user'])('reads %j as the queue', raw => {
    expect(parseRiskTab(raw)).toBe('queue')
  })

  it.each(['queue', 'live', 'records'])('reads %s', raw => {
    expect(parseRiskTab(raw)).toBe(raw)
  })
})

describe('parseUserId', () => {
  it.each(['abc', '0', '-7', '7.5', '07x', '', '9007199254740993'])('rejects %j', raw => {
    expect(parseUserId(raw)).toBeNull()
  })

  it('accepts a positive safe integer', () => {
    expect(parseUserId('7')).toBe(7)
    expect(parseUserId(null)).toBeNull()
  })
})

// Every link the old five tabs wrote lands on the tab that now answers it,
// in ONE replace. The table of spec §3.4, row by row.
describe('legacyRedirect', () => {
  it.each([
    ['?tab=geo', '?tab=queue&source=geo'],
    ['?tab=geo&source=devices', '?tab=queue&source=devices'],
    ['?tab=risk', '?tab=queue'],
    ['?tab=flags', '?tab=records'],
    ['?tab=connections', '?tab=live'],
    ['?tab=user&id=7', '?tab=queue&user=7'],
    ['?tab=user', '?tab=queue'],
    ['?tab=user&id=abc', '?tab=queue'],
    ['?tab=user&id=0', '?tab=queue'],
  ])('%s becomes %s', (from, to) => {
    expect(legacyRedirect(q(from))).toBe(to)
  })

  // Other params travel with the link; the lookup's id never does — it
  // either became the drawer's user or named nobody.
  it('carries the other params and always drops id', () => {
    expect(legacyRedirect(q('?tab=flags&user=9&id=3'))).toBe('?tab=records&user=9')
    expect(legacyRedirect(q('?tab=connections&id=3&live_panel=2'))).toBe('?tab=live&live_panel=2')
  })

  it.each(['', '?tab=queue', '?tab=live', '?tab=records', '?tab=bogus', '?user=7'])(
    '%j is not a redirect', search => {
      expect(legacyRedirect(q(search))).toBeNull()
    })
})

describe('queue params', () => {
  it('reads the defaults from an empty URL', () => {
    expect(parseQueueParams(q(''))).toEqual({ status: 'open', page: 1, page_size: 25 })
  })

  it('reads every filter', () => {
    expect(parseQueueParams(q('status=all&source=geo,devices&level=flagged&auto=1&urgent=1&q=ali&page=3&size=50')))
      .toEqual({
        status: 'all', source: 'geo,devices', level: 'flagged', auto_suspended: true, urgent: true, q: 'ali',
        page: 3, page_size: 50,
      })
  })

  // geo_auto is shown by its card and the level chip, never as a source
  // filter (D18); a source this build does not know filters nothing.
  it('drops unknown sources and geo_auto, keeping the canonical order', () => {
    expect(parseQueueParams(q('source=devices,geo_auto,travel,geo,devices')).source).toBe('geo,devices')
    expect(parseQueueParams(q('source=geo_auto')).source).toBeUndefined()
  })

  it('ignores malformed values', () => {
    expect(parseQueueParams(q('status=bogus&level=suspended&auto=yes&urgent=0&page=-1&size=7&q=')))
      .toEqual({ status: 'open', page: 1, page_size: 25 })
  })

  it.each([
    { status: 'open' as const, page: 1, page_size: 25 },
    { status: 'dismissed' as const, page: 2, page_size: 100 },
    { status: 'trusted' as const, source: 'sub_spread,login_country', page: 1, page_size: 25 },
    { status: 'all' as const, auto_suspended: true, page: 1, page_size: 25 },
    { status: 'open' as const, level: 'suspect' as const, urgent: true, q: 'bob', page: 4, page_size: 50 },
  ])('round-trips %j', params => {
    expect(parseQueueParams(queueSearch(q(''), params))).toEqual(params)
  })

  // The defaults are never written: a clean URL is the default view.
  it('omits the defaults', () => {
    expect(queueSearch(q('status=dismissed&page=3&size=50'), { status: 'open', page: 1, page_size: 25 }).toString())
      .toBe('')
  })

  it('writes auto and urgent as 1 and removes them when off', () => {
    expect(queueSearch(q(''), { auto_suspended: true, urgent: true }).toString()).toBe('auto=1&urgent=1')
    expect(queueSearch(q('auto=1&urgent=1'), { auto_suspended: false, urgent: false }).toString()).toBe('')
  })

  it('writes the source list without geo_auto', () => {
    expect(queueSearch(q(''), { source: 'geo_auto,devices,geo' }).get('source')).toBe('geo,devices')
    expect(queueSearch(q('source=geo'), { source: '' }).has('source')).toBe(false)
  })

  // Page 3 of one filter is not a page of another.
  it('resets the page on every filter change, and only then', () => {
    const at = q('tab=queue&level=flagged&page=3')
    expect(queueSearch(at, { status: 'dismissed' }).has('page')).toBe(false)
    expect(queueSearch(at, { q: 'ali' }).has('page')).toBe(false)
    expect(queueSearch(at, { source: 'geo' }).has('page')).toBe(false)
    expect(queueSearch(at, { page_size: 50 }).has('page')).toBe(false)
    expect(queueSearch(at, { page: 4 }).get('page')).toBe('4')
  })

  // The tab, the drawer's user and the other tabs' params are not the
  // queue's to touch.
  it('keeps every other param', () => {
    const next = queueSearch(q('tab=queue&user=7&live_panel=2'), { level: 'suspect' })
    expect(next.get('tab')).toBe('queue')
    expect(next.get('user')).toBe('7')
    expect(next.get('live_panel')).toBe('2')
    expect(next.get('level')).toBe('suspect')
  })
})

describe('live params', () => {
  it('reads the defaults from an empty URL', () => {
    expect(parseLiveParams(q(''))).toEqual({ page: 1, page_size: 25 })
  })

  it('reads every filter', () => {
    expect(parseLiveParams(q('live_user=7&live_panel=2&live_excl=shared&live_page=3&live_size=50')))
      .toEqual({ user_id: 7, panel_id: 2, exclusion: 'shared', page: 3, page_size: 50 })
    expect(parseLiveParams(q('live_excl=kept')).exclusion).toBe('kept')
    expect(parseLiveParams(q('live_excl=excluded')).exclusion).toBe('excluded')
  })

  it('ignores malformed values', () => {
    expect(parseLiveParams(q('live_user=abc&live_panel=0&live_excl=bogus&live_page=x&live_size=7')))
      .toEqual({ page: 1, page_size: 25 })
  })

  it.each([
    { page: 1, page_size: 25 },
    { user_id: 7, panel_id: 3, exclusion: 'infra', page: 2, page_size: 100 },
  ])('round-trips %j', params => {
    expect(parseLiveParams(liveSearch(q(''), params))).toEqual(params)
  })

  it('resets the page on a filter change and keeps every other param', () => {
    const at = q('tab=live&user=9&level=flagged&live_page=3')
    const next = liveSearch(at, { exclusion: 'shared' })
    expect(next.has('live_page')).toBe(false)
    expect(next.get('live_excl')).toBe('shared')
    expect(next.get('user')).toBe('9')
    expect(next.get('level')).toBe('flagged')
    expect(liveSearch(at, { page: 5 }).get('live_page')).toBe('5')
    expect(liveSearch(q('live_user=7'), { user_id: undefined }).has('live_user')).toBe(false)
  })
})

// The records tab's filters, under their own `rec_` names. The time bounds
// are kept as the datetime-local text the admin typed (browser time); the
// tab turns them into instants when it asks.
describe('records params', () => {
  it('reads the defaults from an empty URL', () => {
    expect(parseRecordsParams(q(''))).toEqual({ page: 1, page_size: 25 })
  })

  it('reads every filter', () => {
    expect(parseRecordsParams(q('rec_user=7&rec_source=review&rec_since=2026-09-01T08:00&rec_until=2026-09-02T09:30:15'
      + '&rec_level=cleared&rec_event=dismissed&rec_page=3&rec_size=50')))
      .toEqual({
        user_id: 7, source: 'review', since: '2026-09-01T08:00', until: '2026-09-02T09:30:15',
        level: 'cleared', event: 'dismissed', page: 3, page_size: 50,
      })
  })

  // A value this build does not offer filters nothing: an old link's source,
  // a mistyped time, an event from a newer server.
  it('ignores malformed values', () => {
    expect(parseRecordsParams(q('rec_user=abc&rec_source=travel&rec_since=yesterday&rec_until=2026-13&rec_level=bogus'
      + '&rec_event=bogus&rec_page=0&rec_size=7')))
      .toEqual({ page: 1, page_size: 25 })
  })

  it.each([
    { page: 1, page_size: 25 },
    { user_id: 7, source: 'geo_auto', since: '2026-09-01T08:00', level: 'suspended', page: 2, page_size: 100 },
    { source: 'review', event: 'trusted', until: '2026-09-02T09:30', page: 1, page_size: 50 },
  ])('round-trips %j', params => {
    expect(parseRecordsParams(recordsSearch(q(''), params))).toEqual(params)
  })

  it('omits the defaults', () => {
    expect(recordsSearch(q('rec_source=geo&rec_page=3&rec_size=50'), { source: '', page: 1, page_size: 25 }).toString())
      .toBe('')
  })

  it('resets the page on a filter change and keeps every other param', () => {
    const at = q('tab=records&user=9&level=flagged&live_panel=2&rec_page=3')
    const next = recordsSearch(at, { event: 'dismissed' })
    expect(next.has('rec_page')).toBe(false)
    expect(next.get('rec_event')).toBe('dismissed')
    expect(next.get('tab')).toBe('records')
    expect(next.get('user')).toBe('9')
    expect(next.get('level')).toBe('flagged')
    expect(next.get('live_panel')).toBe('2')
    expect(recordsSearch(at, { page: 5 }).get('rec_page')).toBe('5')
    expect(recordsSearch(q('rec_user=7'), { user_id: undefined }).has('rec_user')).toBe(false)
  })
})
