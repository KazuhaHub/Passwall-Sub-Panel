// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { readChartTokens } from '@/hooks/useChartTokens'
import { areaGradient, chartBase, dashOf, lineSeries, tooltipHtml, tooltipFrame, withAlpha } from './chartKit'

describe('chartKit', () => {
  const tk = readChartTokens()
  it('turns the ECharts legend off and keeps hidden series unselected', () => {
    const o = chartBase(tk, ['a', 'b'], new Set(['b']))
    expect(o.legend).toEqual({ show: false, data: ['a', 'b'], selected: { b: false } })
  })
  it('cycles solid, dashed, dotted', () => {
    expect([0, 1, 2, 3].map(dashOf)).toEqual(['solid', 'dashed', 'dotted', 'solid'])
  })
  it('escapes names and values in the tooltip', () => {
    const html = tooltipHtml('<h>', [{ name: '<i>x</i>', value: '1 & 2', color: 'red', dash: 'solid' }], tk)
    expect(html).toContain('&lt;i&gt;x&lt;/i&gt;')
    expect(html).toContain('1 &amp; 2')
    expect(html).not.toContain('<i>')
  })
  it('fades the series colour to transparent from top to bottom', () => {
    const g = areaGradient('rgb(10, 20, 30)', 0.2)
    expect(g.colorStops).toEqual([{ offset: 0, color: 'rgba(10, 20, 30, 0.2)' }, { offset: 1, color: 'rgba(10, 20, 30, 0)' }])
    expect(withAlpha('#0a141e', 0.5)).toBe('rgba(10, 20, 30, 0.5)')
  })
  it('fills by default, leaves out the fill on request, and marks points only when there are few', () => {
    const few = lineSeries(0, tk, { name: 'a', data: [1, 2, 3] })
    expect(few.areaStyle?.color.type).toBe('linear')
    expect(few.showSymbol).toBe(true)
    expect(few.smoothMonotone).toBe('x')
    expect('areaStyle' in lineSeries(1, tk, { name: 'b', data: [1] }, { area: false })).toBe(false)
    expect(lineSeries(0, tk, { name: 'c', data: Array.from({ length: 100 }, (_, i) => i) }).showSymbol).toBe(false)
  })
  it('draws the axis pointer dashed and animates unless motion is reduced', () => {
    expect(tooltipFrame(tk).axisPointer.lineStyle.type).toBe('dashed')
    expect(chartBase(tk, [], new Set()).animation).toBe(true)
    const original = window.matchMedia
    window.matchMedia = ((q: string) => ({ matches: q.includes('reduce') })) as typeof window.matchMedia
    try { expect(chartBase(tk, [], new Set()).animation).toBe(false) } finally { window.matchMedia = original }
  })
})
