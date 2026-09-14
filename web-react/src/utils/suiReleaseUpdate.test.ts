import { describe, expect, it } from 'vitest'
import { newerSUIRelease } from './suiReleaseUpdate'

describe('S-UI shared release metadata', () => {
  it.each(['1.5.0', 'v1.5.0', '1.6.2-beta.1'])('shows a stable release newer than %s', current => {
    expect(newerSUIRelease(current, 'v1.6.2')).toBe('v1.6.2')
  })
  it.each(['1.6.2', 'v1.6.2', '1.7.0', '2.0.0', 'dev', 'unknown', '', undefined, '1.6', '01.5.0'])
    ('does not misreport %s as out of date', current => {
      expect(newerSUIRelease(current, 'v1.6.2')).toBeUndefined()
    })
  it.each([null, '', 'dev', 'v1.6', 'v1.6.2-beta.1'])('refuses unavailable/unstable metadata %s', target => {
    expect(newerSUIRelease('1.5.0', target)).toBeUndefined()
  })
})
