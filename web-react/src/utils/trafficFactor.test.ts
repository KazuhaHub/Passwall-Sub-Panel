import { describe, expect, it } from 'vitest'
import { factorTextToMilli, hasCustomFactor, milliToFactorText } from './trafficFactor'

describe('trafficFactor', () => {
  it('converts between the decimal multiplier and milli', () => {
    expect(milliToFactorText(1500)).toBe('1.5')
    expect(milliToFactorText(1000)).toBe('1')
    expect(milliToFactorText(0)).toBe('0')
    expect(milliToFactorText(undefined)).toBe('1')
    expect(factorTextToMilli('1.5')).toBe(1500)
    expect(factorTextToMilli('0.001')).toBe(1)
    expect(factorTextToMilli('1.1')).toBe(1100)
    expect(factorTextToMilli(' 100 ')).toBe(100000)
    expect(factorTextToMilli('0')).toBe(0)
  })
  it('rejects values outside 0..100 or not numeric', () => {
    for (const bad of ['', 'abc', '-1', '100.001', '1e2', '1.5.2']) expect(factorTextToMilli(bad)).toBeNull()
  })
  it('treats 1000 and a missing value as the default', () => {
    expect(hasCustomFactor(1000)).toBe(false)
    expect(hasCustomFactor(undefined)).toBe(false)
    expect(hasCustomFactor(0)).toBe(true)
    expect(hasCustomFactor(1500)).toBe(true)
  })
})
