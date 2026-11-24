import { describe, expect, it } from 'vitest'
import { readChartTokens } from '@/hooks/useChartTokens'
import { chartBase, dashOf, tooltipHtml } from './chartKit'

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
})
