import type { i18n as I18n } from 'i18next'

import { NAMESPACES, flatten, type Nested } from './options'

// WHICH NAMESPACES A PAGE NEEDS, AND LOADING THEM WHEN IT DOES.
//
// The `admin` namespace is ~390 KB raw (~130 KB gzip) per language and `user` is small but
// only the portal reads it. Loading all seven for every visitor made the login page, which
// reads four, pay for the whole admin console's wording before React mounted. So the set is
// split: the BASE namespaces load at boot for everyone; `admin` and `user` load when their
// shell route is entered (router/shellLoaders.ts) and are then kept up to date by every
// later language switch.
//
// This module is side-effect free and takes its collaborators as arguments, so a test can
// drive it with fake loaders and a standalone i18next instance. ./index.ts wires the real ones.

/** Everything a page outside the two shells reads (nav is under 1 KB and both shells need it). */
export const BASE_NAMESPACES = ['common', 'appearance', 'language', 'auth', 'nav'] as const
/** Loaded on entering the shell that reads them. */
export const SHELL_NAMESPACES = ['admin', 'user'] as const

// BASE ∪ SHELL must be exactly the closed namespace set the backend validator mirrors
// (service/locale/validate.go). A namespace added to one list and not the other would be
// silently never loaded.
const _complete: readonly string[] = [...BASE_NAMESPACES, ...SHELL_NAMESPACES]
if (_complete.length !== NAMESPACES.length || !NAMESPACES.every(ns => _complete.includes(ns))) {
  throw new Error('i18n: BASE_NAMESPACES + SHELL_NAMESPACES must equal NAMESPACES')
}

/** The always-present language: the final fallbackLng, and the one a partial pack leans on. */
export const BASE_LANGUAGE = 'zh-CN'

export type Flat = Record<string, string>
export type LocaleLoaders = Record<string, () => Promise<{ default: Nested }>>
type Resources = Record<string, Record<string, Flat>>

export interface ResourceDeps {
  /** import.meta.glob map: `/src/locales/<lang>/<ns>.json` → dynamic import. */
  loaders: LocaleLoaders
  /** GET one uploaded pack (every namespace in a single JSON). */
  fetchPack: (lang: string) => Promise<{ namespaces: Record<string, Record<string, unknown>> }>
  isBuiltin: (lang: string) => boolean
  /** Resolves once the manifest of uploaded packs has been folded into the supported list. */
  manifestReady: () => Promise<void>
  instance: Pick<I18n, 'hasResourceBundle' | 'addResourceBundle' | 'changeLanguage' | 'language' | 'resolvedLanguage'>
}

export function createResources(deps: ResourceDeps) {
  const { loaders, instance } = deps
  // BASE is needed from the first paint; a shell adds its own when it is entered.
  const required = new Set<string>(BASE_NAMESPACES)

  async function loadBuiltinNamespace(lang: string, ns: string): Promise<Flat> {
    const loader = loaders[`/src/locales/${lang}/${ns}.json`]
    return loader ? flatten((await loader()).default) : {}
  }

  async function loadPack(lang: string): Promise<Record<string, Flat>> {
    const { namespaces } = await deps.fetchPack(lang)
    const out: Record<string, Flat> = {}
    // A pack carries all namespaces in one JSON, so it is fetched whole whichever pages
    // need it; registering every namespace costs nothing extra. Missing ones are {} and
    // the zh-CN fallback fills the gaps.
    for (const ns of NAMESPACES) out[ns] = namespaces[ns] ? flatten(namespaces[ns] as Nested) : {}
    return out
  }

  // Fetch what `lang` is missing for `nss`, as a resources tree. `has` says what is already
  // registered (nothing, at boot).
  async function collect(lang: string, nss: readonly string[], has: (lng: string, ns: string) => boolean): Promise<Resources> {
    const out: Resources = {}
    const put = (lng: string, ns: string, flat: Flat) => { (out[lng] ??= {})[ns] = flat }
    const builtin = deps.isBuiltin(lang)
    if (builtin) {
      await Promise.all(nss.filter(ns => !has(lang, ns)).map(async ns => put(lang, ns, await loadBuiltinNamespace(lang, ns))))
    } else if (!has(lang, 'common')) {
      for (const [ns, flat] of Object.entries(await loadPack(lang))) put(lang, ns, flat)
    }
    // The two built-in languages have exactly the same keys (localeParity.test.ts enforces
    // it; zh-TW is generated from zh-CN), so they never need the fallback bundle. An uploaded
    // pack may be partial, and `load: 'currentOnly'` means i18next will not fetch a fallback
    // itself, so the namespaces in use are registered from zh-CN or a missing key would
    // render raw.
    if (!builtin && lang !== BASE_LANGUAGE) {
      await Promise.all(nss.filter(ns => !has(BASE_LANGUAGE, ns) && !out[BASE_LANGUAGE]?.[ns])
        .map(async ns => put(BASE_LANGUAGE, ns, await loadBuiltinNamespace(BASE_LANGUAGE, ns))))
    }
    return out
  }

  const hasNow = (lng: string, ns: string) => instance.hasResourceBundle(lng, ns)

  async function register(lang: string): Promise<void> {
    // Repeat until nothing is missing: a shell can be entered while a load is in flight and
    // add its namespace to `required` meanwhile.
    for (let guard = 0; guard < 4; guard++) {
      const missing = await collect(lang, [...required], hasNow)
      const added = Object.entries(missing).flatMap(([lng, nss]) => Object.entries(nss).map(([ns, flat]) => [lng, ns, flat] as const))
      if (added.length === 0) return
      for (const [lng, ns, flat] of added) instance.addResourceBundle(lng, ns, flat, true, true)
    }
  }

  return {
    /** The resources tree for i18n.init: the BASE namespaces (plus the fallback for a pack). */
    initial: (lang: string) => collect(lang, [...required], () => false),

    /**
     * Make `nss` available in the active language before a shell renders them. Safe to call
     * repeatedly; a namespace already loaded costs nothing.
     */
    async ensureNamespaces(nss: readonly string[]): Promise<void> {
      for (const ns of nss) required.add(ns)
      await register(instance.resolvedLanguage || instance.language || BASE_LANGUAGE)
    },

    /**
     * Switch language: load every namespace the current shell needs for `lang` FIRST, so
     * the switch never paints a page of raw keys. On a load failure nothing changes.
     */
    async setLanguage(lang: string): Promise<void> {
      if (!deps.isBuiltin(lang)) await deps.manifestReady()
      try {
        await register(lang)
      } catch (err) {
        // eslint-disable-next-line no-console
        console.warn('setLanguage: failed to load resources for', lang, err)
        return
      }
      await instance.changeLanguage(lang)
    },
  }
}
