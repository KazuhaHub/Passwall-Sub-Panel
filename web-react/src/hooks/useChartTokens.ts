import { useEffect, useState } from 'react'
import '@/styles/data-tokens.css'

/**
 * The colours an ECharts option needs, resolved to concrete values.
 *
 * ECharts draws on a canvas and cannot read `var(--x)`, and the app's colours
 * are written in oklch(), which zrender's own colour parser does not understand
 * (it uses the parser for hover lifts and gradients). So each variable is read
 * from <html> and converted to sRGB through a 1x1 canvas, which is what the
 * browser would paint anyway.
 *
 * HOW TrafficChart AND NodeMetricsChart WILL SWITCH (not done in this package):
 * - replace `useTheme().palette.md` with `const tk = useChartTokens()`;
 * - `md.onSurfaceVariant` (axis labels, legend text) -> `tk.mutedText`;
 * - `md.outlineVariant` (axis line, split lines) -> `tk.axisLine` / `tk.grid`;
 * - `md.primary` / `md.tertiary` (series) -> `tk.series[0]` / `tk.series[1]`, or
 *   spread `chartBaseOption(tk)` first so tooltip, text and palette come free;
 * - add `tk` to the dependency array of the effect that calls setOption, so a
 *   mode or brand change repaints the chart without remounting it.
 */
export interface ChartTokens {
  /** Categorical series colours, 8 of them; the first follows the brand. */
  series: string[]
  text: string
  mutedText: string
  axisLine: string
  grid: string
  tooltipBg: string
  tooltipBorder: string
  tooltipText: string
  brand: string
  success: string
  warning: string
  danger: string
}

const SERIES_VARS = Array.from({ length: 8 }, (_, i) => `--series-${i + 1}`)

// Used only when a variable is missing (a test, or a page before the stylesheet
// loaded), so a chart is never painted with an empty colour string.
const FALLBACK = {
  text: '#171717', mutedText: '#737373', axisLine: '#d4d4d4', grid: '#e5e5e5',
  tooltipBg: '#ffffff', tooltipBorder: '#e5e5e5', tooltipText: '#171717',
  brand: '#3b5bdb', success: '#2f9e44', warning: '#f08c00', danger: '#e03131',
  series: ['#3b5bdb', '#1098ad', '#f59f00', '#9c36b5', '#e64980', '#37b24d', '#1c7ed6', '#868e96'],
}

let scratch: CanvasRenderingContext2D | null | undefined

function toSrgb(css: string): string {
  if (scratch === undefined) {
    try {
      const canvas = document.createElement('canvas')
      canvas.width = canvas.height = 1
      scratch = canvas.getContext('2d', { willReadFrequently: true })
    } catch { scratch = null }
  }
  if (!scratch) return css
  try {
    scratch.clearRect(0, 0, 1, 1)
    scratch.fillStyle = '#000'
    scratch.fillStyle = css
    scratch.fillRect(0, 0, 1, 1)
    const [r, g, b, a] = scratch.getImageData(0, 0, 1, 1).data
    return a === 255 ? `rgb(${r}, ${g}, ${b})` : `rgba(${r}, ${g}, ${b}, ${(a / 255).toFixed(3)})`
  } catch { return css }
}

export function readChartTokens(root: HTMLElement = document.documentElement): ChartTokens {
  const style = getComputedStyle(root)
  const read = (name: string, fallback: string) => {
    const raw = style.getPropertyValue(name).trim()
    return raw ? toSrgb(raw) : fallback
  }
  return {
    series: SERIES_VARS.map((v, i) => read(v, FALLBACK.series[i])),
    text: read('--foreground', FALLBACK.text),
    mutedText: read('--muted-foreground', FALLBACK.mutedText),
    axisLine: read('--border', FALLBACK.axisLine),
    grid: read('--border', FALLBACK.grid),
    tooltipBg: read('--popover', FALLBACK.tooltipBg),
    tooltipBorder: read('--border', FALLBACK.tooltipBorder),
    tooltipText: read('--popover-foreground', FALLBACK.tooltipText),
    brand: read('--brand', FALLBACK.brand),
    success: read('--success', FALLBACK.success),
    warning: read('--warning', FALLBACK.warning),
    danger: read('--destructive', FALLBACK.danger),
  }
}

/**
 * Colour tokens for a chart, re-read whenever the appearance changes. The
 * appearance store mirrors mode (`class`), brand and density (`data-*`) and a
 * custom brand colour (an inline `style`) onto <html>, so watching those four
 * attributes is enough and needs no knowledge of the store.
 */
export function useChartTokens(): ChartTokens {
  const [tokens, setTokens] = useState<ChartTokens>(() => readChartTokens())
  useEffect(() => {
    const observer = new MutationObserver(() => setTokens(readChartTokens()))
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['class', 'style', 'data-brand', 'data-density'],
    })
    // The stylesheet or the appearance sync may have landed between the first render and now.
    setTokens(prev => {
      const next = readChartTokens()
      return JSON.stringify(prev) === JSON.stringify(next) ? prev : next
    })
    return () => observer.disconnect()
  }, [])
  return tokens
}

/** The option fragments nearly every chart repeats: palette, text, axes, tooltip. */
export function chartBaseOption(tk: ChartTokens) {
  const axis = {
    axisLabel: { color: tk.mutedText },
    axisLine: { lineStyle: { color: tk.axisLine } },
    splitLine: { lineStyle: { color: tk.grid, opacity: 0.6 } },
  }
  return {
    backgroundColor: 'transparent',
    color: tk.series,
    textStyle: { color: tk.text },
    legend: { textStyle: { color: tk.mutedText } },
    tooltip: {
      backgroundColor: tk.tooltipBg,
      borderColor: tk.tooltipBorder,
      textStyle: { color: tk.tooltipText },
    },
    xAxis: axis,
    yAxis: axis,
  }
}
