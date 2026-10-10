import { expect, it } from 'vitest'
import { samplePolicy } from '@/test/accessControlFixtures'
import { policyInput, validatePolicyDraft, isCIDR, matchSummary, executionChanged } from './policyDraft'

it('builds the full update body without leaking overview/runtime fields', () => {
  expect(policyInput(samplePolicy)).toEqual({ name: 'No mail', action: 'block', list_ids: [], inline: { ports: '25,465,587', network: 'tcp' }, scope: 'all', group_ids: [], enabled: true, counts_as_risk: true, template_key: '' })
})
it('requires a name, a real condition, and group scope selections', () => {
  expect(validatePolicyDraft({ ...policyInput(samplePolicy), name: '', inline: {}, scope: 'groups', group_ids: [] })).toEqual({ name: 'required', match: 'no_match', group_ids: 'groups_required' })
  expect(validatePolicyDraft(policyInput(samplePolicy))).toEqual({})
})
it.each(['25,465,587', '1', '65535', '1000-2000', '80,443,1000-2000'])('accepts literal port grammar %s', ports => {
  expect(validatePolicyDraft({ ...policyInput(samplePolicy), inline: { ports } })).toEqual({})
})
it.each(['0', '65536', '2000-1000', '80x', '1.5', '80,', '80 443'])('rejects invalid ports %s', ports => {
  expect(validatePolicyDraft({ ...policyInput(samplePolicy), inline: { ports } })).toHaveProperty('ports', 'ports_invalid')
})
it.each(['10.0.0.0/8', '0.0.0.0/0', '2001:db8::/32', '::1/128', '::/0'])('accepts valid CIDR %s', value => expect(isCIDR(value)).toBe(true))
it.each(['10.0.0/8', '999.1.1.1/24', '01.1.1.1/24', '1.1.1.1/33', '::/129', ':::/32', 'example.com/24', '1.1.1.1', '1.1.1.1/-1'])('rejects malformed CIDR %s', value => expect(isCIDR(value)).toBe(false))
it('reports original line numbers for CIDR errors', () => {
  const draft = { ...policyInput(samplePolicy), inline: { cidrs: ['10.0.0.0/8', '', 'wrong/24'] } }
  expect(validatePolicyDraft(draft)).toHaveProperty('cidrs', '3')
})
it('summarizes match fields and splits BT only when a destination condition is also present', () => {
  const describe = (inline: typeof samplePolicy.inline, list_ids: number[] = []) => matchSummary({ ...policyInput(samplePolicy), inline, list_ids })
  expect(describe({}, [1])).toEqual({ list_ids: [1], ports: '', network: '', cidrs: 0, bt: false, private: false, split: false })
  expect(describe({ ports: '80' }, [1])).toMatchObject({ list_ids: [1], ports: '80', split: false })
  expect(describe({ protocols: ['bittorrent'], ports: '80' })).toMatchObject({ bt: true, split: false })
  expect(describe({ protocols: ['bittorrent'], ports: '80' }, [1])).toMatchObject({ bt: true, split: true })
})
it('does not predict restart for name/risk-only edits, but detects execution and enablement edits', () => {
  const seed = policyInput(samplePolicy)
  expect(executionChanged({ ...seed, name: 'Renamed', counts_as_risk: false }, seed)).toBe(false)
  expect(executionChanged({ ...seed, inline: { ports: '587' } }, seed)).toBe(true)
  expect(executionChanged({ ...seed, enabled: false }, seed)).toBe(true)
})
