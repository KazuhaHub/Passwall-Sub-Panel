import { describe, expect, it } from 'vitest'
import { xrayRequiresMLKEMFirst } from './xrayReality'

describe('xrayRequiresMLKEMFirst', () => {
  it.each([
    ['26.9.7', false],
    ['26.9.8', true],
    ['v26.9.9', true],
    ['Xray 26.10.0', true],
    ['', false],
    ['latest', false],
    ['1.14.0', false],
  ])('maps %s to %s', (version, expected) => {
    expect(xrayRequiresMLKEMFirst(version)).toBe(expected)
  })
})
