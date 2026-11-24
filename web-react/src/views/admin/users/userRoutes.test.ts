import { describe, expect, it } from 'vitest'
import { legacyUserTarget, userPath } from './userRoutes'

describe('userPath', () => {
  it('leaves the first tab out of the address', () => {
    expect(userPath(7)).toBe('/admin/users/7')
    expect(userPath(7, 'overview')).toBe('/admin/users/7')
    expect(userPath(7, 'settings')).toBe('/admin/users/7?tab=settings')
  })
})

describe('legacyUserTarget', () => {
  it.each([
    ['?user=7', '/admin/users/7'],
    ['?user=7&tab=security', '/admin/users/7?tab=security'],
    ['?q=a&user=7&page=3', '/admin/users/7'],
    ['?user=new', '/admin/users/new'],
  ])('%s -> %s', (search, to) => expect(legacyUserTarget(search)).toBe(to))

  it.each(['', '?q=a', '?user=', '?user=0', '?user=-3', '?user=7.5', '?user=07x', '?user=abc', '?user=99999999999999999999'])('%s is not a link', search => {
    expect(legacyUserTarget(search)).toBeNull()
  })
})
