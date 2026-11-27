import { describe, expect, it } from 'vitest'
import type { Group } from '@/api/types'
import { buildTagConditions, createRequest, dirtyFormTabs, EMPTY_FORM, formOf, parseTagConditions, sameForm, tagFilterOf, updateRequest } from './model'

const group = (over: Partial<Group> = {}): Group => ({
  id: 1, slug: 'g', name: 'G', tag_filter: { all: false, mode: 'all', tags: [] },
  layout: { separators: [], sort: [], default_sort_strategy: '' }, members: 0, ...over,
})

describe('tag conditions', () => {
  it('splits a stored filter by prefix, treats the legacy server: as node:, and keeps what it does not know', () => {
    expect(parseTagConditions(['region:US', ' tag: Premium ', 'server:1.1.1.1', 'node:2.2.2.2', 'vendor:gcp', 'bare', '', '  '])).toEqual({
      regions: ['US'], tags: ['Premium'], servers: ['1.1.1.1', '2.2.2.2'], custom: ['vendor:gcp', 'bare'],
    })
  })

  it('rebuilds them in the backend order and drops empties', () => {
    const f = { ...EMPTY_FORM, regions: ['US', ' '], tags: ['Premium'], servers: ['1.1.1.1'], custom_text: 'vendor:gcp, , bare' }
    expect(buildTagConditions(f)).toEqual(['region:US', 'tag:Premium', 'node:1.1.1.1', 'vendor:gcp', 'bare'])
  })

  it('sends no conditions when the group matches every node, and always names the conjunction', () => {
    const f = { ...EMPTY_FORM, all: true, regions: ['US'] }
    expect(tagFilterOf(f)).toEqual({ all: true, mode: 'all', tags: [] })
    expect(tagFilterOf({ ...f, all: false, mode: 'any' })).toEqual({ all: false, mode: 'any', tags: ['region:US'] })
  })
})

describe('the request bodies', () => {
  it('update: a limit left empty is cleared, a typed 0 is sent as 0, nothing is both', () => {
    const body = updateRequest({ ...EMPTY_FORM, name: 'N', traffic_limit_gb: null, ip_limit: 0, device_limit: 5 })
    expect(body).toEqual({
      name: 'N', tag_filter: { all: false, mode: 'all', tags: [] }, remark: '', require_2fa: false,
      clear_traffic_limit: true, ip_limit: 0, device_limit: 5,
    })
  })

  it('create: an empty limit is left out (that already means "states nothing"), a 0 is kept', () => {
    const body = createRequest({ ...EMPTY_FORM, slug: 's', name: 'N', traffic_limit_gb: 0, ip_limit: null, device_limit: null })
    expect(body).toEqual({ slug: 's', name: 'N', tag_filter: { all: false, mode: 'all', tags: [] }, remark: '', require_2fa: false, traffic_limit_gb: 0 })
  })
})

describe('reading a group into the form', () => {
  it('keeps null (states nothing) apart from 0 (uncapped), and an unknown mode becomes "all"', () => {
    const f = formOf(group({ traffic_limit_gb: 0, ip_limit: null, tag_filter: { all: false, mode: 'weird' as never, tags: ['region:US'] } }))
    expect(f).toMatchObject({ traffic_limit_gb: 0, ip_limit: null, device_limit: null, mode: 'all', regions: ['US'] })
  })

  it('compares forms by content', () => {
    expect(sameForm(formOf(group()), formOf(group()))).toBe(true)
    expect(sameForm(formOf(group()), { ...formOf(group()), remark: 'x' })).toBe(false)
  })
})

describe('which tabs of a group hold unsaved edits', () => {
  const saved = EMPTY_FORM
  it('names the tab each field belongs to, in the tabs\' own order', () => {
    expect(dirtyFormTabs(saved, saved)).toEqual([])
    expect(dirtyFormTabs({ ...saved, name: 'x' }, saved)).toEqual(['general'])
    expect(dirtyFormTabs({ ...saved, require_2fa: true, remark: 'r' }, saved)).toEqual(['general'])
    expect(dirtyFormTabs({ ...saved, mode: 'any', regions: ['HK'] }, saved)).toEqual(['nodes'])
    expect(dirtyFormTabs({ ...saved, ip_limit: 0 }, saved)).toEqual(['limits'])
    expect(dirtyFormTabs({ ...saved, ip_limit: 0, all: true, name: 'x' }, saved)).toEqual(['general', 'nodes', 'limits'])
  })
})
