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
const scanned = [
  'settings.geo_anomaly.', 'settings.risk.', 'settings.risk_center.', 'risk_signals.', 'risk_center.', 'groups.scope.risk_',
  // The access-control page: its windows are settings (the apply wait, the
  // refresh interval, the quotas) and its texts interpolate them.
  'access_control.', 'servers.access.',
]

// The access-control keys are also held to the stricter rule below: no digit
// directly before a day unit at all, because every number of days is a setting
// or the min of one (the one fixed window in this block, the 24 hours of
// \"hits in the last 24 hours\", is a unit of the server's field name).

// The hit records add the windows of the access-control page: the days a count
// is over are `min(7, retention)` (R14) and the retention is a setting, so a
// text interpolates them ({{days}}) and never states 7 or 30 as the rule.
const literals: Record<'zh' | 'en', string[]> = {
  zh: ['每小时', '4 周', '最近 7 天', '3 个以上', '35 天', '满 14 天', '满 4 天', '过去 7 天', '近 7 天', '7 天内', '7 天命中', '保留 30 天', '保留 7 天'],
  en: ['hourly', 'four weeks', '4 weeks', 'last 7 days', '3+ ', '35 days', 'starts at 14', 'flagged at 4', 'past 7 days', 'in 7 days', 'kept 7 days', 'kept 30 days'],
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

describe('the access-control texts name their windows only through the setting', () => {
  const cases = [['zh', zh], ['en', en]] as const
  it.each(cases)('%s: no literal number of days in access_control.*, servers.access.* or the hit texts of the risk center', (_lang, bundle) => {
    const found: string[] = []
    for (const [key, value] of Object.entries(flatten(bundle as Nested))) {
      if (!(key.startsWith('access_control.') || key.startsWith('servers.access.') || key.startsWith('risk_signals.dest_block'))) continue
      // An exemption's expiry choices (24 hours, 7 days, never) are options the admin picks, not a window the page states.
      if (key.startsWith('access_control.exemptions.dialog.d')) continue
      // "{{days}} 天" is the form; "7 天" or "30 days" is a rule that is only true on the shipped settings.
      if (/(?<![{\w])\d+\s*(天|days?)(?!\w)/.test(unqualified(value))) found.push(`${key}: ${JSON.stringify(value)}`)
    }
    expect(found).toEqual([])
  })

  it('catches a planted literal and allows a qualified default', () => {
    expect(hits('zh', '过去 7 天本应拦截')).toEqual(['过去 7 天'])
    expect(hits('en', 'Hits in the past 7 days')).toEqual(['past 7 days'])
    expect(hits('zh', '命中记录保留（默认 30 天）')).toEqual([])
  })
})
