import { describe, expect, it } from 'vitest'
import { regionLabel, regionOptions } from './regions'

describe('regionLabel', () => {
  it('shows the flag, the English name and the upper-cased code', () => {
    expect(regionLabel('tw', 'en')).toBe('🇹🇼 Taiwan · TW')
  })
  it('returns a value that is not a two-letter code as it is', () => {
    expect(regionLabel('TW-N')).toBe('TW-N')
  })
  it('carries both the Chinese and the English name in zh-CN', () => {
    const label = regionLabel('TW', 'zh-CN')
    expect(label).toContain('Taiwan')
    expect(label).toMatch(/[一-鿿]/)
    expect(label.endsWith(' · TW')).toBe(true)
  })
})

describe('regionOptions', () => {
  it('lists the used regions first, in the given order, then every other country', () => {
    const opts = regionOptions(['US', 'TW'])
    expect(opts[0].value).toBe('US')
    expect(opts[1].value).toBe('TW')
    expect(opts.slice(2).map(o => o.value)).toContain('JP')
  })
  it('does not repeat a used region further down', () => {
    const values = regionOptions(['US', 'TW']).map(o => o.value)
    expect(values.filter(v => v === 'US')).toHaveLength(1)
    expect(values.filter(v => v === 'TW')).toHaveLength(1)
    expect(new Set(values).size).toBe(values.length)
  })
  it('keeps an older free-text region pickable', () => {
    expect(regionOptions(['Tokyo'])[0]).toEqual({ value: 'Tokyo', label: 'Tokyo' })
  })
})
