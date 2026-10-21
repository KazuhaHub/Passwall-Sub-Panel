import { useEffect, useMemo, useRef } from 'react'
import { Box, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
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

/** Explicit, separate legend/plot/axis bands prevent theme defaults overlapping. */
export function metricsChartOption({ times, series, coverage, from, to, locale, timeZone, colors, textColor, lineColor, coverageLabel }: {
  times: string[]; series: MetricsSeries[]; coverage?: number[]; from?: string; to?: string
  locale?: string; timeZone: string; colors: string[]; textColor: string; lineColor: string; coverageLabel: string
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
      trigger: 'axis', confine: true,
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
    series: series.map(item => ({
      name: item.name, type: 'line',
      data: item.values.map((value, index) => [instants[index], value]),
      showSymbol: false, connectNulls: false, smooth: false, lineStyle: { width: 2 },
      emphasis: { focus: 'series' },
    })),
  }
}

export default function NodeMetricsChart({ times, series, coverage, height = 280, from, to, label }: Props) {
  const ref = useRef<HTMLDivElement | null>(null)
  const chartRef = useRef<echarts.ECharts | null>(null)
  const { t, i18n } = useTranslation('admin')
  const timeZone = useSiteStore(state => state.timezone)
  const { palette: { md } } = useTheme()
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
      colors: [md.primary, md.tertiary, md.secondary], textColor: md.onSurfaceVariant,
      lineColor: md.outlineVariant, coverageLabel: t('admin:nodeMetrics.coverage'),
    }), true)
  }, [times, visible, coverage, from, to, md, timeZone, i18n?.resolvedLanguage, t, hasData])

  if (!hasData) return <Box sx={{ minHeight: height, display: 'grid', placeItems: 'center' }}>
    <Typography color="text.secondary" variant="body2">{t('admin:nodeMetrics.noHistory')}</Typography>
  </Box>
  return <div ref={ref} style={{ width: '100%', height, minWidth: 0 }} role="img" aria-label={label} />
}
