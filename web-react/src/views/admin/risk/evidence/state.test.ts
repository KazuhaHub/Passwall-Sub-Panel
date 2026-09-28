import { describe, expect, it } from 'vitest'

import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import { flatten, type Nested } from '@/i18n/options'
import { stateColor, stateLabelKey, type DetectorState } from './state'

// Every state a detector row can be in: the seven the server stores, and the
// one the page says for a kind with no row at all.
const STATES: readonly DetectorState[] = [
  'flagged', 'suspect', 'clean', 'unknown', 'idle', 'exempt', 'disabled', 'not_computed',
]

// Colour is the first thing an operator reads on these chips, so it has to
// track what they should DO rather than how alarming the word sounds.
describe('stateColor', () => {
  // Only flagged is actionable, and it must be the only one that looks like an
  // emergency — otherwise the ramp and the verdict are indistinguishable.
  it('reserves the alarm colour for the one actionable state', () => {
    expect(stateColor('flagged')).toBe('error')
    for (const s of STATES.filter(x => x !== 'flagged')) {
      expect(stateColor(s), s).not.toBe('error')
    }
  })

  // The single most important rule here. "Cannot tell" on a fleet whose geo
  // database has quietly stopped working looks EXACTLY like a clean fleet if
  // it is coloured like one — a detector that has stopped detecting would read
  // as a fleet with nobody sharing.
  it('never colours a non-verdict as clean', () => {
    for (const s of ['unknown', 'exempt', 'disabled', 'idle', 'not_computed'] as const) {
      expect(stateColor(s), s).not.toBe('success')
    }
    expect(stateColor('clean')).toBe('success')
    expect(stateColor('unknown')).toBe('info')
  })

  // Suspect is the visible ramp: distinct from both a flag and a clean row, so
  // the eventual flag does not appear out of nowhere.
  it('gives the ramp its own colour', () => {
    const suspect = stateColor('suspect')
    expect(suspect).toBe('warning')
    expect(suspect).not.toBe(stateColor('flagged'))
    expect(suspect).not.toBe(stateColor('clean'))
  })
})

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
