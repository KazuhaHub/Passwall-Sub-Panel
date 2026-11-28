// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { readChartTokens } from '@/hooks/useChartTokens'
import { metricsChartOption, metricsTimeFormatter } from './NodeMetricsChart'

const start = '2026-10-21T07:00:00Z'
const end = '2026-10-21T08:00:00Z'

describe('metrics charts', () => {
  it('uses a real time axis, turns the ECharts legend off and preserves gaps', () => {
    const option = metricsChartOption({
      times: [start, '2026-10-21T07:50:00Z', end],
      series: [{ name: '<b>CPU</b>', unit: 'percent', values: [20, null, 25] }],
      from: start, to: end, timeZone: 'America/Los_Angeles', locale: 'en-US',
      tk: readChartTokens(), coverageLabel: 'Coverage',
    }) as Record<string, any>
    expect(option.xAxis.type).toBe('time')
    expect(option.xAxis.min).toBe(Date.parse(start))
    expect(option.xAxis.max).toBe(Date.parse(end))
    expect(option.xAxis.axisLabel.hideOverlap).toBe(true)
    expect(option.xAxis.axisLabel.formatter(Date.parse(start))).toBe('00:00')
    expect(option.legend.show).toBe(false)
    expect(option.series[0].showSymbol).toBe(true) // three points
    expect(option.series[0].areaStyle.color.type).toBe('linear')
    expect(option.xAxis.axisLine.show).toBe(false)
    expect(option.series[0].data[1]).toEqual([Date.parse('2026-10-21T07:50:00Z'), null])
    expect(option.series[0].connectNulls).toBe(false)
    // The tooltip is HTML in the popover style, and a series name is escaped, never interpreted.
    const html = option.tooltip.formatter([{ seriesName: '<b>CPU</b>', value: [0, 20], dataIndex: 0 }])
    expect(html).toContain('&lt;b&gt;CPU&lt;/b&gt;')
    expect(html).not.toContain('<b>CPU</b>')
    expect(option.yAxis.axisLabel.formatter(20)).toBe('20.0%')
  })

  it('formats times in the configured timezone and tolerates invalid settings', () => {
    const format = metricsTimeFormatter('en-US', 'Asia/Tokyo', true)
    expect(format.format(Date.parse(start))).toContain('16:00')
    expect(() => metricsTimeFormatter('en-US', 'invalid/zone', false).format(Date.parse(start))).not.toThrow()
  })
})
