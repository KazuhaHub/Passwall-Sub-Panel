import { describe, expect, it } from 'vitest'
import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import { CONFIG_SYNC_KEY, CONFIG_SYNC_STATES, configSyncColor, humanizeSince } from './configSync'

/**
 * Every config_sync_state the Go side can write must have its own dot colour
 * and its own wording here.
 *
 * configSyncColor falls back to the "never captured" grey for anything it does
 * not recognise, and the label falls back with it — so a state added on the
 * backend without copy here does not render as an obvious gap. It renders as
 * "未捕获本地配置（渲染时回源）", a DIFFERENT and wrong claim about the node.
 * That is exactly how "failed" would have arrived looking like "never
 * captured".
 *
 * The list is duplicated on the Go side (domain.ConfigSync* and
 * TestConfigSyncStatesAreExhaustive). Two lists, one guard each — the same
 * shape as the locale bundles, because neither language can read the other.
 */
const MD = { error: '#ef4444', outlineVariant: '#cbd5e1' }

describe('config sync states', () => {
  it('gives every state its own colour decision, not the unrecognised fallback', () => {
    const fallback = configSyncColor('a-state-that-does-not-exist', MD)
    for (const state of CONFIG_SYNC_STATES) {
      if (state === '') continue // '' IS the fallback, by definition
      expect(configSyncColor(state, MD), `state ${state} falls through to the unrecognised colour`)
        .not.toBe(fallback)
    }
  })

  it('has wording in both bundles for every state that names one', () => {
    const keys = CONFIG_SYNC_STATES.map(s => CONFIG_SYNC_KEY[s])
    for (const [lang, bundle] of [['zh-CN', zh], ['en-US', en]] as const) {
      for (const k of keys) {
        expect(bundle.nodes.config_sync, `${lang} missing nodes.config_sync.${k}`).toHaveProperty(k)
      }
    }
  })

  // pending promises a retry; failed has had its retry cancelled. If the two
  // read the same, the operator cannot tell whether to wait or to act — which
  // is the whole reason failed was split out.
  it('does not let failed and pending say the same thing', () => {
    for (const bundle of [zh, en]) {
      const cs = bundle.nodes.config_sync as Record<string, string>
      expect(cs.failed).not.toBe(cs.pending)
      expect(cs.failed.length, 'failed must say what the operator should do about it').toBeGreaterThan(10)
    }
  })
})

describe('humanizeSince', () => {
  const now = new Date('2026-09-09T12:00:00Z')
  const ago = (ms: number) => new Date(now.getTime() - ms).toISOString()

  // Coarse by design: the reader is deciding wait-or-investigate, and that
  // turns on minutes-versus-days.
  it('reads at the granularity the decision needs', () => {
    expect(humanizeSince(ago(30_000), now)).toBe('<1 min')
    expect(humanizeSince(ago(5 * 60_000), now)).toBe('5 min')
    expect(humanizeSince(ago(3 * 3_600_000), now)).toBe('3 h')
    expect(humanizeSince(ago(72 * 3_600_000), now)).toBe('3 d')
  })

  // An unknown lag renders as nothing, never as a number. A tooltip saying
  // "un-converged for 0 min" on a node whose stamp is missing would be a
  // confident answer to a question the panel cannot answer.
  it('returns nothing rather than guessing', () => {
    expect(humanizeSince('not-a-date', now)).toBe('')
    expect(humanizeSince(new Date(now.getTime() + 60_000).toISOString(), now)).toBe('')
  })
})
