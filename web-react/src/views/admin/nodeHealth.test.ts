import { describe, expect, it } from 'vitest'
import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import {
  NODE_HEALTH_KEY,
  NODE_HEALTH_PRODUCED,
  NODE_HEALTH_STATES,
  nodeHealthColor,
} from './nodeHealth'

const MD = { error: '#ef4444', outlineVariant: '#cbd5e1' }

describe('node health states', () => {
  /**
   * The defect this pins: `unreachable` had no palette entry, so it fell to the
   * unknown-state grey and a DOWN node rendered as "not yet probed". Any state
   * the backend can write that lands on the fallback is making a different and
   * wrong claim about the node, not merely an unstyled one.
   */
  it('gives every producible state its own colour, not the never-probed fallback', () => {
    const fallback = nodeHealthColor('a-state-that-does-not-exist', MD)
    for (const state of NODE_HEALTH_PRODUCED) {
      if (state === '') continue // '' IS the fallback, by definition
      expect(
        nodeHealthColor(state, MD),
        `state '${state}' falls through to the never-probed colour — it would render as "no signal"`,
      ).not.toBe(fallback)
    }
  })

  it('has wording in both bundles for every state', () => {
    for (const [lang, bundle] of [['zh-CN', zh], ['en-US', en]] as const) {
      for (const state of NODE_HEALTH_STATES) {
        const key = NODE_HEALTH_KEY[state]
        expect(
          bundle.nodes.health,
          `${lang} missing nodes.health.${key} (state '${state}')`,
        ).toHaveProperty(key)
      }
    }
  })

  /**
   * "The probe could not decide" must never look like "the probe says fine".
   * This is the whole reason the state was split out of ok — the UDP branch
   * used to report silence as healthy, so an unreachable Hysteria2 host was
   * indistinguishable from a working one.
   */
  it('never paints inconclusive as healthy', () => {
    expect(nodeHealthColor('inconclusive', MD)).not.toBe(nodeHealthColor('ok', MD))
  })

  /**
   * ...and it must not look like a hard failure either. An obfuscated UDP node
   * is permanently unprobeable, so painting it red would put a permanent alarm
   * on a condition nobody can clear.
   */
  it('does not paint inconclusive as a failure', () => {
    expect(nodeHealthColor('inconclusive', MD)).not.toBe(nodeHealthColor('unreachable', MD))
  })

  /**
   * '' means the probe has not run; 'inconclusive' means it ran and could not
   * tell. Collapsing them loses the only evidence that the probe is alive.
   */
  it('separates never-probed from probed-but-undecidable', () => {
    expect(nodeHealthColor('', MD)).not.toBe(nodeHealthColor('inconclusive', MD))
    expect(NODE_HEALTH_KEY['']).not.toBe(NODE_HEALTH_KEY.inconclusive)
  })
})
