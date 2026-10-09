import type { DestinationExemptionView } from '@/api/accessControl'
export type ExemptionExpiry = 'never' | 'day' | 'week' | 'custom'
export function localExpiry(value: number) {
  const date = new Date(value), pad = (v: number) => String(v).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}
export function exemptionExpiry(mode: ExemptionExpiry, custom: string, now: number, existing?: DestinationExemptionView): number | null {
  if (mode === 'never') return null
  if (mode === 'day' || mode === 'week') return now + (mode === 'day' ? 1 : 7) * 86400000
  // A reason-only edit retains the stored timestamp, including seconds and
  // milliseconds that the datetime-local control does not display.
  if (existing?.expires_at && custom === localExpiry(existing.expires_at)) return existing.expires_at
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(custom)) return NaN
  const value = new Date(custom).getTime()
  return Number.isFinite(value) && localExpiry(value) === custom ? value : NaN
}
export function validateExemption(userId: number | null, reason: string, expiresAt: number | null, now: number) {
  return { user: !userId || !Number.isSafeInteger(userId) || userId <= 0,
    reason: !reason.trim() || Array.from(reason.trim()).length > 255,
    expiry: expiresAt !== null && (!Number.isFinite(expiresAt) || expiresAt <= now) }
}
