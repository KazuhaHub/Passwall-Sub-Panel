import type { Localized } from '@/api/accessControl'

/** A catalog string in the UI language: English readers get en-US, everyone else their own pack's, and zh-CN is the fallback. */
export function pickLocalized(l: Localized | undefined, lang: string): string {
  if (!l) return ''
  if (l[lang]) return l[lang]
  if (lang.toLowerCase().startsWith('en') && l['en-US']) return l['en-US']
  return l['zh-CN'] ?? Object.values(l)[0] ?? ''
}
