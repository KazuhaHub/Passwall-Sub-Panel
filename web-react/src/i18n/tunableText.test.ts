import { describe, expect, it } from 'vitest'

import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import { flatten, type Nested } from './options'

// THE RISK TEXTS NAME NO TUNABLE AS A FACT. The risk worker's cadence,
// usage_shift's baseline and judged days, its warm-up and flag thresholds,
// login_country's hold and the shared-exit threshold used to be constants,
// and the texts said "hourly", "the past 4 weeks", "the last 7 days", "3 or
// more accounts". Each is a setting now. A sentence that states the shipped
// number as the rule is wrong, silently, on every panel that changed it —
// and it reads as the truth, which is worse than a missing string. So a
// shipped number may appear only as what it is: the default, inside a
// parenthesis that says so ("（默认每小时）", "(hourly by default)"). A
// sentence about one verdict takes its numbers from the verdict's evidence.
//
// Scanned: the zh-CN and en-US values under the settings.geo_anomaly, risk
// and risk_center blocks (the policy page's labels and hints), the
// risk-signal and risk-center tabs, and the group editor's risk rows; plus
// the Chinese defaultValue copies in the settings view, which an i18n miss
// would show instead. The risk center's views carry no Chinese copies at all
// (riskKeys.test.ts).
const scanned = ['settings.geo_anomaly.', 'settings.risk.', 'settings.risk_center.', 'risk_signals.', 'risk_center.', 'groups.scope.risk_']

const literals: Record<'zh' | 'en', string[]> = {
  zh: ['每小时', '4 周', '最近 7 天', '3 个以上', '35 天', '满 14 天', '满 4 天'],
  en: ['hourly', 'four weeks', '4 weeks', 'last 7 days', '3+ ', '35 days', 'starts at 14', 'flagged at 4'],
}

// Drops every innermost parenthesis that names a default, repeatedly, so a
// qualified default nested in another parenthesis goes too.
function unqualified(text: string): string {
  let prev: string
  do {
    prev = text
    text = text.replace(/（[^（）]*默认[^（）]*）/g, '').replace(/\([^()]*by default[^()]*\)/g, '')
  } while (text !== prev)
  return text
}

function hits(lang: 'zh' | 'en', text: string): string[] {
  const rest = unqualified(text)
  return literals[lang].filter(l => rest.includes(l))
}

describe('no hard-coded tunable text remains', () => {
  it.each([['zh', zh], ['en', en]] as const)('%s risk and settings texts', (lang, bundle) => {
    const found: string[] = []
    for (const [key, value] of Object.entries(flatten(bundle as Nested))) {
      if (!scanned.some(p => key.startsWith(p))) continue
      for (const l of hits(lang, value)) found.push(`${key}: "${l}" in ${JSON.stringify(value)}`)
    }
    expect(found).toEqual([])
  })

  it.each(['../views/admin/SettingsView.tsx'])(
    'the Chinese defaultValue copies in %s', async file => {
      const fs = await import('node:fs')
      const source = fs.readFileSync(new URL(file, import.meta.url), 'utf8')
      // Every quoted string or template literal holding Chinese: comments
      // are English in these files, and a defaultValue is what an admin
      // reads when the bundle misses a key.
      const strings = source.match(/(['`])(?:\\.|(?!\1)[^\\\n])*[一-鿿](?:\\.|(?!\1)[^\\\n])*\1/g) ?? []
      expect(strings.length).toBeGreaterThan(0)
      const found = strings.flatMap(s => hits('zh', s).map(l => `"${l}" in ${s}`))
      expect(found).toEqual([])
    })

  it('allows a default that says it is one', () => {
    // The guard's own escape hatch, pinned: without it the texts that must
    // name the shipped value could not be written at all.
    expect(hits('zh', '按「风险信号计算间隔」（默认每小时）计算')).toEqual([])
    expect(hits('zh', '用量变化（与自己基线期（默认 4 周）相比）')).toEqual([])
    expect(hits('en', 'recomputed at the risk refresh interval (hourly by default)')).toEqual([])
    expect(hits('zh', '每小时计算一次')).toEqual(['每小时'])
    expect(hits('en', 'recomputed hourly')).toEqual(['hourly'])
  })
})
