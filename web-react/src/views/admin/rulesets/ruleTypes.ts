import type { TFunction } from 'i18next'
import { SINGBOX_TYPES } from '@/utils/ruleset/types'

/** The name a person reads for a type: "Domain suffix", with the keyword it is written as when it is a known one. */
export function typeName(t: TFunction, type: string, known: boolean): string {
  const upper = type.toUpperCase()
  if (!known) return t('admin:rules.types.unknown')
  if (upper === 'AND' || upper === 'OR' || upper === 'NOT') return t('admin:rules.types.logical')
  const key = `admin:rules.types.${upper}`
  return t(key, { defaultValue: upper })
}

/** Whether sing-box keeps rules of this type (it silently skips the rest). */
export const singboxSupports = (type: string) => SINGBOX_TYPES.has(type.toUpperCase())
