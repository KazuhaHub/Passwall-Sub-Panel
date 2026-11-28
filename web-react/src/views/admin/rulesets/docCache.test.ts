import { describe, expect, it } from 'vitest'
import { editRule } from '@/utils/ruleset/ops'
import { parseDoc, serializeDoc } from '@/utils/ruleset/parse'
import { readDoc, seedDoc } from './docCache'

describe('seedDoc', () => {
  it('hands readers the document an edit produced instead of reading its text again', () => {
    const after = editRule(parseDoc('- DOMAIN,a.example,X\n- DOMAIN,b.example,Y\n'), 0, { target: 'Z' })
    const text = serializeDoc(after)
    seedDoc(text, after)
    expect(readDoc(text)).toBe(after)
  })

  it('keeps only the last few documents', () => {
    const first = parseDoc('- DOMAIN,first.example,X\n')
    seedDoc(serializeDoc(first), first)
    for (let i = 0; i < 6; i++) { const d = parseDoc(`- DOMAIN,${i}.example,X\n`); seedDoc(serializeDoc(d), d) }
    expect(readDoc(serializeDoc(first))).not.toBe(first)
  })
})
