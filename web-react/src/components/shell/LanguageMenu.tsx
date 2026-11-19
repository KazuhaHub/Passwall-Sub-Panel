import { useTranslation } from 'react-i18next'

import { IconLanguage } from '@/lib/icons'
import { IconButton } from '@/components/common/IconButton'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { SUPPORTED_LANGUAGES, currentLanguage, isBuiltinLanguage, serverLanguageMeta, setLanguage } from '@/i18n'

/**
 * The language switcher. It lists SUPPORTED_LANGUAGES by reference, so a language pack
 * an admin uploaded (appended there at boot) shows up with its own endonym, under any
 * active UI language. Built-in names come from the `language` namespace.
 */
export function LanguageMenu({ value, onChange }: {
  /** Defaults to the active language; the caller passes it only to control it. */
  value?: string
  onChange?: (lang: string) => void
}) {
  const { t, i18n } = useTranslation('language')
  // Reading the i18n instance's language (not currentLanguage() alone) is what makes
  // the checkmark follow a switch made anywhere else.
  const active = value ?? (i18n.resolvedLanguage || currentLanguage())

  function label(lng: string): string {
    if (isBuiltinLanguage(lng)) return t(lng)
    return serverLanguageMeta(lng)?.name ?? lng
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<IconButton label={t('title')} />}>
        <IconLanguage />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-44">
        <DropdownMenuRadioGroup value={active} onValueChange={lng => { (onChange ?? (l => { void setLanguage(l) }))(String(lng)) }}>
          {SUPPORTED_LANGUAGES.map(lng => (
            <DropdownMenuRadioItem key={lng} value={lng}>{label(lng)}</DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
