import { useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { TrafficHistoryItem } from '@/api/traffic'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ChartLegend, useHiddenSeries } from '@/components/common/ChartLegend'
import { useChartTokens } from '@/hooks/useChartTokens'
import { chartBase, dashOf, lineSeries, tooltipFrame, tooltipHtml } from '@/lib/chartKit'
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
 *  - the three series differ in line pattern (solid, dashed, dotted), drawn the same in the HTML legend, so they are
 *    told apart without the palette; the options come from lib/chartKit like every other chart;
 *  - the chart is a role="img" with a one-sentence summary (range and total);
 *  - the same numbers are available as a table under a disclosure, which is also the way
 *    to read an exact figure without hovering.
 * The y axis is in the same bytes format as every other traffic figure.
 */
export default function TrafficChart({ items, height = 360 }: Props) {
  const { t } = useTranslation('common')
  const tk = useChartTokens()
  const { hidden, toggle } = useHiddenSeries()
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
    up: t('traffic_chart.up'),
    down: t('traffic_chart.down'),
    total: t('traffic_chart.total'),
  }), [t])

  useEffect(() => {
    const chart = chartRef.current
    if (!chart || empty) return
    const base = chartBase(tk, [names.up, names.down, names.total], hidden)
    const rows = [names.up, names.down, names.total]
    chart.setOption({
      ...base,
      tooltip: {
        ...tooltipFrame(tk),
        formatter: (raw: unknown) => {
          const list = raw as { axisValueLabel?: string; seriesName: string; value: number | string; dataIndex: number }[]
          if (!Array.isArray(list) || !list.length) return ''
          return tooltipHtml(list[0].axisValueLabel ?? '', list.map(p => ({
            name: p.seriesName, value: bytesToHuman(Number(p.value)), color: tk.series[rows.indexOf(p.seriesName)] ?? tk.series[0], dash: dashOf(rows.indexOf(p.seriesName)),
          })), tk)
        },
      },
      grid: { left: 64, right: 24, top: 16, bottom: 32 },
      xAxis: { ...base.xAxis, type: 'category', data: items.map(i => i.date) },
      yAxis: { ...base.yAxis, type: 'value', axisLabel: { ...base.yAxis.axisLabel, formatter: (v: number) => bytesToHuman(v) } },
      series: [
        lineSeries(0, { name: names.up, data: items.map(i => i.up_bytes) }),
        lineSeries(1, { name: names.down, data: items.map(i => i.down_bytes) }),
        lineSeries(2, { name: names.total, data: items.map(i => i.total_bytes) }),
      ],
    }, true)
  }, [items, tk, names, empty, hidden])

  if (empty) return <EmptyState compact title={t('traffic_chart.empty')} className="min-h-40" />

  const total = items.reduce((sum, i) => sum + i.total_bytes, 0)
  const summary = t('traffic_chart.aria', { from: items[0].date, to: items[items.length - 1].date, total: bytesToHuman(total) })

  return (
    <div>
      <ChartLegend className="mb-1" hidden={hidden} onToggle={toggle}
        items={[names.up, names.down, names.total].map((name, i) => ({ name, color: tk.series[i], dash: dashOf(i) }))} />
      <div ref={ref} role="img" aria-label={summary} style={{ width: '100%', height }} />
      <details className="mt-2 text-sm">
        <summary className="inline-flex min-h-8 cursor-pointer items-center rounded text-muted-foreground outline-none select-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50">
          {t('traffic_chart.table')}
        </summary>
        <div className="mt-2 max-h-72 overflow-auto rounded-lg border">
          <table className="w-full text-sm tabular-nums">
            <caption className="sr-only">{summary}</caption>
            <thead className="sticky top-0 bg-muted text-left text-xs text-muted-foreground">
              <tr>
                <th scope="col" className="px-3 py-1.5 font-medium">{t('traffic_chart.col_date')}</th>
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
