import i18n from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'

import type { AppLanguage } from '@/theme'

export const SUPPORTED_LANGUAGES: AppLanguage[] = ['zh-CN', 'en-US']

// Locale namespaces. Each (lang, ns) bundle is loaded via dynamic import
// — Vite splits them into per-namespace chunks so a user only downloads
// the languages and namespaces they actually need. Pre-fix this module
// statically imported all 7 namespaces × 2 languages (14 JSON modules,
// ~120KB of which admin.json was 57KB per language) into the main
// bundle; admins on en-US pulled the zh-CN strings for free, and users
// who never visit admin pages still shipped the admin namespace.
const NAMESPACES = ['common', 'appearance', 'language', 'auth', 'nav', 'admin', 'user'] as const

// Flatten { a: { b: { c: 'x' } } } → { 'a.b.c': 'x' }.
// We do this at init time + run i18n with keySeparator:false so a call like
// t('admin:servers.title') treats 'servers.title' as one flat key. Workaround
// for an i18next quirk where instance-level keySeparator doesn't actually get
// used during nested-key resolution.
type Nested = { [k: string]: string | Nested }
function flatten(obj: Nested, prefix = ''): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (typeof v === 'string') out[key] = v
    else if (v && typeof v === 'object') Object.assign(out, flatten(v, key))
  }
  return out
}

// Vite's import.meta.glob gives us per-(lang, ns) lazy chunks. The
// `import()`-style returns a Promise<{ default: Nested }> — wrapped in
// flatten() at register time so the runtime cost is paid once per
// namespace-language, not per t() call.
const localeLoaders = import.meta.glob<{ default: Nested }>('@/locales/*/*.json')

async function loadNamespace(lang: AppLanguage, ns: string): Promise<Record<string, string> | null> {
  const key = `/src/locales/${lang}/${ns}.json`
  const loader = localeLoaders[key]
  if (!loader) return null
  const mod = await loader()
  return flatten(mod.default)
}

// resolveInitialLanguage picks the language to bundle into the first paint.
// Mirrors i18next's detection order (querystring → localStorage → navigator)
// but runs synchronously here so we can preload only that one language's
// namespaces. i18next then takes over for runtime detection/switching.
function resolveInitialLanguage(): AppLanguage {
  if (typeof window !== 'undefined') {
    const url = new URL(window.location.href)
    const q = url.searchParams.get('lang')
    if (q && SUPPORTED_LANGUAGES.includes(q as AppLanguage)) return q as AppLanguage
    try {
      const stored = localStorage.getItem('psp-lang')
      if (stored && SUPPORTED_LANGUAGES.includes(stored as AppLanguage)) return stored as AppLanguage
    } catch { /* localStorage disabled — fall through */ }
    const nav = (window.navigator?.language || '').toLowerCase()
    if (nav.startsWith('zh')) return 'zh-CN'
    if (nav.startsWith('en')) return 'en-US'
  }
  return 'zh-CN'
}

const initialLang = resolveInitialLanguage()

// Pre-load every namespace for the initial language, in parallel. Other
// languages stream in on demand when the user toggles the language picker.
async function loadLanguageResources(lang: AppLanguage): Promise<Record<string, Record<string, string>>> {
  const entries = await Promise.all(
    NAMESPACES.map(async ns => {
      const flat = await loadNamespace(lang, ns)
      return [ns, flat ?? {}] as const
    }),
  )
  return Object.fromEntries(entries)
}

export const i18nReady = (async () => {
  const initialResources = await loadLanguageResources(initialLang)
  await i18n
    .use(LanguageDetector)
    .use(initReactI18next)
    .init({
      resources: { [initialLang]: initialResources },
      lng: initialLang,
      // Map generic browser language tags (en/zh) onto the exact bundles we ship.
      // Keep zh-CN as the final fallback so missing translations never surface
      // raw keys in normal use.
      fallbackLng: {
        en: ['en-US', 'zh-CN'],
        zh: ['zh-CN'],
        default: ['zh-CN'],
      },
      supportedLngs: SUPPORTED_LANGUAGES,
      load: 'currentOnly',
      // No preload — we explicitly loaded the initial language above and
      // stream the rest lazily via setLanguage() below.
      ns: NAMESPACES as unknown as string[],
      defaultNS: 'common',
      fallbackNS: 'common',
      // Resources are pre-flattened to dotted keys, so the runtime no longer
      // needs to walk a nested object — keySeparator:false makes t() treat the
      // whole 'servers.title' string as one flat lookup.
      keySeparator: false,
      nsSeparator: ':',
      interpolation: { escapeValue: false },
      detection: {
        order: ['querystring', 'localStorage', 'navigator'],
        lookupQuerystring: 'lang',
        lookupLocalStorage: 'psp-lang',
        caches: ['localStorage'],
      },
      react: {
        useSuspense: false,
      },
    })
  return i18n
})()

export async function setLanguage(lang: AppLanguage): Promise<void> {
  // Lazy-load on first switch — subsequent toggles hit i18next's in-memory
  // cache (hasResourceBundle) so they're free.
  //
  // The Promise.all over per-namespace dynamic imports CAN reject (stale
  // build chunks after a deploy + an open tab clicks language switch =
  // 404 on a missing chunk). Callers (UserLayout / AdminLayout /
  // LoginView*) fire-and-forget; without this try/catch the rejection
  // surfaces as an unhandled promise + the UI silently stays on the
  // old language. Log it and leave changeLanguage uncalled so i18next's
  // resolvedLanguage stays consistent with what t() actually returns.
  if (!i18n.hasResourceBundle(lang, 'common')) {
    try {
      const resources = await loadLanguageResources(lang)
      for (const [ns, bundle] of Object.entries(resources)) {
        i18n.addResourceBundle(lang, ns, bundle, true, true)
      }
    } catch (err) {
      // eslint-disable-next-line no-console
      console.warn('setLanguage: failed to load resources for', lang, err)
      return
    }
  }
  await i18n.changeLanguage(lang)
}

export function currentLanguage(): AppLanguage {
  const lng = i18n.resolvedLanguage
  return SUPPORTED_LANGUAGES.includes(lng as AppLanguage) ? (lng as AppLanguage) : 'zh-CN'
}

export default i18n
