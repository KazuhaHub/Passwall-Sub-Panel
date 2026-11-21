import { useCallback } from 'react'
import type { TFunction } from 'i18next'
import { useTranslation } from 'react-i18next'
import { spacedT } from '@/i18n/spacing'

/**
 * The `user` namespace's `t` with interpolation boundaries resolved (copy rule 8), and the `i18n` instance for the
 * one place that branches on the UI language. Keys are read without the namespace prefix, as the old page did.
 */
export function useMeT() {
  const { t, i18n } = useTranslation('user')
  return { t: useCallback(spacedT(t), [t]) as TFunction, i18n } // eslint-disable-line react-hooks/exhaustive-deps
}
