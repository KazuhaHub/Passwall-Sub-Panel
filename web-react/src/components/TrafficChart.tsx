import { useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { TrafficHistoryItem } from '@/api/traffic'
import { EmptyState } from '@/components/feedback/EmptyState'
import { chartBaseOption, useChartTokens } from '@/hooks/useChartTokens'
import { bytesToHuman } from '@/utils/trafficBytes'

// Register only what we actually render: a line chart with axis/tooltip/legend.
// Pulling the umbrella `echarts` package would ship every chart type and
// renderer (~1MB) for a single time-series plot.
echarts.use([LineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

interface Props {
  items: TrafficHistoryItem[]
  loading?: boolean
  height?: number
}

/**
 * Up / down / total traffic over time, drawn on a canvas with the app's colour tokens
 * (useChartTokens re-reads them when the mode or brand changes, so the chart repaints
 * without remounting).
 *
 * Three things beyond the picture, because a canvas says nothing to a screen reader and
 * hue alone says little to a colour-blind eye:
 *  - the three series differ in line style AND marker (solid circle, dashed square, dotted
 *    triangle), so the legend and the lines tell them apart without the palette;
 *  - the chart is a role="img" with a one-sentence summary (range and total);
 *  - the same numbers are available as a table under a disclosure, which is also the way
 *    to read an exact figure without hovering.
 * The y axis is in the same bytes format as every other traffic figure.
 */
export default function TrafficChart({ items, height = 360 }: Props) {
  const { t } = useTranslation('admin')
  const tk = useChartTokens()
  const ref = useRef<HTMLDivElement | null>(null)
  const chartRef = useRef<echarts.ECharts | null>(null)
  const empty = items.length === 0

  // The container is not rendered while there is nothing to draw, so the chart is created
  // and disposed with it rather than once per mount.
  useEffect(() => {
    if (empty || !ref.current) return
    const el = ref.current
    const chart = echarts.init(el, null, { renderer: 'canvas' })
    chartRef.current = chart
    // The sidebar collapsing resizes this box without a window resize, so watch the box itself.
    const observer = typeof ResizeObserver === 'function' ? new ResizeObserver(() => chart.resize()) : null
    observer?.observe(el)
    const onResize = () => chart.resize()
    window.addEventListener('resize', onResize)
    return () => {
      observer?.disconnect()
      window.removeEventListener('resize', onResize)
      chart.dispose()
      chartRef.current = null
    }
  }, [empty])

  const names = useMemo(() => ({
    up: t('traffic.trend.summary_up'),
    down: t('traffic.trend.summary_down'),
    total: t('traffic.trend.summary_total'),
  }), [t])

  useEffect(() => {
    const chart = chartRef.current
    if (!chart || empty) return
    const base = chartBaseOption(tk)
    chart.setOption({
      ...base,
      tooltip: {
        ...base.tooltip,
        trigger: 'axis',
        valueFormatter: (v: number | string) => bytesToHuman(Number(v)),
      },
      legend: { ...base.legend, data: [names.up, names.down, names.total], top: 4 },
      grid: { left: 64, right: 24, top: 40, bottom: 32 },
      xAxis: { ...base.xAxis, type: 'category', data: items.map(i => i.date), splitLine: { show: false } },
      yAxis: { ...base.yAxis, type: 'value', axisLabel: { ...base.yAxis.axisLabel, formatter: (v: number) => bytesToHuman(v) } },
      series: [
        { name: names.up, type: 'line', data: items.map(i => i.up_bytes), smooth: true, symbol: 'circle', symbolSize: 6, lineStyle: { type: 'solid', width: 2 }, areaStyle: { opacity: 0.1 } },
        { name: names.down, type: 'line', data: items.map(i => i.down_bytes), smooth: true, symbol: 'rect', symbolSize: 6, lineStyle: { type: 'dashed', width: 2 }, areaStyle: { opacity: 0.1 } },
        { name: names.total, type: 'line', data: items.map(i => i.total_bytes), smooth: true, symbol: 'triangle', symbolSize: 7, lineStyle: { type: 'dotted', width: 2.5 } },
      ],
    }, true)
  }, [items, tk, names, empty])

  if (empty) return <EmptyState compact title={t('traffic.chart.empty')} className="min-h-40" />

  const total = items.reduce((sum, i) => sum + i.total_bytes, 0)
  const summary = t('traffic.chart.aria', { from: items[0].date, to: items[items.length - 1].date, total: bytesToHuman(total) })

  return (
    <div>
      <div ref={ref} role="img" aria-label={summary} style={{ width: '100%', height }} />
      <details className="mt-2 text-sm">
        <summary className="inline-flex min-h-8 cursor-pointer items-center rounded text-muted-foreground outline-none select-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50">
          {t('traffic.chart.table')}
        </summary>
        <div className="mt-2 max-h-72 overflow-auto rounded-lg border">
          <table className="w-full text-sm tabular-nums">
            <caption className="sr-only">{summary}</caption>
            <thead className="sticky top-0 bg-muted text-left text-xs text-muted-foreground">
              <tr>
                <th scope="col" className="px-3 py-1.5 font-medium">{t('traffic.chart.col_date')}</th>
                <th scope="col" className="px-3 py-1.5 text-right font-medium">{names.up}</th>
                <th scope="col" className="px-3 py-1.5 text-right font-medium">{names.down}</th>
                <th scope="col" className="px-3 py-1.5 text-right font-medium">{names.total}</th>
              </tr>
            </thead>
            <tbody>
              {items.map(i => (
                <tr key={i.date} className="border-t">
                  <th scope="row" className="px-3 py-1.5 text-left font-normal">{i.date}</th>
                  <td className="px-3 py-1.5 text-right">{bytesToHuman(i.up_bytes)}</td>
                  <td className="px-3 py-1.5 text-right">{bytesToHuman(i.down_bytes)}</td>
                  <td className="px-3 py-1.5 text-right">{bytesToHuman(i.total_bytes)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
  )
}
