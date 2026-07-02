import i18n from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'

import type { AppLanguage } from '@/theme'
import { fetchLanguages, fetchLanguageBundle, type LocaleMeta } from '@/api/i18n'

// Language-pack schema version, mirrors service/locale.Format on the backend.
// The exported "base template" stamps this into `psp_language_pack`.
export const LANGUAGE_PACK_FORMAT = 1

// The two built-in languages are compiled into the bundle. Runtime-uploaded
// packs are appended to SUPPORTED_LANGUAGES at boot (see loadManifest), so this
// is a MUTABLE array — call sites read it by reference. Keep the built-ins first.
export const SUPPORTED_LANGUAGES: AppLanguage[] = ['zh-CN', 'en-US']

// The codes that live in the JS bundle (via the glob below). Everything else is
// a server-supplied pack fetched over HTTP. Kept in sync with the backend's
// reserved-code list.
const BUILTIN_LANGUAGES = new Set<AppLanguage>(['zh-CN', 'en-US'])

export function isBuiltinLanguage(lang: string): boolean {
  return BUILTIN_LANGUAGES.has(lang)
}

// serverLanguages holds the manifest of uploaded packs (populated at boot).
// LanguageMenu reads a pack's self-declared endonym from here for its label.
export const serverLanguages: LocaleMeta[] = []

export function serverLanguageMeta(code: string): LocaleMeta | undefined {
  return serverLanguages.find(m => m.code === code)
}

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

// Pre-load every namespace for a BUILT-IN language, in parallel (from the Vite
// glob chunks). Other languages stream in on demand when the user toggles the
// language picker.
async function loadLanguageResources(lang: AppLanguage): Promise<Record<string, Record<string, string>>> {
  const entries = await Promise.all(
    NAMESPACES.map(async ns => {
      const flat = await loadNamespace(lang, ns)
      return [ns, flat ?? {}] as const
    }),
  )
  return Object.fromEntries(entries)
}

// Fetch + flatten a SERVER-supplied pack. Missing namespaces resolve to {} — the
// i18next fallback chain fills the gaps, so a partial pack is safe.
async function loadServerLanguageResources(lang: AppLanguage): Promise<Record<string, Record<string, string>>> {
  const { namespaces } = await fetchLanguageBundle(lang)
  const out: Record<string, Record<string, string>> = {}
  for (const ns of NAMESPACES) {
    const tree = namespaces[ns]
    out[ns] = tree ? flatten(tree as Nested) : {}
  }
  return out
}

// Dispatch by origin: built-ins from the glob, uploaded packs over HTTP.
function loadResourcesFor(lang: AppLanguage): Promise<Record<string, Record<string, string>>> {
  return isBuiltinLanguage(lang) ? loadLanguageResources(lang) : loadServerLanguageResources(lang)
}

// loadBuiltinSource returns the RAW (nested, un-flattened) source JSON for a
// built-in language. The admin "export base template" affordance uses it so
// translators start from the exact shipped strings in their original nesting.
export async function loadBuiltinSource(lang: AppLanguage): Promise<Record<string, Record<string, unknown>>> {
  const out: Record<string, Record<string, unknown>> = {}
  await Promise.all(NAMESPACES.map(async ns => {
    const key = `/src/locales/${lang}/${ns}.json`
    const loader = localeLoaders[key]
    out[ns] = loader ? ((await loader()).default as Record<string, unknown>) : {}
  }))
  return out
}

// loadManifest fetches the uploaded-pack list and folds it into SUPPORTED_LANGUAGES
// BEFORE i18n.init — i18next rejects changeLanguage to codes outside supportedLngs,
// so the whitelist must already contain server codes. Never throws: a manifest
// failure just leaves the two built-ins available.
async function loadManifest(): Promise<void> {
  try {
    const langs = await fetchLanguages()
    serverLanguages.length = 0
    for (const m of langs) {
      serverLanguages.push(m)
      if (!SUPPORTED_LANGUAGES.includes(m.code)) SUPPORTED_LANGUAGES.push(m.code)
    }
  } catch (err) {
    // eslint-disable-next-line no-console
    console.warn('i18n: failed to load language manifest; only built-in languages available', err)
  }
}

export const i18nReady = (async () => {
  // Order matters: manifest first (extends supportedLngs + lets
  // resolveInitialLanguage accept a persisted custom code), THEN resolve, THEN
  // load resources, THEN init. This whole IIFE must never reject — main.tsx
  // awaits it before mounting React, so a throw here would hang the app on a
  // blank screen. Every await below is guarded.
  await loadManifest()
  let lng = resolveInitialLanguage()
  const resources: Record<string, Record<string, Record<string, string>>> = {}
  try {
    resources[lng] = await loadResourcesFor(lng)
  } catch (err) {
    // A stale/removed custom pack (or a failed fetch) must never block mount —
    // fall back to the always-present built-in default.
    // eslint-disable-next-line no-console
    console.warn('i18n: failed to load initial language, falling back to zh-CN', lng, err)
    lng = 'zh-CN'
    resources[lng] = await loadLanguageResources('zh-CN')
  }
  // Register the zh-CN fallback bundle too when it isn't the active language:
  // load:'currentOnly' means i18next won't lazy-load the fallback, so a partial
  // custom pack would otherwise surface raw keys instead of Chinese.
  if (!resources['zh-CN']) {
    try {
      resources['zh-CN'] = await loadLanguageResources('zh-CN')
    } catch { /* built-in should always be present; ignore */ }
  }
  await i18n
    .use(LanguageDetector)
    .use(initReactI18next)
    .init({
      resources,
      lng,
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
      // No preload — we explicitly loaded the initial language (+ fallback)
      // above and stream the rest lazily via setLanguage() below.
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
  // cache (hasResourceBundle) so they're free. Built-ins come from the Vite
  // glob; uploaded packs are fetched over HTTP (loadResourcesFor dispatches).
  //
  // Loading CAN reject (stale build chunks after a deploy + an open tab clicks
  // language switch = 404 on a missing chunk; or a removed pack / offline
  // backend for a custom code). Callers (UserLayout / AdminLayout / LoginView*)
  // fire-and-forget; without this try/catch the rejection surfaces as an
  // unhandled promise + the UI silently stays on the old language. Log it and
  // leave changeLanguage uncalled so i18next's resolvedLanguage stays consistent
  // with what t() actually returns.
  if (!i18n.hasResourceBundle(lang, 'common')) {
    try {
      const resources = await loadResourcesFor(lang)
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
