import { useEffect, useRef } from 'react'
import { useTheme } from '@mui/material'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

// Only what this chart renders, as TrafficChart does: the umbrella package would
// ship every chart type for one line plot.
echarts.use([LineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

/** One plotted series. A null is a GAP and is drawn as a break. */
export interface MetricsSeries {
  name: string
  /** Selects the axis formatter and the tooltip's units. */
  unit: 'percent' | 'bps' | 'bytes' | 'ratio' | 'count' | 'milliseconds'
  values: (number | null)[]
}

interface Props {
  /** ISO instants, one per point of every series. */
  times: string[]
  series: MetricsSeries[]
  /**
   * Seconds each point actually covers. A partial bucket is drawn with its
   * coverage shown rather than as a full interval, so an hour that was only half
   * observed is not read as a fully healthy one.
   */
  coverage?: number[]
  height?: number
}

function formatValue(value: number, unit: MetricsSeries['unit']): string {
  switch (unit) {
    case 'percent':
      return `${value.toFixed(1)}%`
    case 'ratio':
      return value.toFixed(3)
    case 'bps':
      return formatBits(value)
    case 'bytes':
      return formatBytes(value)
    case 'milliseconds':
      return `${value.toFixed(0)} ms`
    default:
      return value.toFixed(0)
  }
}

/**
 * Byte RATES are formatted as bits and byte GAUGES as bytes, deliberately: a
 * link is quoted in bits and a disk in bytes, and showing a throughput in
 * "MB/s" when an operator thinks in "Mbps" is a units bug with a plausible face.
 */
function formatBits(bitsPerSecond: number): string {
  const units = ['bps', 'Kbps', 'Mbps', 'Gbps', 'Tbps']
  let value = bitsPerSecond
  let unit = 0
  while (value >= 1000 && unit < units.length - 1) {
    value /= 1000
    unit++
  }
  return `${value.toFixed(2)} ${units[unit]}`
}

function formatBytes(bytes: number): string {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value.toFixed(2)} ${units[unit]}`
}

export default function NodeMetricsChart({ times, series, coverage, height = 280 }: Props) {
  const ref = useRef<HTMLDivElement | null>(null)
  const chartRef = useRef<echarts.ECharts | null>(null)
  const theme = useTheme()
  const md = theme.palette.md

  useEffect(() => {
    if (!ref.current) return
    chartRef.current = echarts.init(ref.current, null, { renderer: 'canvas' })
    const onResize = () => chartRef.current?.resize()
    window.addEventListener('resize', onResize)
    return () => {
      window.removeEventListener('resize', onResize)
      chartRef.current?.dispose()
      chartRef.current = null
    }
  }, [])

  // One unit per chart, because a chart mixing percentages and byte rates has an
  // axis that means nothing. The callers group their series accordingly.
  const unit = series[0]?.unit ?? 'count'

  useEffect(() => {
    if (!chartRef.current) return
    const formatter = (value: number | string) => formatValue(Number(value), unit)
    chartRef.current.setOption(
      {
        backgroundColor: 'transparent',
        grid: { left: 56, right: 24, top: 40, bottom: 40 },
        tooltip: {
          trigger: 'axis',
          valueFormatter: formatter,
          // The coverage is part of the reading: a point covering 90 seconds of a
          // 60-second bucket is an average over more than the bucket, and a point
          // covering 10 seconds is barely evidence at all.
          formatter: (params: unknown) => {
            const items = params as { seriesName: string; value: number | null; dataIndex: number }[]
            if (!items.length) return ''
            const index = items[0].dataIndex
            const lines = [`${times[index]}`]
            for (const item of items) {
              lines.push(`${item.seriesName}: ${item.value === null ? '—' : formatter(item.value)}`)
            }
            if (coverage && coverage[index] !== undefined) {
              lines.push(`coverage: ${coverage[index]}s`)
            }
            return lines.join('<br/>')
          },
        },
        legend: { data: series.map(s => s.name), textStyle: { color: md.onSurfaceVariant } },
        xAxis: {
          type: 'category',
          data: times,
          axisLabel: { color: md.onSurfaceVariant },
          axisLine: { lineStyle: { color: md.outlineVariant } },
        },
        yAxis: {
          type: 'value',
          axisLabel: { color: md.onSurfaceVariant },
          splitLine: { lineStyle: { color: md.outlineVariant } },
        },
        series: series.map(s => ({
          name: s.name,
          type: 'line',
          data: s.values,
          showSymbol: false,
          smooth: false,
          // A GAP MUST BREAK THE LINE. Interpolating across one would draw a
          // confident slope through an outage, which is the single most
          // misleading thing a monitoring chart can do.
          connectNulls: false,
          lineStyle: { width: 1.5 },
        })),
      },
      true,
    )
  }, [times, series, coverage, md, unit])

  return <div ref={ref} style={{ width: '100%', height }} role="img" />
}

export { formatBits, formatBytes, formatValue }
