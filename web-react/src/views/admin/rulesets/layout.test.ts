import { describe, expect, it } from 'vitest'
import { DOCK_MIN, testerPlacement, toolbarTier } from './layout'

// The Rules tab decides its layout from the width of its OWN box (what the page leaves after the sidebar and the padding), not from
// the window: the same window shows 976px of it with the sidebar open and 1232px with it closed.
describe('where the routing test goes', () => {
  it('is nowhere while it is closed, and for a sub-rule, which has no routing test', () => {
    expect(testerPlacement({ open: false, width: 2000, phone: false, subRule: false })).toBe('closed')
    expect(testerPlacement({ open: true, width: 2000, phone: false, subRule: true })).toBe('closed')
  })
  it('is a screen of its own on a phone', () => {
    expect(testerPlacement({ open: true, width: 343, phone: true, subRule: false })).toBe('screen')
  })
  it('is a third column only when the directory, the group and the test all keep their room', () => {
    expect(testerPlacement({ open: true, width: DOCK_MIN, phone: false, subRule: false })).toBe('dock')
    expect(testerPlacement({ open: true, width: DOCK_MIN - 1, phone: false, subRule: false })).toBe('overlay')
  })
  it('is a panel over the group at the width of a 1280px window with the sidebar open (976px)', () => {
    expect(testerPlacement({ open: true, width: 976, phone: false, subRule: false })).toBe('overlay')
  })
  it('is a panel over the group while the width is not known yet (a first render, a test environment)', () => {
    expect(testerPlacement({ open: true, width: null, phone: false, subRule: false })).toBe('overlay')
  })
})

describe('what the toolbar keeps in view', () => {
  it('shows everything, counts included, from 960px', () => {
    expect(toolbarTier(976)).toBe('full')
    expect(toolbarTier(960)).toBe('full')
  })
  it('drops the counts (the directory says them) from 760px and moves the least used controls into More under that', () => {
    expect(toolbarTier(959)).toBe('compact')
    expect(toolbarTier(760)).toBe('compact')
    expect(toolbarTier(759)).toBe('narrow')
    expect(toolbarTier(343)).toBe('narrow')
  })
  it('shows everything while the width is not known yet', () => {
    expect(toolbarTier(null)).toBe('full')
  })
})
