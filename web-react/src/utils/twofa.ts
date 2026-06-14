import type { TwoFAMethod } from '@/api/types'

// Shared 2FA method-selection logic, used by BOTH the login challenge and the
// in-app step-up verifications (recovery-code regeneration, disabling TOTP, …)
// so they behave identically: land on the method the user used last time
// (per-browser), falling back to the strongest available factor — instead of
// every dialog hard-defaulting to a TOTP code the user may not even have.
export const LAST_2FA_METHOD_KEY = 'psp.2fa.last_method'

// Stable display/priority order, also the fallback when there's no remembered
// choice (or it isn't offered this time). Passkey first: a WebAuthn assertion is
// phishing-resistant (bound to the origin + hardware), strictly stronger than a
// TOTP code a user can be tricked into typing on a lookalike site. Email + one-
// time recovery codes are weaker fallbacks, last.
export const TWOFA_PRIORITY: TwoFAMethod[] = ['passkey', 'totp', 'email', 'recovery']

export function readLast2FAMethod(): TwoFAMethod | null {
  try {
    const v = localStorage.getItem(LAST_2FA_METHOD_KEY)
    return TWOFA_PRIORITY.includes(v as TwoFAMethod) ? (v as TwoFAMethod) : null
  } catch {
    return null
  }
}

export function writeLast2FAMethod(m: TwoFAMethod) {
  try {
    localStorage.setItem(LAST_2FA_METHOD_KEY, m)
  } catch {
    /* ignore */
  }
}

// defaultTwoFAMethod picks the remembered method if it's available this round,
// else the first available in priority order. `available` is the set of methods
// this context actually offers (login challenge methods, or for a step-up the
// methods the account has: passkey enrolled / totp enabled / recovery codes
// left). Empty input falls back to 'totp' for backward-compatibility.
export function defaultTwoFAMethod(available: TwoFAMethod[]): TwoFAMethod {
  const order = TWOFA_PRIORITY.filter(m => available.includes(m))
  const last = readLast2FAMethod()
  if (last && order.includes(last)) return last
  return order[0] ?? 'totp'
}
