import { describe, expect, it } from 'vitest'
import { deviceCapIsInert, ipCapBadgeTone, ipCapUnenforcedPanels, unenforceablePanels } from './capabilities'
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

describe('ipCapUnenforcedPanels', () => {
  const node = (name: string, ip_limit_enforcement?: string): Server =>
    ({ name, capabilities: ['client.iplimit'], ip_limit_enforcement } as unknown as Server)

  it('names the nodes where the cap is stored and ignored', () => {
    expect(ipCapUnenforcedPanels(3, [
      node('has-fail2ban', 'enforced'),
      node('no-fail2ban', 'not_installed'),
      node('env-var-off', 'disabled'),
    ])).toEqual(['no-fail2ban', 'env-var-off'])
  })

  // The whole point of holding "unknown" and "unsupported" apart from the
  // fault states. Naming them here would accuse an operator of a problem that
  // may not exist, and would put a permanent warning on every pre-3.7.0 panel
  // — which is how a warning stops being read.
  it('does not accuse a node it could not read', () => {
    expect(ipCapUnenforcedPanels(3, [
      node('never-answered', 'unknown'),
      node('too-old-to-answer', 'unsupported'),
      node('not-probed-at-all', undefined),
    ])).toEqual([])
  })

  // Windows disconnects the client; it just cannot ban the IP afterwards.
  // Reporting it as inert would send operators chasing a fail2ban install that
  // upstream does not use there.
  it('does not name a windows node, which does disconnect', () => {
    expect(ipCapUnenforcedPanels(3, [node('win-node', 'disconnect_only')])).toEqual([])
  })

  it('is silent when the user is unlimited', () => {
    const dead = [node('no-fail2ban', 'not_installed')]
    expect(ipCapUnenforcedPanels(0, dead)).toEqual([])
    expect(ipCapUnenforcedPanels(-1, dead)).toEqual([])
    expect(ipCapUnenforcedPanels(NaN, dead)).toEqual([])
  })

  // A panel that cannot STORE the field and a node that cannot ACT on it are
  // different faults with different fixes, and this helper answers only the
  // second. An S-UI panel carries no ip_limit_enforcement at all and must not
  // be swept in here — unenforceablePanels already reports it.
  it('leaves the storage question to unenforceablePanels', () => {
    const sui = ({ name: 'sui-osaka', capabilities: ['client.write'] } as unknown as Server)
    expect(ipCapUnenforcedPanels(3, [sui])).toEqual([])
    expect(unenforceablePanels(3, [sui], 'client.iplimit')).toEqual(['sui-osaka'])
  })
})

describe('the views that surface a dead IP cap consult the helpers', () => {
  // Same reasoning as the device-cap guard above: these call sites live inside
  // dialogs and table cells that a locator-based test could silently fail to
  // reach. Deleting either one is the regression — the code would still work,
  // and a node whose IP cap does nothing would go back to looking fine.
  it('UsersView warns where the limit is typed', async () => {
    const fs = await import('node:fs')
    const path = await import('node:path')
    const src = fs.readFileSync(path.resolve(__dirname, '../views/admin/UsersView.tsx'), 'utf8')
    expect(src).toMatch(/import \{[^}]*ipCapUnenforcedPanels[^}]*\} from '@\/utils\/capabilities'/)
    expect(src).toContain('ipCapUnenforcedPanels(')
    expect(src).toContain('ip_limit_unenforced')
  })

  it('ServersView badges the node itself', async () => {
    const fs = await import('node:fs')
    const path = await import('node:path')
    const src = fs.readFileSync(path.resolve(__dirname, '../views/admin/ServersView.tsx'), 'utf8')
    expect(src).toMatch(/import \{[^}]*ipCapBadgeTone[^}]*\} from '@\/utils\/capabilities'/)
    expect(src).toContain('ipCapBadge(s)')
  })
})

describe('ipCapBadgeTone', () => {
  it('flags the two states an operator has to fix', () => {
    expect(ipCapBadgeTone('disabled')).toBe('error')
    expect(ipCapBadgeTone('not_installed')).toBe('error')
  })

  // A broken probe renders like a healthy fleet the moment "cannot tell" is
  // drawn the same as "fine". Same rule the geo tab holds: unknown is never
  // silent and never green.
  it('never lets "cannot tell" look like a clean node', () => {
    expect(ipCapBadgeTone('unknown')).toBe('neutral')
    expect(ipCapBadgeTone('unknown')).not.toBe(ipCapBadgeTone('enforced'))
  })

  // Windows disconnects but cannot ban. Drawn distinctly from both a fault and
  // a fully-enforcing node, or the operator cannot tell which they are looking
  // at.
  it('keeps disconnect-only distinct from both a fault and a clean node', () => {
    expect(ipCapBadgeTone('disconnect_only')).toBe('info')
    expect(ipCapBadgeTone('disconnect_only')).not.toBe(ipCapBadgeTone('not_installed'))
    expect(ipCapBadgeTone('disconnect_only')).not.toBe(ipCapBadgeTone('enforced'))
  })

  // The clean state carries no decoration, like a supported compat version.
  it('says nothing about a node that enforces', () => {
    expect(ipCapBadgeTone('enforced')).toBe('none')
  })

  // A deliberate blind spot, not an oversight: a pre-3.7.0 panel cannot answer,
  // so a badge there would be permanent and un-actionable — and the ones
  // operators learn to ignore are the ones that matter elsewhere. Recorded in
  // docs/connection-limits.md §5.2.
  it('stays quiet about a panel too old to answer', () => {
    expect(ipCapBadgeTone('unsupported')).toBe('none')
  })

  // S-UI never carries the field at all.
  it('stays quiet when there is no verdict at all', () => {
    expect(ipCapBadgeTone(undefined)).toBe('none')
  })
})
