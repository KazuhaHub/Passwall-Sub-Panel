import { describe, expect, it } from 'vitest'
import { isReservedSlug, legacyRuleSetTarget, newRuleSetPath, ruleSetPath } from './ruleSetRoutes'

describe('rule-set addresses', () => {
  it('puts the slug in the path and the source of a copy in the query', () => {
    expect(ruleSetPath('default_rules')).toBe('/admin/rules/default_rules')
    expect(ruleSetPath('a b/c')).toBe('/admin/rules/a%20b%2Fc')
    expect(newRuleSetPath()).toBe('/admin/rules/new')
    expect(newRuleSetPath('a b')).toBe('/admin/rules/new?copy=a%20b')
  })

  // The router matches a path without regard to case, so `NEW` would open the create page too.
  it.each(['new', 'New', 'NEW'])('%s is reserved', slug => expect(isReservedSlug(slug)).toBe(true))
  it.each(['news', 'new-1', '', 'old'])('%j is not reserved', slug => expect(isReservedSlug(slug)).toBe(false))
})

describe('legacyRuleSetTarget', () => {
  it.each([
    ['?edit=custom', '/admin/rules/custom'],
    ['?q=x&edit=custom', '/admin/rules/custom'],
    ['?edit=new', '/admin/rules/new'],
    ['?edit=new&copy=custom', '/admin/rules/new?copy=custom'],
  ])('%s -> %s', (search, to) => expect(legacyRuleSetTarget(search)).toBe(to))

  it.each(['', '?q=a', '?edit=', '?copy=custom'])('%s is not a link', search => {
    expect(legacyRuleSetTarget(search)).toBeNull()
  })
})
