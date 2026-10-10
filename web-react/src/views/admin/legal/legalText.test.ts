import { describe, expect, it } from 'vitest'
import { countLegalChanges, legalBytes, MAX_LEGAL_BYTES } from './legalText'

describe('legal publication text', () => {
  it('measures UTF-8 bytes rather than UTF-16 length', () => {
    expect(legalBytes('中😀a')).toBe(8)
    expect(legalBytes('中'.repeat(20_000))).toBe(MAX_LEGAL_BYTES)
    expect(legalBytes('中'.repeat(20_001))).toBeGreaterThan(MAX_LEGAL_BYTES)
  })
  it('counts separate edits and non-BMP characters', async () => {
    expect(await countLegalChanges('甲😀乙旧丙尾', '甲新乙丙多尾')).toEqual({ added: 2, removed: 2 })
    expect(await countLegalChanges('', '中😀')).toEqual({ added: 2, removed: 0 })
    expect(await countLegalChanges('unchanged', 'unchanged')).toEqual({ added: 0, removed: 0 })
  })
})
