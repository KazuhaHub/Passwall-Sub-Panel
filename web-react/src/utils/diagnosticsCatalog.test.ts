import fs from 'node:fs'
import { describe, expect, it } from 'vitest'
import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import { flatten, type Nested } from '@/i18n/options'

import {
  CARD_ORDER,
  FAMILY_CATALOG,
  FAMILY_LABEL_GROUP,
  STAGE_GROUP_ORDER,
  WRITE_REASON_GROUP_ORDER,
  LIFECYCLE_ERROR_KINDS,
  LIFECYCLE_ERROR_STAGES,
  STAGE_GROUP,
  WRITE_REASON_GROUP,
  familyOf,
  labelFor,
  labelKey,
} from './diagnosticsCatalog'

// DRIFT GUARD AGAINST THE GO SOURCE, in the manner of api/riskCenter.test.ts.
// The diagnostics page groups, labels and explains every metric family the
// server declares, and every one of those tables is keyed by a string the
// compiler cannot check. A family added in Go without an entry here would
// still reach the page (the raw area lists anything it does not know under
// "other"), but it would arrive unexplained and on no card; a stage or a
// write reason added in Go would vanish from the breakdown it belongs to.
// Reading the declarations straight from the source turns each of those into
// a failing test on the day the Go side changes.

const BUNDLES: Array<[string, Set<string>]> = [
  ['zh-CN', new Set(Object.keys(flatten(zh as Nested)))],
  ['en-US', new Set(Object.keys(flatten(en as Nested)))],
]

/** Assert `diagnostics.<key>` exists in both bundles. */
function expectCopy(keys: string[]) {
  for (const [lang, bundle] of BUNDLES) {
    for (const k of keys) expect(bundle.has(`diagnostics.${k}`), `${lang} diagnostics.${k}`).toBe(true)
  }
}

function goSource(rel: string): string {
  return fs.readFileSync(new URL(`../../../${rel}`, import.meta.url), 'utf8')
}

interface Declared { type: 'counter' | 'gauge' | 'histogram'; labelled: boolean }

function declaredFamilies(): Map<string, Declared> {
  const src = goSource('internal/pkg/metrics/psp.go')
  const out = new Map<string, Declared>()
  for (const m of src.matchAll(/New(Counter|Gauge|Histogram)(Vec)?\(\s*"(psp_[a-z0-9_]+)"/g)) {
    out.set(m[3], { type: m[1].toLowerCase() as Declared['type'], labelled: m[2] === 'Vec' })
  }
  if (out.size === 0) throw new Error('psp.go no longer declares metrics through New*(…); update this test')
  return out
}

function pollStages(): string[] {
  const src = goSource('internal/service/traffic/traffic.go')
  const out = [...src.matchAll(/mark\("([a-z_]+)"/g)].map(m => m[1])
  if (out.length === 0) throw new Error('traffic.go no longer marks poll stages with mark("…"); update this test')
  return out
}

// The reasons a lifecycle write is attributed to: every `return "…"` inside
// lifecycleWriteReason, plus the label the caller uses when the panel could
// not be read at all.
function writeReasons(): string[] {
  const src = goSource('internal/service/sharedclient/sharedclient.go')
  const body = /func lifecycleWriteReason\([\s\S]*?\n}\n/.exec(src)
  const unread = /unreadReason := "([a-z_]+)"/.exec(src)
  if (!body || !unread) throw new Error('sharedclient.go no longer has lifecycleWriteReason / unreadReason; update this test')
  const out = [...body[0].matchAll(/return "([a-z_]+)"/g)].map(m => m[1])
  return [...out, unread[1]]
}

describe('FAMILY_CATALOG', () => {
  it('lists exactly the families psp.go declares', () => {
    const declared = [...declaredFamilies().keys()].sort()
    expect(Object.keys(FAMILY_CATALOG).sort()).toEqual(declared)
  })

  it('agrees with Go on each family’s type and whether it is labelled', () => {
    for (const [family, d] of declaredFamilies()) {
      const entry = FAMILY_CATALOG[family]
      expect(entry, family).toBeDefined()
      expect({ type: entry.type, labelled: entry.labelled }, family).toEqual(d)
    }
  })

  it('places every family on a card the page actually renders', () => {
    for (const [family, entry] of Object.entries(FAMILY_CATALOG)) {
      expect(CARD_ORDER, family).toContain(entry.card)
    }
  })

  it('has a label and a description for every family in both bundles', () => {
    expectCopy(Object.keys(FAMILY_CATALOG).flatMap(f => [`metric.${f}.label`, `metric.${f}.desc`]))
  })
})

describe('STAGE_GROUP', () => {
  it('covers exactly the stages the traffic poll marks', () => {
    expect(Object.keys(STAGE_GROUP).sort()).toEqual([...new Set(pollStages())].sort())
  })

  it('has a label for every stage and every group in both bundles', () => {
    expectCopy(Object.keys(STAGE_GROUP).map(st => `labels.stage.${labelKey(st)}`))
    expectCopy(STAGE_GROUP_ORDER.map(gr => `cards.poll.stages.${gr}`))
  })

  it('orders every group it uses', () => {
    expect([...new Set(Object.values(STAGE_GROUP))].sort()).toEqual([...STAGE_GROUP_ORDER].sort())
  })
})

describe('WRITE_REASON_GROUP', () => {
  it('covers every reason a lifecycle write can be attributed to', () => {
    const reasons = writeReasons()
    expect(reasons).toHaveLength(10)
    expect(Object.keys(WRITE_REASON_GROUP).sort()).toEqual([...reasons].sort())
  })

  it('has a label for every reason and every group in both bundles', () => {
    expectCopy(Object.keys(WRITE_REASON_GROUP).map(r => `labels.write_reason.${labelKey(r)}`))
    expectCopy(WRITE_REASON_GROUP_ORDER.map(gr => `labels.write_reason_group.${gr}`))
  })

  it('orders every group it uses', () => {
    expect([...new Set(Object.values(WRITE_REASON_GROUP))].sort()).toEqual([...WRITE_REASON_GROUP_ORDER].sort())
  })
})

// The two breakdowns of psp_lifecycle_sync_error_total. Their label values are
// fixed sets on the Go side (constants in psp.go for the step; the panel kinds
// in domain plus "unknown"), and the page names each one, so a new step or a
// new kind of panel has to arrive here as well.
function lifecycleErrorStages(): string[] {
  const src = goSource('internal/pkg/metrics/psp.go')
  const out = [...src.matchAll(/LifecycleErrorStage\w+\s*=\s*"([a-z_]+)"/g)].map(m => m[1])
  if (out.length === 0) throw new Error('psp.go no longer declares LifecycleErrorStage* constants; update this test')
  return out
}

function lifecycleErrorKinds(): string[] {
  const domain = goSource('internal/domain/types.go')
  const kinds = [...domain.matchAll(/PanelKind\w+\s+PanelKind\s*=\s*"([a-z0-9_]+)"/g)].map(m => m[1])
  const unknown = /LifecycleErrorPanelKindUnknown\s*=\s*"([a-z_]+)"/.exec(goSource('internal/pkg/metrics/psp.go'))
  if (kinds.length === 0 || !unknown) throw new Error('panel kinds or the unknown kind label moved; update this test')
  return [...kinds, unknown[1]]
}

describe('lifecycle failure breakdowns', () => {
  it('know every step psp.go can count a failure under', () => {
    expect([...LIFECYCLE_ERROR_STAGES].sort()).toEqual(lifecycleErrorStages().sort())
  })

  it('know every panel kind a failure can be counted under', () => {
    expect([...LIFECYCLE_ERROR_KINDS].sort()).toEqual(lifecycleErrorKinds().sort())
  })

  it('are catalogued as labelled counters on the user status sync card', () => {
    for (const family of ['psp_lifecycle_sync_error_stage_total', 'psp_lifecycle_sync_error_panel_kind_total']) {
      expect(FAMILY_CATALOG[family], family).toEqual({ card: 'lifecycle', type: 'counter', labelled: true })
    }
  })

  it('has a label for every step and every panel kind in both bundles', () => {
    expectCopy(LIFECYCLE_ERROR_STAGES.map(st => `labels.lifecycle_stage.${labelKey(st)}`))
    expectCopy(LIFECYCLE_ERROR_KINDS.map(k => `labels.panel_kind.${labelKey(k)}`))
  })
})

// The raw area names a labelled family's children through these groups. A
// group named here without copy would print every child under its raw value
// while looking translated everywhere else.
describe('FAMILY_LABEL_GROUP', () => {
  it('names only labelled families the catalogue knows', () => {
    for (const family of Object.keys(FAMILY_LABEL_GROUP)) {
      expect(FAMILY_CATALOG[family]?.labelled, family).toBe(true)
    }
  })

  it('points every family at a label group both bundles have', () => {
    for (const [lang, bundle] of BUNDLES) {
      for (const [family, group] of Object.entries(FAMILY_LABEL_GROUP)) {
        const any = [...bundle].some(k => k.startsWith(`diagnostics.labels.${group}.`))
        expect(any, `${lang} labels.${group} for ${family}`).toBe(true)
      }
    }
  })

  it('names the children the cards translate, with the groups they use', () => {
    expect(FAMILY_LABEL_GROUP).toMatchObject({
      psp_poll_stage_ms: 'stage',
      psp_panel_op_total: 'op',
      psp_lifecycle_sync_error_stage_total: 'lifecycle_stage',
      psp_saml_acs_failure_total: 'saml',
    })
  })
})

describe('familyOf', () => {
  it('reads an unlabelled series as its own family', () => {
    expect(familyOf('psp_poll_total')).toEqual({ family: 'psp_poll_total' })
  })

  it('splits a labelled child into family, label and value', () => {
    expect(familyOf('psp_capability_gap_total{capability=client.iplimit}')).toEqual({
      family: 'psp_capability_gap_total', label: 'capability', value: 'client.iplimit',
    })
  })

  // A label value is whatever the Go call site passed to With(); nothing
  // forbids an "=" inside it, and the series must still split on the first.
  it('keeps everything after the first "=" as the value', () => {
    expect(familyOf('psp_x_total{op=a=b}')).toEqual({ family: 'psp_x_total', label: 'op', value: 'a=b' })
  })
})

describe('labelKey and labelFor', () => {
  // i18next reads "." as nothing special here (keySeparator is false) but a
  // flattened bundle key with a dot in it is still a trap for every tool that
  // walks the JSON, so label values are folded to identifier characters.
  it('folds a label value into a safe key', () => {
    expect(labelKey('client.iplimit')).toBe('client_iplimit')
    expect(labelKey('ListInboundsSlim')).toBe('ListInboundsSlim')
    expect(labelKey('3xui')).toBe('3xui')
  })

  it('translates a known value', () => {
    const t = (k: string) => `T(${k})`
    const exists = (k: string) => k === 'admin:diagnostics.labels.op.GetClient'
    expect(labelFor(t, exists, 'op', 'GetClient')).toBe('T(admin:diagnostics.labels.op.GetClient)')
  })

  // A new op added in Go must still be readable on the day it ships, before
  // anyone has written its translation.
  it('falls back to the raw value when there is no translation', () => {
    const t = (k: string) => `T(${k})`
    expect(labelFor(t, () => false, 'op', 'BrandNewOp')).toBe('BrandNewOp')
  })
})
