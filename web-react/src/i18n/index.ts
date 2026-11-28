import i18n from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'

import { fetchLanguages, fetchLanguageBundle, type LocaleMeta } from '@/api/i18n'
import { NAMESPACES, I18N_STATIC_OPTIONS, type Nested } from './options'
import { BASE_LANGUAGE, createResources, type LocaleLoaders } from './resources'

// Language-pack schema version, mirrors service/locale.Format on the backend.
// The exported "base template" stamps this into `psp_language_pack`.
export const LANGUAGE_PACK_FORMAT = 1

// The built-in languages are 'zh-CN' | 'zh-TW' | 'en-US', but runtime-uploaded language
// packs can register any code, so this is a plain string.
export type AppLanguage = string

// The built-in languages are compiled into the bundle. Runtime-uploaded
// packs are appended to SUPPORTED_LANGUAGES at boot (see loadManifest), so this
// is a MUTABLE array — call sites read it by reference. Keep the built-ins first.
export const SUPPORTED_LANGUAGES: AppLanguage[] = ['zh-CN', 'zh-TW', 'en-US']

// The codes that live in the JS bundle (via the glob below). Everything else is
// a server-supplied pack fetched over HTTP. Kept in sync with the backend's
// reserved-code list.
const BUILTIN_LANGUAGES = new Set<AppLanguage>(['zh-CN', 'zh-TW', 'en-US'])

export function isBuiltinLanguage(lang: string): boolean {
  return BUILTIN_LANGUAGES.has(lang)
}

// serverLanguages holds the manifest of uploaded packs (populated at boot).
// LanguageMenu reads a pack's self-declared endonym from here for its label.
export const serverLanguages: LocaleMeta[] = []

export function serverLanguageMeta(code: string): LocaleMeta | undefined {
  return serverLanguages.find(m => m.code === code)
}

// NAMESPACES / flatten / the static init options live in ./options and the namespace
// loading logic in ./resources (both side-effect-free) so tests can import the real thing
// without triggering this module's bootstrap IIFE. The glob below code-splits the locale
// JSON per (lang, ns); which namespaces load when is decided in ./resources.

// Vite's import.meta.glob gives us per-(lang, ns) lazy chunks. The
// `import()`-style returns a Promise<{ default: Nested }> — wrapped in
// flatten() at register time so the runtime cost is paid once per
// namespace-language, not per t() call.
const localeLoaders: LocaleLoaders = import.meta.glob<{ default: Nested }>('@/locales/*/*.json')

// loadManifest fetches the uploaded-pack list and folds it into SUPPORTED_LANGUAGES.
// i18next rejects changeLanguage to codes outside supportedLngs, and `supportedLngs` below
// is this same array, so a code appended after init is accepted too. Never throws: a
// manifest failure just leaves the three built-ins available.
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

// Started at module load so it runs in parallel with the locale chunk imports rather than
// ahead of them (it used to be awaited first: one round trip, THEN the chunks).
const manifestReady = loadManifest()

const resources = createResources({
  loaders: localeLoaders,
  fetchPack: fetchLanguageBundle,
  isBuiltin: isBuiltinLanguage,
  manifestReady: () => manifestReady,
  instance: i18n,
})

// loadBuiltinSource returns the RAW (nested, un-flattened) source JSON for a
// built-in language. The admin "export base template" affordance uses it so
// translators start from the exact shipped strings in their original nesting.
export async function loadBuiltinSource(lang: AppLanguage): Promise<Record<string, Record<string, unknown>>> {
  const out: Record<string, Record<string, unknown>> = {}
  await Promise.all(NAMESPACES.map(async ns => {
    const loader = localeLoaders[`/src/locales/${lang}/${ns}.json`]
    out[ns] = loader ? ((await loader()).default as Record<string, unknown>) : {}
  }))
  return out
}

// The language hints the browser holds (querystring, then localStorage), in i18next's
// detection order.
function languageHints(): string[] {
  if (typeof window === 'undefined') return []
  const hints: string[] = []
  const q = new URL(window.location.href).searchParams.get('lang')
  if (q) hints.push(q)
  try {
    const stored = localStorage.getItem('psp-lang')
    if (stored) hints.push(stored)
  } catch { /* localStorage disabled — fall through */ }
  return hints
}

// resolveInitialLanguage picks the language to bundle into the first paint. Mirrors
// i18next's detection order (querystring -> localStorage -> navigator) but runs
// synchronously so only that one language is preloaded.
function resolveInitialLanguage(): AppLanguage {
  if (typeof window !== 'undefined') {
    for (const hint of languageHints()) if (SUPPORTED_LANGUAGES.includes(hint)) return hint
    const nav = (window.navigator?.language || '').toLowerCase()
    if (/^zh-(tw|hk|mo)|^zh-hant/.test(nav)) return 'zh-TW'
    if (nav.startsWith('zh')) return 'zh-CN'
    if (nav.startsWith('en')) return 'en-US'
  }
  return 'zh-CN'
}

export const i18nReady = (async () => {
  // This whole IIFE must never reject — main.tsx awaits it before mounting React, so a
  // throw here would hang the app on a blank screen. Every await below is guarded.
  //
  // The manifest is only waited for when a stored/queried language is NOT a built-in: a
  // persisted custom code is accepted only once the manifest has put it in
  // SUPPORTED_LANGUAGES. A built-in language needs nothing from it, so the common case
  // mounts without waiting for that request at all.
  if (languageHints().some(h => !isBuiltinLanguage(h))) await manifestReady
  let lng = resolveInitialLanguage()
  let initial: Awaited<ReturnType<typeof resources.initial>>
  try {
    initial = await resources.initial(lng)
  } catch (err) {
    // A stale/removed custom pack (or a failed fetch) must never block mount —
    // fall back to the always-present built-in default.
    // eslint-disable-next-line no-console
    console.warn('i18n: failed to load initial language, falling back to zh-CN', lng, err)
    lng = BASE_LANGUAGE
    initial = await resources.initial(lng)
  }
  await i18n
    .use(LanguageDetector)
    .use(initReactI18next)
    .init({
      // Shared resolution/fallback config (fallbackLng → zh-CN, keySeparator,
      // fallbackNS, …) lives in ./options so a unit test exercises the same
      // config. Only the per-instance dynamic bits + plugin config are here.
      ...I18N_STATIC_OPTIONS,
      resources: initial,
      lng,
      supportedLngs: SUPPORTED_LANGUAGES,
      detection: {
        order: ['querystring', 'localStorage', 'navigator'],
        lookupLocalStorage: 'psp-lang',
        lookupQuerystring: 'lang',
        caches: ['localStorage'],
      },
      react: {
        useSuspense: false,
      },
    })
  return i18n
})()

// Make the namespaces a shell reads available before it renders (the shell route loaders
// call this). See ./resources.
export const ensureNamespaces = resources.ensureNamespaces

// Switch language. Loading CAN reject (stale build chunks after a deploy + an open tab
// clicks language switch = 404 on a missing chunk; or a removed pack / offline backend for
// a custom code). Callers fire-and-forget, so ./resources logs and leaves changeLanguage
// uncalled: i18next's resolvedLanguage stays consistent with what t() actually returns.
export const setLanguage = resources.setLanguage

export function currentLanguage(): AppLanguage {
  const lng = i18n.resolvedLanguage
  return SUPPORTED_LANGUAGES.includes(lng as AppLanguage) ? (lng as AppLanguage) : 'zh-CN'
}

export default i18n
