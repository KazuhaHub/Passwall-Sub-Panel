import { useCallback } from 'react'
import type { TFunction } from 'i18next'
import { useTranslation } from 'react-i18next'
import { spacedT } from '@/i18n/spacing'

/** The admin namespace's `t`, with interpolation boundaries resolved (copy rule 8) like common's useSpacedT. Keys read `nodes.…`. */
export function useNodesT(): TFunction {
  const { t } = useTranslation('admin')
  return useCallback(spacedT(t), [t]) // eslint-disable-line react-hooks/exhaustive-deps
}
