import { describe, expect, it } from 'vitest'

import { ACCESS_TONE_KINDS } from '@/utils/accessControl'
import { ACCESS_STATUS } from './accessStatus'

describe('the access status table', () => {
  it('has a row for every kind', () => {
    for (const k of ACCESS_TONE_KINDS) expect(ACCESS_STATUS[k]).toBeDefined()
  })

  it('never gives two kinds the same tone and icon, so colour is not the only difference', () => {
    const seen = new Map<string, string>()
    for (const k of ACCESS_TONE_KINDS) {
      const s = ACCESS_STATUS[k]
      const key = `${s.tone}/${s.icon.displayName ?? s.icon.name}`
      expect(seen.get(key), `${k} repeats ${seen.get(key)}`).toBeUndefined()
      seen.set(key, k)
    }
  })
})
