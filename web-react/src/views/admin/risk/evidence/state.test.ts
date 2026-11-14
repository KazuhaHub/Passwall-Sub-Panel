import { describe, expect, it } from 'vitest'

import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import { flatten, type Nested } from '@/i18n/options'
import { stateLabelKey, stateTone, type DetectorState } from './state'

// Every state a detector row can be in: the seven the server stores, and the
// one the page says for a kind with no row at all.
const STATES: readonly DetectorState[] = [
  'flagged', 'suspect', 'clean', 'unknown', 'idle', 'exempt', 'disabled', 'not_computed',
]

describe('stateLabelKey', () => {
  it('names every state by the one vocabulary', () => {
    for (const s of STATES) expect(stateLabelKey(s)).toBe(`risk_center.state.${s}`)
    // Idle is "no data" for every detector: the geo tab's old "no
    // connection" and the risk tab's "no data" were two words for one state.
    expect(stateLabelKey('idle')).toBe('risk_center.state.idle')
  })

  // An admin's trust is a decision about the account, not a policy exemption
  // (an ignore list, allow-anywhere): the two must read differently, or a
  // trusted account looks like one a setting happens to cover.
  it('names an exemption by trust as trusted', () => {
    expect(stateLabelKey('exempt', 'trusted')).toBe('risk_center.state.trusted')
    expect(stateLabelKey('exempt', 'allow_anywhere')).toBe('risk_center.state.exempt')
    expect(stateLabelKey('exempt', 'exempt')).toBe('risk_center.state.exempt')
    expect(stateLabelKey('exempt')).toBe('risk_center.state.exempt')
    // Only the exempt state carries it: a code "trusted" beside another state
    // is not a trust verdict and must not be relabelled as one.
    expect(stateLabelKey('clean', 'trusted')).toBe('risk_center.state.clean')
  })

  // A key the bundles lack would print as the raw key on every chip.
  it('every key it can return exists in both shipped bundles', () => {
    const keys = [...STATES.map(s => stateLabelKey(s)), stateLabelKey('exempt', 'trusted')]
    for (const [lang, bundle] of [['zh-CN', zh], ['en-US', en]] as const) {
      const flat = flatten(bundle as Nested)
      for (const k of keys) {
        expect(typeof flat[k] === 'string' && flat[k] !== '', `${lang} ${k}`).toBe(true)
      }
    }
  })
})

// The badge's tone is chosen from the closed table (components via
// utils/accessControl): colour still follows what the operator should DO.
describe('stateTone', () => {
  it('keeps the colour logic of stateColor: only flagged is the alarm row, suspect the amber ramp, clean the one green', () => {
    expect(stateTone('flagged')).toBe('failing')
    expect(stateTone('suspect')).toBe('attention')
    expect(stateTone('clean')).toBe('ok')
    for (const s of STATES.filter(x => x !== 'clean')) expect(stateTone(s), s).not.toBe('ok')
    for (const s of STATES.filter(x => x !== 'flagged')) expect(stateTone(s), s).not.toBe('failing')
  })

  it('gives each non-verdict its own quiet row: not judged, exempt, switched off, nothing to judge', () => {
    expect(stateTone('unknown')).toBe('unknown')
    expect(stateTone('not_computed')).toBe('unknown')
    expect(stateTone('exempt')).toBe('exempt')
    expect(stateTone('disabled')).toBe('paused')
    expect(stateTone('idle')).toBe('none')
  })

  it('a clean verdict that stands on a floor of its sources is never the green row', () => {
    expect(stateTone('clean', false)).toBe('unknown')
    expect(stateTone('clean', true)).toBe('ok')
    // A flag or a ramp on a floor is an understatement at worst and keeps its row.
    expect(stateTone('flagged', false)).toBe('failing')
    expect(stateTone('suspect', false)).toBe('attention')
  })
})
