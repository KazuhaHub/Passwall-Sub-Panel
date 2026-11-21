import { describe, expect, it } from 'vitest'
import type { RuleSet } from '@/api/rules'
import { coverageOf, extractProxyGroups, extractRuleTargets, formHint } from './analysis'

const rs = (slug: string, content: string, enabled = true): RuleSet => ({
  slug, name: slug, sort: 1, enabled, direct_subscription_domain: false, proxy_group_order: [], content,
})
const sets = new Map([
  ['ads', rs('ads', '- DOMAIN-SUFFIX,ads.example,REJECT\n- DOMAIN-SUFFIX,a.example,Proxy,no-resolve\n# comment\n- MATCH,Fallback')],
  ['off', rs('off', '- DOMAIN,x.example,Hidden', false)],
])
const longYaml = (groups: string[]) => 'proxies: []\nproxy-groups:\n' + groups.map(g => `  - name: "${g}"\n    type: select\n`).join('') + 'rules: []\n'

describe('template coverage', () => {
  it('reads group names from the proxy-groups block only, without quotes', () => {
    expect([...extractProxyGroups(longYaml(['Proxy', 'Fallback']))]).toEqual(['Proxy', 'Fallback'])
    expect(extractProxyGroups('rules:\n  - name: not-a-group\n').size).toBe(0)
  })

  it('collects the targets of enabled rule sets, skipping built-ins, comments and no-resolve', () => {
    expect([...extractRuleTargets([...sets.values()])].sort()).toEqual(['Fallback', 'Proxy'])
  })

  it('says unbound, auto, missing or ok', () => {
    expect(coverageOf({ rule_sets: [], content: '' }, sets)).toEqual({ kind: 'none' })
    expect(coverageOf({ rule_sets: ['gone'], content: '' }, sets)).toEqual({ kind: 'none' })
    expect(coverageOf({ rule_sets: ['ads'], content: 'x: {{ proxy_groups }}' }, sets)).toEqual({ kind: 'auto' })
    expect(coverageOf({ rule_sets: ['ads'], content: longYaml(['Proxy']) }, sets)).toEqual({ kind: 'missing', names: ['Fallback'] })
    expect(coverageOf({ rule_sets: ['ads'], content: longYaml(['Proxy', 'Fallback']) }, sets)).toEqual({ kind: 'ok' })
  })

  it('words the editor hint from the unsaved draft', () => {
    expect(formHint({ rule_sets: [], content: '' }, sets)).toEqual({ tone: 'muted', key: 'admin:templates.hint.no_binding' })
    expect(formHint({ rule_sets: ['ads'], content: longYaml(['Proxy']) }, sets)).toMatchObject({ tone: 'bad', key: 'admin:templates.hint.missing_targets', params: { names: 'Fallback' } })
    expect(formHint({ rule_sets: ['ads'], content: longYaml(['Proxy', 'Fallback']) }, sets)).toMatchObject({ tone: 'good', key: 'admin:templates.hint.groups_ok', params: { n: 2 } })
    expect(formHint({ rule_sets: ['ads'], content: 'short' }, sets)).toEqual({ tone: 'muted', key: 'admin:templates.hint.incomplete' })
  })
})
