import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import {
  formatBytes,
  formatCount,
  formatDuration,
  formatExact,
  formatLatency,
  formatPct,
  formatRate,
  type QuantileReading,
} from '@/utils/diagnostics'
import { labelFor, type Translate } from '@/utils/diagnosticsCatalog'

/**
 * Every number on the page goes through here, in the reader's language: one
 * place decides how a count, a share, a span or a latency is written, so the
 * cards, the findings and the raw area can never write one value two ways.
 */
export interface DiagFormat {
  t: Translate
  lang: string
  count: (n: number) => string
  exact: (n: number) => string
  pct: (n: number, d: number) => string
  duration: (ms: number) => string
  latency: (ms: number) => string
  rate: (r: number) => string
  /** A histogram value in its own unit: ms as latency, bytes in 1024 steps. */
  unit: (value: number, unit: string) => string
  /** Whether a key exists in the bundles, so optional copy is never shown
   *  as its own key path. */
  has: (key: string) => boolean
  /** A label value's translated name, or the value itself. */
  label: (group: string, value: string) => string
  /** "typical X" and "95% within Y", or what the samples allow instead. */
  quantile: (r: QuantileReading, unit: string) => { value: string; caption?: string }
}

export function useDiagFormat(): DiagFormat {
  const { t, i18n } = useTranslation(['admin', 'nav'])
  const lang = i18n.language
  return useMemo(() => {
    const tt: Translate = (key, options) => t(key, options as never) as unknown as string
    const exists = (key: string) => i18n.exists(key)
    const count = (n: number) => formatCount(n, lang)
    const rate = (r: number) => formatRate(r, lang)
    const unit = (value: number, u: string) => {
      if (u === 'ms') return formatLatency(value)
      if (u === 'bytes') return formatBytes(value)
      if (u === 'km') return `${rate(value)} km`
      return rate(value)
    }
    return {
      t: tt,
      lang,
      count,
      exact: (n: number) => formatExact(n, lang),
      pct: (n: number, d: number) => formatPct(n, d, tt),
      duration: (ms: number) => formatDuration(ms, tt),
      latency: formatLatency,
      rate,
      unit,
      has: exists,
      label: (group: string, value: string) => labelFor(tt, exists, group, value),
      quantile: (r: QuantileReading, u: string) => {
        if (r.kind === 'none') return { value: tt('admin:diagnostics.fmt.no_samples') }
        if (r.kind === 'few') {
          return { value: tt('admin:diagnostics.fmt.few_samples', { count: count(r.count), max: unit(r.max, u) }) }
        }
        // Past the last finite bucket the estimate is pulled toward the max,
        // so the number is withheld and only the ceiling is stated.
        const caption = r.over
          ? tt('admin:diagnostics.fmt.p95_over', { ceiling: unit(r.ceiling, u) })
          : tt('admin:diagnostics.fmt.p95_within', { value: unit(r.p95, u) })
        return { value: tt('admin:diagnostics.fmt.typical', { value: unit(r.p50, u) }), caption }
      },
    }
  }, [t, i18n, lang])
}
