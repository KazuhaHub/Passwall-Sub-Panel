import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { spacedT } from '@/i18n/spacing'

/**
 * `useTranslation` for the risk center: the same `t` (keys keep their `admin:` prefix, which is what
 * i18n/riskKeys.test.ts scans for) with interpolation boundaries resolved, so a Chinese sentence around
 * an account name or a number gets its Latin-letter spacing right.
 */
export function useRiskT() {
  const { t, i18n } = useTranslation(['admin', 'common'])
  // eslint-disable-next-line react-hooks/exhaustive-deps
  return { t: useCallback(spacedT(t), [t]), i18n }
}
