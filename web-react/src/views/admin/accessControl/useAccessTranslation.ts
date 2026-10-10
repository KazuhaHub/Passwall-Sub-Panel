import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import type { TOptions } from 'i18next'

/** Format displayed measures without changing numeric plural-selection inputs. */
export function useAccessTranslation(namespaces: Parameters<typeof useTranslation>[0]) {
  const translation = useTranslation(namespaces)
  const { t, i18n } = translation
  const language = i18n?.language ?? 'en-US'
  const formatted = useMemo(() => {
    const counts = new Intl.NumberFormat(language, { maximumFractionDigits: 20 })
    const dates = new Intl.DateTimeFormat(language, { dateStyle: 'medium', timeStyle: 'medium' })
    const times = new Intl.DateTimeFormat(language, { timeStyle: 'medium' })
    const localizedT = ((key: string | string[], options?: TOptions | string, extra?: TOptions) => {
      const actual = typeof options === 'string' ? { ...extra, defaultValue: options } : options
      const variables = typeof actual?.replace === 'object' && actual.replace ? actual.replace : actual
      const replace = Object.fromEntries(Object.entries(variables ?? {}).map(([name, value]) =>
        [name, typeof value === 'number' && name !== 'id' ? counts.format(value) : value]))
      // `count` stays numeric in options for plural rules; only interpolation
      // receives its display string. Account identifiers retain their raw form.
      return t(key, { ...actual, replace })
    }) as unknown as typeof t
    return { t: localizedT, number: (value: number) => counts.format(value),
      dateTime: (value: number) => dates.format(value), time: (value: number) => times.format(value) }
  }, [t, language])
  return { ...translation, ...formatted }
}
