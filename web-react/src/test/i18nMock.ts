import { flatten, NAMESPACES, type Nested } from '@/i18n/options'

// A stand-in for react-i18next that answers from the REAL zh-CN bundles, so a
// test reads as the copy a user sees and a key a component asks for but the
// bundle lacks shows up as its raw key. Used from a vi.mock factory:
//
//   vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())
const files = import.meta.glob<{ default: Nested }>('../locales/zh-CN/*.json', { eager: true })

const bundles: Record<string, Record<string, string>> = {}
for (const ns of NAMESPACES) {
  const mod = files[`../locales/zh-CN/${ns}.json`]
  if (mod) bundles[ns] = flatten(mod.default)
}

export function translate(defaultNs: string | string[] | undefined, key: string, opts?: Record<string, unknown>): string {
  const first = Array.isArray(defaultNs) ? defaultNs[0] : defaultNs
  let ns = first ?? 'common'
  let k = key
  const colon = key.indexOf(':')
  if (colon > 0 && NAMESPACES.includes(key.slice(0, colon) as never)) {
    ns = key.slice(0, colon)
    k = key.slice(colon + 1)
  }
  const dict = bundles[ns] ?? {}
  const plural = typeof opts?.count === 'number' ? dict[`${k}_other`] : undefined
  const raw = plural ?? dict[k] ?? (typeof opts?.defaultValue === 'string' ? opts.defaultValue : key)
  // The spacing helper (src/i18n/spacing.ts) passes an `escape` that brackets each value; honour it
  // so the sentinels reach resolveBoundaries as they do with real i18next.
  const escape = (opts?.interpolation as { escape?: (v: string) => string } | undefined)?.escape
  return raw.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (opts && name in opts ? (escape ? escape(String(opts[name])) : String(opts[name])) : m))
}

export function reactI18nextMock() {
  return {
    useTranslation: (ns?: string | string[]) => ({
      t: (key: string, opts?: Record<string, unknown>) => translate(ns, key, opts),
      i18n: { language: 'zh-CN', resolvedLanguage: 'zh-CN', changeLanguage: () => Promise.resolve() },
    }),
    Trans: ({ children }: { children?: unknown }) => children ?? null,
  }
}
