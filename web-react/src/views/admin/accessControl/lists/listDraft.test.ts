import { expect, it } from 'vitest'
import { listContentImpact, listIsProblem, listPreviewBlocksSave, listSourceLabel, validRemoteURL } from './listDraft'
import type { DestinationListSummary } from '@/api/accessControl'
const summary = { state: 'ready', last_fetched_at: 1000, parse_report_summary: { ignored_broad: 2 }, used_by: [{ kind: 'policy', id: 12 }] } as DestinationListSummary
it('does not call a filtered category a failed list', () => {
  expect(listIsProblem(summary, new Set([12]))).toBe(false)
  expect(listIsProblem({ ...summary, state: 'pending' }, new Set([12]))).toBe(true)
  expect(listIsProblem({ ...summary, state: 'pending' }, new Set())).toBe(false)
  expect(listIsProblem({ ...summary, state: 'failed' }, new Set())).toBe(true)
})
it('blocks invalid remote content but allows temporary fetch failure and custom exclusions', () => {
  expect(listPreviewBlocksSave('remote', 'dest_list_too_broad')).toBe(true)
  expect(listPreviewBlocksSave('remote', 'dest_list_empty_after_filter')).toBe(true)
  expect(listPreviewBlocksSave('remote', 'dest_list_fetch_failed')).toBe(false)
  expect(listPreviewBlocksSave('custom', '', 0)).toBe(false)
  expect(listPreviewBlocksSave('geosite', '', 0)).toBe(true)
  expect(listPreviewBlocksSave('geosite', '', 5)).toBe(false)
  expect(listPreviewBlocksSave('custom', 'dest_list_too_large')).toBe(true)
})
it('requires exact full digests rather than equal bounded entry samples', () => {
  expect(listContentImpact(1, 'full-a', 'full-a')).toBe('unchanged')
  expect(listContentImpact(1, 'full-a', 'full-b')).toBe('changes')
  expect(listContentImpact(1, undefined, 'full-b')).toBe('unknown')
  expect(listContentImpact(0, undefined, undefined)).toBe('unused')
})
it('admits only valid https remote addresses before any network preview', () => {
  expect(validRemoteURL('https://example.org/list.txt')).toBe(true)
  for (const input of ['http://example.org', 'example.org', 'https://', 'https://user:password@example.org/list']) expect(validRemoteURL(input)).toBe(false)
})
it('selects the source belonging to the list kind and keeps remote table labels compact', () => {
  expect(listSourceLabel({ ...summary, kind: 'remote', source_url: 'https://example.org/list.txt', geosite_category: 'old-category' })).toBe('example.org')
  expect(listSourceLabel({ ...summary, kind: 'geosite', source_url: 'https://old.example', geosite_category: 'category-finance' })).toBe('category-finance')
})
