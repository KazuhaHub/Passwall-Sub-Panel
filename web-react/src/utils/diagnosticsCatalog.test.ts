import fs from 'node:fs'
import { describe, expect, it } from 'vitest'

import {
  CARD_ORDER,
  FAMILY_CATALOG,
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

  it.todo('has a label and a description for every family in both bundles')
})

describe('STAGE_GROUP', () => {
  it('covers exactly the stages the traffic poll marks', () => {
    expect(Object.keys(STAGE_GROUP).sort()).toEqual([...new Set(pollStages())].sort())
  })

  it.todo('has a label for every stage in both bundles')
})

describe('WRITE_REASON_GROUP', () => {
  it('covers every reason a lifecycle write can be attributed to', () => {
    const reasons = writeReasons()
    expect(reasons).toHaveLength(10)
    expect(Object.keys(WRITE_REASON_GROUP).sort()).toEqual([...reasons].sort())
  })

  it.todo('has a label for every reason and every group in both bundles')
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
