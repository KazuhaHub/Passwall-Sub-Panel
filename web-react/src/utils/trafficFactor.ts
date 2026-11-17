/** Default traffic factor in thousandths: users are charged one-for-one. */
export const DEFAULT_TRAFFIC_FACTOR_MILLI = 1000
export const MAX_TRAFFIC_FACTOR_MILLI = 100000

/** True when a factor differs from one-for-one. A missing value (older backend) counts as default. */
export function hasCustomFactor(milli: number | null | undefined): milli is number {
  return typeof milli === 'number' && milli !== DEFAULT_TRAFFIC_FACTOR_MILLI
}

/** Milli -> the decimal multiplier text shown to admins ("1.5", "0.25", "1"). */
export function milliToFactorText(milli: number | null | undefined): string {
  const m = typeof milli === 'number' ? milli : DEFAULT_TRAFFIC_FACTOR_MILLI
  return String(Math.round(m) / 1000)
}

/** Decimal multiplier text -> integer milli, or null when it is not a valid 0..100 multiplier. */
export function factorTextToMilli(text: string): number | null {
  const s = text.trim()
  if (!/^\d+(\.\d+)?$|^\.\d+$/.test(s)) return null
  const milli = Math.round(Number(s) * 1000)
  if (!Number.isFinite(milli) || milli < 0 || milli > MAX_TRAFFIC_FACTOR_MILLI) return null
  return milli
}
