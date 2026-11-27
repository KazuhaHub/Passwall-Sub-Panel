import type { TFunction } from 'i18next'

export const mb = (n: number) => `${(n / (1 << 20)).toFixed(2)} MB`

/**
 * The sentence for a rule-set request the server (or a proxy in front of it) refused as too large. The panel's own 413
 * reports its limit; a reverse proxy's does not, and may allow less than the panel does, so then the sentence says the
 * limit is not known and names the nginx setting that governs it. `size` is what the editor measured for that request.
 */
export function tooLargeText(t: TFunction, limitBytes: number | undefined, size: number): string {
  return limitBytes
    ? t('admin:rules.too_large', { size: mb(size), limit: mb(limitBytes) })
    : t('admin:rules.too_large_proxy', { size: mb(size) })
}
