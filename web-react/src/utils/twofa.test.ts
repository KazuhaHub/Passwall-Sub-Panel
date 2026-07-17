import { beforeEach, describe, expect, it, vi } from 'vitest'

import {
  defaultTwoFAMethod,
  LAST_2FA_METHOD_KEY,
  readLast2FAMethod,
  writeLast2FAMethod,
} from './twofa'

describe('2FA method selection', () => {
  const values = new Map<string, string>()

  beforeEach(() => {
    values.clear()
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
    })
  })

  it('prefers a remembered method only when it is currently available', () => {
    values.set(LAST_2FA_METHOD_KEY, 'email')
    expect(defaultTwoFAMethod(['totp', 'email'])).toBe('email')
    expect(defaultTwoFAMethod(['passkey', 'totp'])).toBe('passkey')
  })

  it('uses the security priority and preserves the legacy empty fallback', () => {
    expect(defaultTwoFAMethod(['recovery', 'totp', 'email'])).toBe('totp')
    expect(defaultTwoFAMethod([])).toBe('totp')
  })

  it('persists valid methods and rejects unknown stored values', () => {
    writeLast2FAMethod('passkey')
    expect(readLast2FAMethod()).toBe('passkey')
    values.set(LAST_2FA_METHOD_KEY, 'sms')
    expect(readLast2FAMethod()).toBeNull()
  })

  it('fails safely when storage access is unavailable', () => {
    vi.stubGlobal('localStorage', {
      getItem: () => { throw new Error('blocked') },
      setItem: () => { throw new Error('blocked') },
    })
    expect(readLast2FAMethod()).toBeNull()
    expect(() => writeLast2FAMethod('totp')).not.toThrow()
  })
})
