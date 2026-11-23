import { useCallback } from 'react'
import type { TFunction } from 'i18next'
import { useTranslation } from 'react-i18next'
import { spacedT } from '@/i18n/spacing'

/** `t` for this page: both namespaces, with interpolation boundaries resolved (copy rule 8). Keys read `admin:groups.…`. */
export function useGroupsT(): TFunction {
  const { t } = useTranslation(['admin', 'common'])
  return useCallback(spacedT(t), [t]) // eslint-disable-line react-hooks/exhaustive-deps
}
