import type { ChartTokens } from '@/hooks/useChartTokens'

// ONE BUILDER FOR EVERY ECHARTS OPTION, so the charts cannot drift apart. ECharts stays the engine; what a chart looks
// like is decided here and in common/ChartLegend: no built-in legend, 2px lines with no always-on markers (a point
// appears on hover only), muted axis labels and border-coloured split lines with no axis line, and a tooltip drawn as
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
    axisPointer: { type: 'line' as const, lineStyle: { color: tk.axisLine, type: 'solid' as const } },
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

/** A line series: 2px, no markers until hover, the pattern of its index. */
export function lineSeries(index: number, extra: { name: string; data: unknown[] } & Record<string, unknown>) {
  return {
    type: 'line' as const,
    showSymbol: false,
    symbol: 'circle',
    symbolSize: 6,
    smooth: false,
    lineStyle: { width: 2, type: dashOf(index) },
    emphasis: { focus: 'series' as const },
    ...extra,
  }
}

/** Base options shared by every chart. The ECharts legend is off; `hidden` series stay toggled off through its selection map. */
export function chartBase(tk: ChartTokens, names: readonly string[], hidden: ReadonlySet<string>) {
  return {
    backgroundColor: 'transparent',
    color: tk.series,
    textStyle: { color: tk.text, fontFamily: 'inherit' },
    legend: { show: false, data: [...names], selected: Object.fromEntries([...hidden].map(n => [n, false])) },
    ...chartAxes(tk),
  }
}
