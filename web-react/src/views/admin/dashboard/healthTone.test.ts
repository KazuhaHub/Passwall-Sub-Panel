import { describe, expect, it } from 'vitest'
import { healthTone } from './healthTone'

describe('dashboard health tone', () => {
  it('never paints inconclusive as healthy, and not as a failure', () => {
    expect(healthTone('inconclusive')).toBe('warning')
    expect(healthTone('inconclusive')).not.toBe(healthTone('ok'))
    expect(healthTone('inconclusive')).not.toBe(healthTone('unreachable'))
  })
  it('treats an unreachable node or a missing inbound as a failure', () => {
    for (const s of ['unreachable', 'panel_unreachable', 'inbound_missing']) expect(healthTone(s)).toBe('danger')
  })
  it('keeps never-probed and unknown states quiet instead of healthy', () => {
    for (const s of ['', 'something_new']) expect(healthTone(s)).toBe('neutral')
  })
})
