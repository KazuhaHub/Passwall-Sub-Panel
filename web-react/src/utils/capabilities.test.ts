import { describe, expect, it } from 'vitest'
import { unenforceablePanels } from './capabilities'
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
