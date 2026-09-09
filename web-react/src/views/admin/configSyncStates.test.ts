import { describe, expect, it } from 'vitest'
import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import { CONFIG_SYNC_KEY, CONFIG_SYNC_STATES, configSyncColor } from './configSync'

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
