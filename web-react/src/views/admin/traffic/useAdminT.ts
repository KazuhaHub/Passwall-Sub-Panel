import { useMemo } from 'react'
import type { TFunction } from 'i18next'
import { useTranslation } from 'react-i18next'
import { spacedT } from '@/i18n/spacing'

/** The admin namespace's `t` with interpolation boundaries resolved (copy rule 8), for the dashboard and traffic pages. */
export function useAdminT(): TFunction {
  const { t } = useTranslation('admin')
  return useMemo(() => spacedT(t), [t])
}
