import { describe, expect, it } from 'vitest'
import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import type { LimitEnforcement, LimitPanel } from '@/api/limitEnforcement'
import type { CapVerdict } from './limitEnforcement'
import { assessDeviceLimit, assessIPLimit, ipEnforcing, isMisleading } from './limitEnforcement'

function panel(over: Partial<LimitPanel> = {}): LimitPanel {
  return {
    panel_id: 1, name: 'de-fra', kind: 'xui', clients: 1,
    can_store_ip: true, can_store_device: true,
    ip_limit_enforcement: 'enforced',
    ...over,
  }
}

function d(over: Partial<LimitEnforcement> = {}): LimitEnforcement {
  return {
    ip_limit: 2, device_limit: 0, device_limit_enforced_anywhere: false,
    panels: [panel()],
    ...over,
  }
}

describe('ipEnforcing', () => {
  // Mirrors domain.IPLimitEnforcement.Enforcing(). If that Go method gains a
  // state, this must too — the two are one rule expressed twice.
  it('counts only the states where something actually happens', () => {
    expect(ipEnforcing('enforced')).toBe(true)
    // Windows: the client is disconnected but no IP ban follows. Still an
    // enforcement, just a weaker one.
    expect(ipEnforcing('disconnect_only')).toBe(true)
  })
  it('never counts an unread precondition as a working one', () => {
    for (const s of ['unknown', 'unsupported', 'disabled', 'not_installed', '']) {
      expect(ipEnforcing(s)).toBe(false)
    }
  })
})

describe('assessIPLimit', () => {
  it('says nothing when no cap is set', () => {
    expect(assessIPLimit(d({ ip_limit: 0 })).verdict).toBe('unlimited')
  })

  it('is exact when one panel with one client enforces', () => {
    const a = assessIPLimit(d())
    expect(a.verdict).toBe('exact')
    expect(a.enforcingRows).toBe(1)
  })

  // The cap is budgeted per (panel, email), so a user on three enforcing
  // panels can hold three times what the admin typed.
  it('multiplies by the number of enforcing panel-side rows', () => {
    const a = assessIPLimit(d({ panels: [
      panel({ panel_id: 1 }),
      panel({ panel_id: 2 }),
      panel({ panel_id: 3 }),
    ] }))
    expect(a.verdict).toBe('multiplied')
    expect(a.enforcingRows).toBe(3)
  })

  it('counts a second credential class on ONE panel as a second enforcer', () => {
    const a = assessIPLimit(d({ panels: [panel({ clients: 2 })] }))
    expect(a.verdict).toBe('multiplied')
    expect(a.enforcingRows).toBe(2)
  })

  // The rule that matters most: one hole makes the cap void, not merely
  // higher. The user routes everything through the panel that does not
  // enforce, so a "x2" reading would be actively wrong.
  it('is void, not multiplied, when any panel cannot store the field', () => {
    const a = assessIPLimit(d({ panels: [
      panel({ panel_id: 1 }),
      panel({ panel_id: 2, kind: 'sui', can_store_ip: false }),
    ] }))
    expect(a.verdict).toBe('void')
    expect(a.voidPanels.map(p => p.panel_id)).toEqual([2])
  })

  it('is void when a panel stores the value but fail2ban is off', () => {
    // The worst shape: the panel accepts the write and shows the number back,
    // so it looks configured while banning nobody.
    for (const state of ['disabled', 'not_installed']) {
      const a = assessIPLimit(d({ panels: [panel({ ip_limit_enforcement: state })] }))
      expect(a.verdict).toBe('void')
    }
  })

  it('separates an unread panel from a known-bad one', () => {
    for (const state of ['unknown', 'unsupported']) {
      const a = assessIPLimit(d({ panels: [panel({ ip_limit_enforcement: state })] }))
      expect(a.verdict).toBe('unknown')
      expect(a.voidPanels).toHaveLength(0)
      expect(a.unknownPanels).toHaveLength(1)
    }
  })

  it('reports an unreachable or deleted panel as unknown, never as enforcing', () => {
    for (const over of [{ panel_unreachable: true }, { panel_missing: true }]) {
      const a = assessIPLimit(d({ panels: [panel(over)] }))
      expect(a.verdict).toBe('unknown')
      expect(a.enforcingRows).toBe(0)
    }
  })

  // A hole outranks an unknown: the fleet-wide cap is already gone, and
  // reporting "we could not read one panel" would bury the certain finding
  // under the uncertain one.
  it('reports void ahead of unknown when both are present', () => {
    const a = assessIPLimit(d({ panels: [
      panel({ panel_id: 1, can_store_ip: false }),
      panel({ panel_id: 2, ip_limit_enforcement: 'unknown' }),
    ] }))
    expect(a.verdict).toBe('void')
  })

  it('says the user has no clients rather than claiming a working cap', () => {
    expect(assessIPLimit(d({ panels: [] })).verdict).toBe('no_clients')
  })
})

describe('assessDeviceLimit', () => {
  it('says nothing when no cap is set', () => {
    expect(assessDeviceLimit(d({ device_limit: 0 })).verdict).toBe('unlimited')
  })

  // The headline fact. Upstream counts a device only when a client app fetches
  // its subscription FROM THE PANEL, and PSP serves the subscriptions — so the
  // gate never fires for a PSP user on any panel version. This is not "no
  // panel happens to support it": no panel can.
  it('is never enforced, whatever the panels report', () => {
    const a = assessDeviceLimit(d({
      device_limit: 3,
      device_limit_enforced_anywhere: false,
      panels: [panel({ can_store_device: true })],
    }))
    expect(a.verdict).toBe('never_enforced')
  })

  it('falls back to the per-panel rules if a panel ever does enforce it', () => {
    // Guards the day the server flips the flag: the branch below must already
    // behave, rather than being written under pressure at that point.
    const a = assessDeviceLimit(d({
      device_limit: 3,
      device_limit_enforced_anywhere: true,
      panels: [panel({ panel_id: 1 }), panel({ panel_id: 2, can_store_device: false })],
    }))
    expect(a.verdict).toBe('void')
    expect(a.voidPanels.map(p => p.panel_id)).toEqual([2])
  })
})

describe('isMisleading', () => {
  it('flags exactly the verdicts where the typed number is not the enforced one', () => {
    const misleading: CapVerdict[] = ['multiplied', 'void', 'unknown', 'never_enforced']
    const honest: CapVerdict[] = ['unlimited', 'exact', 'no_clients']
    expect(misleading.every(isMisleading)).toBe(true)
    expect(honest.some(isMisleading)).toBe(false)
  })
})

// Every verdict the assessors can emit must have wording in BOTH bundles.
// The keys are computed at runtime, so a new verdict without copy renders as a
// raw key rather than failing to compile — and this repo has already shipped a
// missing en-US key that silently fell back to Chinese.
describe('locale coverage', () => {
  it('has wording in both bundles for every verdict that renders one', () => {
    const keys: Record<Exclude<CapVerdict, 'unlimited' | 'exact'>, string> = {
      never_enforced: 'device_limit_inert',
      void: 'cap_void',
      unknown: 'cap_unknown',
      multiplied: 'cap_multiplied',
      no_clients: 'cap_no_clients',
    }
    for (const key of Object.values(keys)) {
      expect(zh.users.field, `zh-CN missing ${key}`).toHaveProperty(key)
      expect(en.users.field, `en-US missing ${key}`).toHaveProperty(key)
    }
  })

  it('keeps the placeholders the view interpolates', () => {
    for (const bundle of [zh, en]) {
      const f = bundle.users.field as Record<string, string>
      expect(f.cap_void).toContain('{{panels}}')
      expect(f.cap_unknown).toContain('{{panels}}')
      for (const ph of ['{{total}}', '{{rows}}', '{{typed}}']) {
        expect(f.cap_multiplied).toContain(ph)
      }
    }
  })
})
