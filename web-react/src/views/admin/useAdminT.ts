import { useCallback } from 'react'
import type { TFunction } from 'i18next'
import { useTranslation } from 'react-i18next'
import { spacedT } from '@/i18n/spacing'

/**
 * `t` for the admin pages that are not under nodes/: the admin namespace first, `common:`, `auth:` and `language:` by prefix,
 * with interpolation boundaries resolved (copy rule 8) like common's useSpacedT.
 */
export function useAdminT(): TFunction {
  const { t } = useTranslation(['admin', 'common', 'auth', 'language'])
  return useCallback(spacedT(t), [t]) // eslint-disable-line react-hooks/exhaustive-deps
}
