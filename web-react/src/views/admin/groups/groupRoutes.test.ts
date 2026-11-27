import { describe, expect, it } from 'vitest'
import { groupPath, legacyGroupTarget, NEW_GROUP_PATH, parseGroupId } from './groupRoutes'

describe('the addresses of the Groups section', () => {
  it('opens a group on a tab, leaving the first tab out of the address', () => {
    expect(groupPath(5)).toBe('/admin/groups/5')
    expect(groupPath(5, 'general')).toBe('/admin/groups/5')
    expect(groupPath(5, 'destination')).toBe('/admin/groups/5?tab=destination')
  })

  it('reads an id only when it is a positive whole number', () => {
    expect(parseGroupId('5')).toBe(5)
    for (const bad of [undefined, '', '0', '-1', '1.5', 'abc', '5x', '99999999999999999999']) expect(parseGroupId(bad)).toBeNull()
  })

  describe('the old side-sheet links on the list', () => {
    it('sends ?edit=<id> to that group and ?edit=new to the create page', () => {
      expect(legacyGroupTarget('?edit=5')).toBe('/admin/groups/5')
      expect(legacyGroupTarget('?edit=new')).toBe(NEW_GROUP_PATH)
    })

    it('keeps the tab the link carried (dtab), and drops one it does not know', () => {
      expect(legacyGroupTarget('?edit=5&dtab=destination')).toBe('/admin/groups/5?tab=destination')
      expect(legacyGroupTarget('?edit=5&dtab=general')).toBe('/admin/groups/5')
      expect(legacyGroupTarget('?edit=5&dtab=nope')).toBe('/admin/groups/5')
      expect(legacyGroupTarget('?edit=new&dtab=nodes')).toBe('/admin/groups/new?tab=nodes')
    })

    it('reads anything else as no link at all', () => {
      for (const s of ['', '?q=x', '?edit=', '?edit=abc', '?edit=0', '?edit=-3', '?edit=5x', '?dtab=nodes']) expect(legacyGroupTarget(s)).toBeNull()
    })
  })
})
