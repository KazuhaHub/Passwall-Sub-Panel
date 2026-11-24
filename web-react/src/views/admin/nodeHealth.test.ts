import { describe, expect, it } from 'vitest'
import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import {
  NODE_HEALTH_KEY,
  NODE_HEALTH_STATES,
} from './nodeHealth'

describe('node health states', () => {
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
   * '' means the probe has not run; 'inconclusive' means it ran and could not
   * tell. Collapsing them loses the only evidence that the probe is alive.
   */
  it('keeps separate wording for never-probed and probed-but-undecidable', () => {
    expect(NODE_HEALTH_KEY['']).not.toBe(NODE_HEALTH_KEY.inconclusive)
  })
})
