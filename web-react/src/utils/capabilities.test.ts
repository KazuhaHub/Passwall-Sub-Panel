import { describe, expect, it } from 'vitest'
import { deviceCapIsInert, unenforceablePanels } from './capabilities'
import type { Server } from '@/api/servers'

const srv = (name: string, capabilities: string[]): Server =>
  ({ name, capabilities } as unknown as Server)

const XUI = srv('xui-tokyo', ['client.write', 'client.iplimit', 'client.devicelimit'])
const SUI = srv('sui-osaka', ['client.write'])

describe('unenforceablePanels', () => {
  it('names the panels that cannot enforce the cap', () => {
    expect(unenforceablePanels(3, [XUI, SUI], 'client.iplimit')).toEqual(['sui-osaka'])
  })

  it('is silent when every panel can enforce it', () => {
    expect(unenforceablePanels(3, [XUI], 'client.iplimit')).toEqual([])
  })

  // Mirrors reportCapabilityGaps: a gap exists only when PSP intends to
  // enforce. Warning about an unlimited user would train the operator to
  // ignore the warning that matters.
  it('is silent when the user is unlimited', () => {
    expect(unenforceablePanels(0, [XUI, SUI], 'client.iplimit')).toEqual([])
    expect(unenforceablePanels(-1, [XUI, SUI], 'client.iplimit')).toEqual([])
  })

  it('is silent on a non-numeric limit rather than warning about everything', () => {
    expect(unenforceablePanels(NaN, [XUI, SUI], 'client.iplimit')).toEqual([])
  })

  // The two caps are independent: a panel may carry one and not the other.
  it('treats the two caps separately', () => {
    const partial = srv('partial', ['client.iplimit'])
    expect(unenforceablePanels(2, [partial], 'client.iplimit')).toEqual([])
    expect(unenforceablePanels(2, [partial], 'client.devicelimit')).toEqual(['partial'])
  })

  // Never probed / older build: absent or empty capabilities must warn, not
  // be assumed capable.
  it('treats a panel with no declared capabilities as lacking it', () => {
    expect(unenforceablePanels(2, [srv('unprobed', [])], 'client.iplimit')).toEqual(['unprobed'])
    expect(unenforceablePanels(2, [{ name: 'nocaps' } as unknown as Server], 'client.iplimit'))
      .toEqual(['nocaps'])
  })
})

// The device cap is stored on every panel and enforced by none. This pins the
// predicate that says so, and — separately — that the two forms which collect
// the number actually consult it. A correct predicate nobody calls is exactly
// the shape of gap that let the cap ship looking enforced for six releases.
describe('deviceCapIsInert', () => {
  it('warns on any cap an operator actually set', () => {
    expect(deviceCapIsInert(1)).toBe(true)
    expect(deviceCapIsInert(99)).toBe(true)
  })

  // Same rule as unenforceablePanels: a cap nobody set is not a broken
  // promise, and warning about it trains the operator to ignore the warning
  // that matters.
  it('is silent on unlimited and on garbage', () => {
    expect(deviceCapIsInert(0)).toBe(false)
    expect(deviceCapIsInert(-1)).toBe(false)
    expect(deviceCapIsInert(NaN)).toBe(false)
  })

  // Not derived from the server list, on purpose. Every panel that can STORE
  // the field still fails to enforce it, so a per-panel answer would be
  // confidently wrong rather than merely unhelpful.
  it('does not depend on which panels declare the capability', () => {
    expect(deviceCapIsInert(3)).toBe(true)
    expect(unenforceablePanels(3, [XUI], 'client.devicelimit')).toEqual([])
  })
})

describe('the views that collect a device cap consult the predicate', () => {
  // Read as source rather than rendered: these are two large MUI forms whose
  // fields are reached through dialogs and tabs, and a locator-based test that
  // silently matched nothing would report success. Deleting either call site
  // is the regression this exists to catch.
  it.each([
    ['UsersView', '../views/admin/UsersView.tsx'],
    ['GroupsView', '../views/admin/GroupsView.tsx'],
  ])('%s imports and calls deviceCapIsInert', async (_name, rel) => {
    const fs = await import('node:fs')
    const path = await import('node:path')
    const src = fs.readFileSync(path.resolve(__dirname, rel), 'utf8')
    expect(src).toMatch(/import \{[^}]*deviceCapIsInert[^}]*\} from '@\/utils\/capabilities'/)
    expect(src).toContain('deviceCapIsInert(')
    expect(src).toContain('device_limit_inert')
  })
})
