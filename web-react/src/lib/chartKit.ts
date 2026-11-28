import type { ChartTokens } from '@/hooks/useChartTokens'

// ONE BUILDER FOR EVERY ECHARTS OPTION, so the charts cannot drift apart. ECharts stays the engine; what a chart looks
// like is decided here and in common/ChartLegend: no built-in legend, 2px lines under a soft gradient fill, markers
// only where there are few points (otherwise a point appears on hover), muted axis labels and border-coloured split lines with no axis line, and a tooltip drawn as
// the app's popover.

/** Line patterns, one per series, so series differ by more than colour. The legend swatch draws the same pattern. */
export const DASHES = ['solid', 'dashed', 'dotted'] as const
export type Dash = (typeof DASHES)[number]
export const dashOf = (index: number): Dash => DASHES[index % DASHES.length]

const escapeHtml = (text: string) => text.replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!)

/** A short line sample in the series colour and pattern: the swatch of the legend and of a tooltip row. */
export function swatchHtml(color: string, dash: Dash): string {
  return `<span style="display:inline-block;width:14px;height:0;border-top:2px ${dash} ${color};flex:none"></span>`
}

export interface TooltipRow { name: string; value: string; color: string; dash: Dash }

/** The tooltip body in the popover's style: a muted heading, then one row per series with a swatch and a right-aligned figure. Every string is escaped. */
export function tooltipHtml(heading: string, rows: readonly TooltipRow[], tk: ChartTokens): string {
  const body = rows.map(r => `<div style="display:flex;align-items:center;gap:8px;line-height:18px">${swatchHtml(r.color, r.dash)}<span style="color:${tk.mutedText};flex:1;min-width:0">${escapeHtml(r.name)}</span><span style="font-variant-numeric:tabular-nums;font-weight:500;margin-left:16px;text-align:right">${escapeHtml(r.value)}</span></div>`).join('')
  return `<div style="font-size:12px;min-width:140px"><div style="color:${tk.mutedText};margin-bottom:4px">${escapeHtml(heading)}</div>${body}</div>`
}

/** The tooltip chrome: popover colours and border, small text, no ECharts default padding or shadow. */
export function tooltipFrame(tk: ChartTokens) {
  return {
    trigger: 'axis' as const,
    confine: true,
    backgroundColor: tk.tooltipBg,
    borderColor: tk.tooltipBorder,
    borderWidth: 1,
    padding: [8, 10],
    textStyle: { color: tk.tooltipText, fontSize: 12 },
    extraCssText: 'border-radius:8px;box-shadow:0 4px 12px -2px rgb(0 0 0 / 0.18);',
    axisPointer: { type: 'line' as const, lineStyle: { color: tk.axisLine, type: 'dashed' as const, width: 1 } },
  }
}

/** Axis defaults: muted labels, border-coloured split lines on the value axis only, no axis line or ticks. */
export function chartAxes(tk: ChartTokens) {
  const label = { color: tk.mutedText, fontSize: 11 }
  return {
    xAxis: { axisLabel: label, axisLine: { show: false }, axisTick: { show: false }, splitLine: { show: false } },
    yAxis: { axisLabel: label, axisLine: { show: false }, axisTick: { show: false }, splitLine: { lineStyle: { color: tk.grid, opacity: 0.7 } } },
  }
}

/** Up to this many points a marker on every point is readable (a 7-day trend); beyond it, markers only on hover. */
export const MAX_MARKED_POINTS = 31

/** The colour with a new alpha. Token colours arrive as rgb()/rgba() (useChartTokens resolves them); hex is handled for the fallbacks. */
export function withAlpha(color: string, alpha: number): string {
  const rgb = /^rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/.exec(color)
  if (rgb) return `rgba(${rgb[1]}, ${rgb[2]}, ${rgb[3]}, ${alpha})`
  const hex = /^#([0-9a-f]{6})$/i.exec(color)
  if (hex) {
    const n = parseInt(hex[1], 16)
    return `rgba(${n >> 16}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`
  }
  return color
}

/** A vertical fade from the series colour at the top to nothing at the baseline, built from the resolved token so it follows the theme. */
export function areaGradient(color: string, alpha: number) {
  return {
    type: 'linear' as const, x: 0, y: 0, x2: 0, y2: 1,
    colorStops: [{ offset: 0, color: withAlpha(color, alpha) }, { offset: 1, color: withAlpha(color, 0) }],
  }
}

/** True when the user asked for less motion; charts then draw without animation. */
export function prefersReducedMotion(): boolean {
  try { return typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches }
  catch { return false }
}

/**
 * A line series: 2px, the pattern of its index, a gradient fill under it, and a ringed marker.
 *
 * Fill: `area: false` leaves an aggregate (a total that sits over its parts) as a bare line so it does not muddy the
 * series beneath it; with several filled series the fill is fainter so the overlaps stay readable.
 * Markers: always on for few points, hover-only otherwise; the ring is in the surface colour so a marker stays
 * legible on top of the line and the fill.
 * Smoothing is slight and monotone in x: the curve never overshoots between two points, so it cannot dip below zero
 * or past a maximum that the data does not contain, and it stays honest about the values.
 */
export function lineSeries(index: number, tk: ChartTokens, extra: { name: string; data: unknown[] } & Record<string, unknown>, opts: { area?: boolean; fillAlpha?: number } = {}) {
  const color = tk.series[index % tk.series.length]
  const { area = true, fillAlpha = 0.22 } = opts
  return {
    type: 'line' as const,
    showSymbol: extra.data.length <= MAX_MARKED_POINTS,
    symbol: 'circle',
    symbolSize: 6,
    smooth: 0.2,
    smoothMonotone: 'x' as const,
    lineStyle: { width: 2, type: dashOf(index) },
    itemStyle: { color, borderColor: tk.tooltipBg, borderWidth: 2 },
    ...(area ? { areaStyle: { color: areaGradient(color, fillAlpha), opacity: 1 } } : {}),
    emphasis: { focus: 'series' as const, scale: 1.4, itemStyle: { color, borderColor: tk.tooltipBg, borderWidth: 2 } },
    ...extra,
  }
}

/** Base options shared by every chart. The ECharts legend is off; `hidden` series stay toggled off through its selection map. */
export function chartBase(tk: ChartTokens, names: readonly string[], hidden: ReadonlySet<string>) {
  return {
    backgroundColor: 'transparent',
    animation: !prefersReducedMotion(),
    color: tk.series,
    textStyle: { color: tk.text, fontFamily: 'inherit' },
    legend: { show: false, data: [...names], selected: Object.fromEntries([...hidden].map(n => [n, false])) },
    ...chartAxes(tk),
  }
}
