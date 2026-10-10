import { describe, expect, it } from 'vitest'
import { parseRecordsParams, recordsRequest, recordsSearch, type RecordsFilters } from './recordsParams'

const parse = (query = '', days = 30) => parseRecordsParams(new URLSearchParams(query), days)
const base = (): RecordsFilters => ({ since: '24h', group_by: 'none', include_trial: false, page: 1, page_size: 50 })
const now = new Date('2026-10-09T12:00:00Z').getTime()

describe('destination records URL grammar', () => {
  it('round-trips all supported filters without changing unrelated drawer or tab state', () => {
    const input: RecordsFilters = { user_id: 7, panel_id: 9, source: 'g12', action: 'deny', since: '7d', group_by: 'site', include_trial: true, page: 1, page_size: 100 }
    const previous = new URLSearchParams('tab=records&user=8&lst_state=problem&rec_page=5')
    const result = recordsSearch(previous, input, 30)
    expect(parseRecordsParams(result, 30)).toEqual(input)
    expect(result.get('tab')).toBe('records')
    expect(result.get('user')).toBe('8')
    expect(result.get('lst_state')).toBe('problem')
    expect(previous.get('rec_page')).toBe('5')
    expect(result.has('rec_page')).toBe(false)
    for (const group_by of ['none', 'site', 'user', 'policy'] as const) {
      expect(parseRecordsParams(recordsSearch(result, { group_by }, 30), 30).group_by).toBe(group_by)
    }
  })

  it('omits defaults and resets pagination on every filter change', () => {
    expect(recordsSearch(new URLSearchParams(), base(), 30).toString()).toBe('')
    expect(recordsSearch(new URLSearchParams('rec_page=4'), { source: 'p2', page: 9 }, 30).has('rec_page')).toBe(false)
    expect(recordsSearch(new URLSearchParams('rec_source=p2&rec_page=4'), { page: 6 }, 30).toString()).toBe('rec_source=p2&rec_page=6')
    expect(recordsSearch(new URLSearchParams('rec_page=4&rec_size=100'), { page_size: 50 }, 30).toString()).toBe('')
  })

  it('falls back for malformed values without rewriting the supplied URL', () => {
    const input = new URLSearchParams('rec_user=-1&rec_panel=7.5&rec_source=g12x1&rec_action=allow&rec_since=32d&rec_until=yesterday&rec_group_by=host&rec_trial=true&rec_page=NaN&rec_size=200')
    const before = input.toString()
    expect(parseRecordsParams(input, 365)).toEqual(base())
    expect(input.toString()).toBe(before)
    for (const value of ['0', '-1', '7.2', '1e3', 'Infinity', '9007199254740992']) {
      expect(parse(`rec_user=${value}&rec_panel=${value}&rec_page=${value}`)).toEqual(base())
    }
    for (const value of ['g0', 'p01', 'p-1', 'g12x1', 'P12', 'example.test', 'g9007199254740992']) {
      expect(parse(`rec_source=${value}`).source).toBeUndefined()
    }
  })

  it('bounds relative windows by retention and the maximum request range', () => {
    expect(parse('rec_since=1h', 1).since).toBe('1h')
    expect(parse('rec_since=1d', 1).since).toBe('1d')
    expect(parse('rec_since=7d', 2).since).toBe('24h')
    expect(parse('rec_since=31d', 365).since).toBe('31d')
    expect(parse('rec_since=32d', 365).since).toBe('24h')
    expect(parse('rec_since=01d').since).toBe('24h')
  })

  it('keeps valid browser-local timestamps and rejects impossible calendar dates', () => {
    const since = '2026-10-01T14:30:12.345', until = '2026-10-02T14:30'
    const result = recordsSearch(new URLSearchParams(), { since, until }, 30)
    expect(parseRecordsParams(result, 30)).toEqual({ ...base(), since, until })
    for (const date of ['2026-02-30T14:30', '2026-04-31T14:30', '2026-13-01T14:30', '2026-10-01T25:00', '2026-10-01T14:30Z']) {
      expect(parse(`rec_since=${date}`).since).toBe('24h')
    }
    expect(recordsSearch(result, { since: '24h' }, 30).has('rec_until')).toBe(false)
    expect(parse('rec_since=1h&rec_until=2026-10-02T14:30').until).toBeUndefined()
  })
})

describe('destination search stays in the request only', () => {
  it.each(['private-host.example', '100%_match!', '例子.测试', 'api.example.test/path?token=sensitive'])('never serializes %s into the page URL', keyword => {
    const previous = new URLSearchParams({ tab: 'records', rec_q: keyword, q: keyword, rec_page: '5' })
    const patch = { ...base(), q: keyword, rec_q: keyword } as Partial<RecordsFilters>
    const output = recordsSearch(previous, patch, 30)
    expect(output.has('rec_q')).toBe(false)
    expect(output.has('q')).toBe(false)
    expect(output.toString()).not.toContain(encodeURIComponent(keyword))
    expect(parseRecordsParams(previous, 30)).not.toHaveProperty('q')
    expect(recordsRequest(base(), `  ${keyword}  `, now)?.q).toBe(keyword)
  })

  it.each([
    ['block', 'block', 'policy'], ['deny', 'block', 'group'], ['observe', 'observe', 'policy'],
  ] as const)('maps %s to the intended action and source kind', (selected, action, source_kind) => {
    const input = parse(`rec_action=${selected}&rec_user=7&rec_panel=9&rec_source=p12&rec_trial=1&rec_group_by=user&rec_page=3&rec_size=25`)
    expect(recordsRequest(input, '', now)).toEqual({ user_id: 7, panel_id: 9, source: 'p12', action, source_kind,
      since: now - 86400000, until: now, include_trial: true, group_by: 'user', page: 3, page_size: 25 })
  })

  it('resolves relative and browser-local times, rejecting reversed or oversized custom ranges', () => {
    expect(recordsRequest(parse('rec_since=1h'), '', now)?.since).toBe(now - 3600000)
    const filters = { ...base(), since: '2026-10-01T14:30', until: '2026-10-02T14:30' }
    expect(recordsRequest(filters, '', now)).toMatchObject({ since: new Date(filters.since).getTime(), until: new Date(filters.until).getTime() })
    expect(recordsRequest({ ...filters, until: filters.since }, '', now)).toBeNull()
    expect(recordsRequest({ ...filters, until: '2026-12-01T14:30' }, '', now)).toBeNull()
    expect(recordsRequest({ ...base(), since: 'not a date' }, '', now)).toBeNull()
    expect(recordsRequest(base(), '', Number.NaN)).toBeNull()
  })
})
