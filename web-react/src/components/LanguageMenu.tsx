import { useState, type MouseEvent } from 'react'
import {
  IconButton,
  Menu,
  MenuItem,
  ListItemText,
  Tooltip,
} from '@mui/material'
import TranslateIcon from '@mui/icons-material/Translate'
import CheckIcon from '@mui/icons-material/Check'
import { useTranslation } from 'react-i18next'
import type { AppLanguage } from '@/theme'
import { SUPPORTED_LANGUAGES, isBuiltinLanguage, serverLanguageMeta } from '@/i18n'

interface Props {
  value: AppLanguage
  onChange: (lang: AppLanguage) => void
}

export default function LanguageMenu({ value, onChange }: Props) {
  const { t } = useTranslation('language')
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)

  function open(e: MouseEvent<HTMLElement>) {
    setAnchor(e.currentTarget)
  }
  function pick(lang: AppLanguage) {
    onChange(lang)
    setAnchor(null)
  }

  // Built-in labels come from the `language` namespace (t(lng)); uploaded packs
  // carry their own endonym in the manifest, shown under any active UI language.
  function label(lng: AppLanguage): string {
    if (isBuiltinLanguage(lng)) return t(lng)
    return serverLanguageMeta(lng)?.name ?? lng
  }

  return (
    <>
      <Tooltip title={t('title')}>
        <IconButton onClick={open} aria-label={t('title')}>
          <TranslateIcon />
        </IconButton>
      </Tooltip>
      <Menu
        open={!!anchor}
        anchorEl={anchor}
        onClose={() => setAnchor(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
        PaperProps={{ sx: { minWidth: 180, mt: 1 } }}
      >
        {SUPPORTED_LANGUAGES.map(lng => (
          <MenuItem key={lng} onClick={() => pick(lng)} selected={lng === value}>
            <ListItemText primary={label(lng)} />
            {lng === value && <CheckIcon fontSize="small" />}
          </MenuItem>
        ))}
      </Menu>
    </>
  )
}
