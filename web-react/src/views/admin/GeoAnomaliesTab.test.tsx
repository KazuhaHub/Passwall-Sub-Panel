/** @vitest-environment jsdom */
import { describe, expect, it } from 'vitest'
import { stateColor } from './GeoAnomaliesTab'

// Colour is the first thing an operator reads on this table, so it has to
// track what they should DO rather than how alarming the word sounds.
describe('stateColor', () => {
  // Only flagged is actionable, and it must be the only one that looks like an
  // emergency — otherwise the ramp and the verdict are indistinguishable.
  it('reserves the alarm colour for the one actionable state', () => {
    expect(stateColor('flagged')).toBe('error')
    for (const s of ['suspect', 'clean', 'unknown', 'idle', 'exempt', 'disabled'] as const) {
      expect(stateColor(s)).not.toBe('error')
    }
  })

  // The single most important rule here. "Cannot tell" on a fleet whose geo
  // database has quietly stopped working looks EXACTLY like a clean fleet if
  // it is coloured like one — a detector that has stopped detecting would read
  // as a fleet with nobody sharing.
  it('never colours a non-verdict as clean', () => {
    for (const s of ['unknown', 'exempt', 'disabled', 'idle'] as const) {
      expect(stateColor(s)).not.toBe('success')
    }
    expect(stateColor('clean')).toBe('success')
  })

  // Suspect is the visible ramp: distinct from both a flag and a clean row, so
  // the eventual flag does not appear out of nowhere.
  it('gives the ramp its own colour', () => {
    const suspect = stateColor('suspect')
    expect(suspect).not.toBe(stateColor('flagged'))
    expect(suspect).not.toBe(stateColor('clean'))
  })
})
