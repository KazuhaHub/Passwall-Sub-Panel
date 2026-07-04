import type { InitOptions } from 'i18next'

// Side-effect-free i18n building blocks: the namespace list, the flatten helper,
// and the static (resource-independent) i18next options. Kept in their own module
// so tests can import them WITHOUT triggering the bootstrap IIFE in ./index.ts
// (which does network + glob work on import). ./index.ts re-uses these too, so a
// test verifies the REAL config rather than a drifting copy.

// Locale namespaces. Each (lang, ns) bundle is loaded via dynamic import
// — Vite splits them into per-namespace chunks so a user only downloads
// the languages and namespaces they actually need. Keep in sync with the
// backend namespace whitelist in service/locale.
export const NAMESPACES = ['common', 'appearance', 'language', 'auth', 'nav', 'admin', 'user'] as const

export type Nested = { [k: string]: string | Nested }

// Flatten { a: { b: { c: 'x' } } } → { 'a.b.c': 'x' }.
// We do this at register time + run i18n with keySeparator:false so a call like
// t('admin:servers.title') treats 'servers.title' as one flat key. Workaround
// for an i18next quirk where instance-level keySeparator doesn't actually get
// used during nested-key resolution.
export function flatten(obj: Nested, prefix = ''): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (typeof v === 'string') out[key] = v
    else if (v && typeof v === 'object') Object.assign(out, flatten(v, key))
  }
  return out
}

// The static, resource-independent i18next options shared by the runtime instance
// (./index.ts) and tests. The dynamic bits — resources, lng, supportedLngs — plus
// the plugin config (detection, react) are supplied per-instance in ./index.ts.
//
// These are exactly the options that govern missing-key fallback:
//   - keySeparator:false + nsSeparator:':'  → dotted keys are single flat lookups
//   - fallbackLng → zh-CN                    → an untranslated key resolves to the
//                                              built-in language, never a raw key
//   - fallbackNS:'common'
export const I18N_STATIC_OPTIONS: InitOptions = {
  // Map generic browser language tags (en/zh) onto the exact bundles we ship.
  // Keep zh-CN as the final fallback so missing translations never surface raw
  // keys in normal use.
  fallbackLng: {
    en: ['en-US', 'zh-CN'],
    zh: ['zh-CN'],
    default: ['zh-CN'],
  },
  load: 'currentOnly',
  ns: [...NAMESPACES],
  defaultNS: 'common',
  fallbackNS: 'common',
  // Resources are pre-flattened to dotted keys, so the runtime no longer needs to
  // walk a nested object — keySeparator:false makes t() treat the whole
  // 'servers.title' string as one flat lookup.
  keySeparator: false,
  nsSeparator: ':',
  interpolation: { escapeValue: false },
}
