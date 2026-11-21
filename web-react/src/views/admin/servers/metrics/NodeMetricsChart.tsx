import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { EmptyState } from '@/components/feedback/EmptyState'
import { useChartTokens } from '@/hooks/useChartTokens'
import { useSiteStore } from '@/stores/site'

echarts.use([LineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

/** A null is a gap, never an idle measurement. */
export interface MetricsSeries {
  name: string
  unit: 'percent' | 'bps' | 'bytes' | 'ratio' | 'count' | 'milliseconds'
  values: (number | null)[]
}
interface Props {
  times: string[]
  series: MetricsSeries[]
  coverage?: number[]
  height?: number
  from?: string
  to?: string
  label?: string
}

export function formatValue(value: number, unit: MetricsSeries['unit']): string {
  switch (unit) {
    case 'percent': return `${value.toFixed(1)}%`
    case 'ratio': return value.toFixed(3)
    case 'bps': return formatBits(value)
    case 'bytes': return formatBytes(value)
    case 'milliseconds': return `${value.toFixed(0)} ms`
    default: return value.toFixed(0)
  }
}

// Network rates are bits per second; storage and memory gauges are bytes.
export function formatBits(bits: number): string {
  const units = ['bps', 'Kbps', 'Mbps', 'Gbps', 'Tbps']
  let unit = 0
  while (bits >= 1000 && unit < units.length - 1) { bits /= 1000; unit++ }
  return `${bits.toFixed(2)} ${units[unit]}`
}
export function formatBytes(bytes: number): string {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let unit = 0
  while (bytes >= 1024 && unit < units.length - 1) { bytes /= 1024; unit++ }
  return `${bytes.toFixed(2)} ${units[unit]}`
}

export function metricsTimeFormatter(locale: string | undefined, timeZone: string, withDate: boolean) {
  const options: Intl.DateTimeFormatOptions = {
    timeZone: timeZone || undefined, hour: '2-digit', minute: '2-digit', hour12: false,
    ...(withDate ? { month: '2-digit', day: '2-digit' } as const : {}),
  }
  try { return new Intl.DateTimeFormat(locale, options) }
  catch { return new Intl.DateTimeFormat(undefined, { ...options, timeZone: undefined }) }
}

const LINE_TYPES = ['solid', 'dashed', 'dotted'] as const

/** Explicit, separate legend/plot/axis bands prevent theme defaults overlapping. */
export function metricsChartOption({ times, series, coverage, from, to, locale, timeZone, colors, textColor, lineColor, coverageLabel, tooltipStyle }: {
  times: string[]; series: MetricsSeries[]; coverage?: number[]; from?: string; to?: string
  locale?: string; timeZone: string; colors: string[]; textColor: string; lineColor: string; coverageLabel: string
  /** Popover colours from the chart tokens; absent in tests, where the ECharts defaults are fine. */
  tooltipStyle?: { backgroundColor: string; borderColor: string; textStyle: { color: string } }
}): echarts.EChartsCoreOption {
  const instants = times.map(time => new Date(time).getTime())
  const span = Date.parse(to ?? times.at(-1) ?? '') - Date.parse(from ?? times[0] ?? '')
  const axisTime = metricsTimeFormatter(locale, timeZone, span >= 86400_000)
  const tooltipTime = metricsTimeFormatter(locale, timeZone, true)
  const unit = series[0]?.unit ?? 'count'
  return {
    backgroundColor: 'transparent', color: colors,
    textStyle: { fontFamily: 'inherit', color: textColor },
    grid: { left: 12, right: 20, top: 52, bottom: 12, containLabel: true },
    legend: {
      type: 'scroll', top: 0, left: 0, right: 0, bottom: 'auto',
      itemWidth: 16, itemHeight: 8, textStyle: { color: textColor, fontSize: 12 },
      pageTextStyle: { color: textColor },
    },
    tooltip: {
      trigger: 'axis', confine: true, ...tooltipStyle,
      // Canvas text avoids interpreting interface names or locale strings as HTML.
      renderMode: 'richText',
      formatter: (raw: unknown) => {
        const items = raw as { seriesName: string; value: [number, number | null]; dataIndex: number }[]
        if (!Array.isArray(items) || !items.length) return ''
        const index = items[0].dataIndex
        const lines = [tooltipTime.format(instants[index])]
        for (const item of items) {
          const value = item.value[1]
          lines.push(`${item.seriesName}: ${value == null ? '—' : formatValue(value, unit)}`)
        }
        if (coverage?.[index] !== undefined) lines.push(`${coverageLabel}: ${coverage[index]} s`)
        return lines.join('\n')
      },
    },
    xAxis: {
      type: 'time', min: from ? Date.parse(from) : undefined, max: to ? Date.parse(to) : undefined,
      splitNumber: 4,
      axisLabel: { color: textColor, hideOverlap: true, margin: 12, formatter: (value: number) => axisTime.format(value) },
      axisLine: { lineStyle: { color: lineColor } }, axisTick: { show: false },
    },
    yAxis: {
      type: 'value', min: 0, splitNumber: 3,
      axisLabel: { color: textColor, formatter: (value: number) => formatValue(value, unit) },
      splitLine: { lineStyle: { color: lineColor, opacity: 0.55 } },
    },
    series: series.map((item, index) => ({
      name: item.name, type: 'line',
      data: item.values.map((value, index) => [instants[index], value]),
      // Line style as well as hue tells two series apart (the same rule TrafficChart follows).
      showSymbol: false, connectNulls: false, smooth: false, lineStyle: { width: 2, type: LINE_TYPES[index % LINE_TYPES.length] },
      emphasis: { focus: 'series' },
    })),
  }
}

/**
 * One metric over time. Besides the canvas it offers the same numbers as a table under a
 * disclosure (a canvas says nothing to a screen reader, and a table is the way to read an exact
 * figure without hovering); the rows are built only while it is open, because a 90-day range is
 * a couple of thousand of them.
 */
export default function NodeMetricsChart({ times, series, coverage, height = 240, from, to, label }: Props) {
  const ref = useRef<HTMLDivElement | null>(null)
  const chartRef = useRef<echarts.ECharts | null>(null)
  const { t, i18n } = useTranslation('admin')
  const timeZone = useSiteStore(state => state.timezone)
  const tk = useChartTokens()
  const [tableOpen, setTableOpen] = useState(false)
  // Unavailable series must not look like a zero-valued host/container series.
  const visible = useMemo(() => series.filter(item => item.values.some(value => value != null)), [series])
  const hasData = times.length > 0 && visible.length > 0

  useEffect(() => {
    if (!ref.current || !hasData) return
    const chart = echarts.init(ref.current, null, { renderer: 'canvas' })
    chartRef.current = chart
    const resize = () => chart.resize()
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(resize)
    observer?.observe(ref.current)
    window.addEventListener('resize', resize)
    return () => {
      observer?.disconnect()
      window.removeEventListener('resize', resize)
      chart.dispose()
      chartRef.current = null
    }
  }, [hasData])

  useEffect(() => {
    chartRef.current?.setOption(metricsChartOption({
      times, series: visible, coverage, from, to, locale: i18n?.resolvedLanguage, timeZone,
      colors: tk.series, textColor: tk.mutedText, lineColor: tk.axisLine, coverageLabel: t('admin:nodeMetrics.coverage'),
      tooltipStyle: { backgroundColor: tk.tooltipBg, borderColor: tk.tooltipBorder, textStyle: { color: tk.tooltipText } },
    }), true)
  }, [times, visible, coverage, from, to, tk, timeZone, i18n?.resolvedLanguage, t, hasData])

  const timeFormat = useMemo(() => metricsTimeFormatter(i18n?.resolvedLanguage, timeZone, true), [i18n?.resolvedLanguage, timeZone])

  if (!hasData) return <EmptyState compact title={t('admin:nodeMetrics.noHistory')} className="min-h-32" />
  return (
    <div className="min-w-0">
      <div ref={ref} role="img" aria-label={label} style={{ width: '100%', height }} />
      <details className="mt-2 text-sm" onToggle={event => setTableOpen(event.currentTarget.open)}>
        <summary className="inline-flex min-h-8 cursor-pointer items-center rounded text-muted-foreground outline-none select-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50">
          {t('admin:traffic.chart.table')}
        </summary>
        {tableOpen && (
          <div className="mt-2 max-h-72 overflow-auto rounded-lg border">
            <table className="w-full text-sm tabular-nums">
              <caption className="sr-only">{label}</caption>
              <thead className="sticky top-0 bg-muted text-left text-xs text-muted-foreground">
                <tr>
                  <th scope="col" className="px-3 py-1.5 font-medium" />
                  {visible.map(item => <th key={item.name} scope="col" className="px-3 py-1.5 text-right font-medium">{item.name}</th>)}
                  {coverage && <th scope="col" className="px-3 py-1.5 text-right font-medium">{t('admin:nodeMetrics.coverage')}</th>}
                </tr>
              </thead>
              <tbody>
                {times.map((time, index) => (
                  <tr key={time} className="border-t">
                    <th scope="row" className="px-3 py-1.5 text-left font-normal whitespace-nowrap">{timeFormat.format(new Date(time))}</th>
                    {visible.map(item => {
                      const value = item.values[index]
                      return <td key={item.name} className="px-3 py-1.5 text-right">{value == null ? '—' : formatValue(value, item.unit)}</td>
                    })}
                    {coverage && <td className="px-3 py-1.5 text-right">{coverage[index] ?? '—'}</td>}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </details>
    </div>
  )
}
