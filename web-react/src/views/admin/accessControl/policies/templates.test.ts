import { expect, it } from 'vitest'
import { pickLocalized } from './templates'

const l = { 'zh-CN': '中', 'en-US': 'en' }
it('picks by language with zh-CN as the fallback', () => {
  expect(pickLocalized(l, 'en-US')).toBe('en')
  expect(pickLocalized(l, 'en-GB')).toBe('en')
  expect(pickLocalized(l, 'zh-TW')).toBe('中')
  expect(pickLocalized(l, 'fr-FR')).toBe('中')
  expect(pickLocalized(undefined, 'en-US')).toBe('')
})
