import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'

// Copy rule 8 (audit section 7.2): a space goes between Chinese and Latin
// letters or digits, and NOT between two Chinese words. A template cannot know
// what it will be filled with (a node called "HK 03" or "香港 03"), so the
// templates are written without spaces at the joins and this adds the ones the
// rule asks for after interpolation. Text that already has the space is left alone.
const CJK = '\\u3400-\\u4dbf\\u4e00-\\u9fff\\uf900-\\ufaff'
const toLatin = new RegExp(`([${CJK}])([A-Za-z0-9])`, 'g')
const fromLatin = new RegExp(`([A-Za-z0-9])([${CJK}])`, 'g')

export function autoSpace(text: string): string {
  return text.replace(toLatin, '$1 $2').replace(fromLatin, '$1 $2')
}

/** `t` from the common namespace with rule 8 applied to every result. */
export function useSpacedT() {
  const { t, i18n } = useTranslation('common')
  const spaced = useCallback(((...args: Parameters<TFunction>) => autoSpace(String(t(...args)))) as unknown as TFunction, [t])
  return { t: spaced, i18n }
}
