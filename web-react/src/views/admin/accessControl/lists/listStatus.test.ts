// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'

import { setTestLang, translate } from '@/test/accessControlHarness'
import { listStatus, PARSE_REASON_CODES, PARSE_WARNING_CODES, reasonText, warningText } from './listStatus'

const t = (k: string, o?: Record<string, unknown>) => translate(k, o)

describe('listStatus', () => {
  const cases = [
    { state: 'ready', fetched: 5, used: false, key: 'ready', tone: 'ok', problem: false },
    { state: 'refreshing', fetched: 5, used: true, key: 'refreshing', tone: 'measuring', problem: false },
    { state: 'failed', fetched: 5, used: true, key: 'failed_old', tone: 'attention', problem: true },
    // Never fetched: there is no old content to claim to be using.
    { state: 'failed', fetched: null, used: false, key: 'failed_never', tone: 'failing', problem: true },
    { state: 'pending', fetched: null, used: true, key: 'pending_used', tone: 'failing', problem: true },
    { state: 'pending', fetched: null, used: false, key: 'pending', tone: 'measuring', problem: false },
  ] as const
  it.each(cases)('$state fetched=$fetched used=$used', ({ state, fetched, used, key, tone, problem }) => {
    expect(listStatus({ state, last_fetched_at: fetched }, used)).toEqual({ key, tone, problem })
  })
})

// Every reason and error code destlist/parse.go can report resolves to words,
// never to a raw key, in both languages; an unknown code is the generic sentence.
describe.each(['zh-CN', 'en-US'] as const)('parse codes in %s', lang => {
  it('every reason code has its own sentence', () => {
    setTestLang(lang)
    const seen = new Set<string>()
    for (const code of PARSE_REASON_CODES) {
      const text = reasonText(t, code)
      expect(text, code).not.toContain('access_control')
      expect(text).not.toBe(reasonText(t, 'no_such_code'))
      seen.add(text)
    }
    expect(seen.size).toBe(PARSE_REASON_CODES.length)
    expect(reasonText(t, 'no_such_code')).not.toContain('access_control')
  })
  it('every warning code has its own sentence', () => {
    setTestLang(lang)
    for (const code of PARSE_WARNING_CODES) expect(warningText(t, code)).not.toContain('access_control')
    expect(warningText(t, 'x')).not.toContain('access_control')
  })
})
