import { Box, Link } from '@mui/material'
import { Link as RouterLink } from 'react-router'
import { useTranslation } from 'react-i18next'

export default function LegalLinks({ newTab = false, version }: { newTab?: boolean; version?: number }) {
  const { t, i18n } = useTranslation('auth')
  const search = `?lang=${encodeURIComponent(i18n.resolvedLanguage || i18n.language)}${version ? `&v=${version}` : ''}`
  return <Box component="span" sx={{ display: 'inline-flex', flexWrap: 'wrap', gap: 1.5, alignItems: 'center' }}>
    {(['terms', 'privacy'] as const).map(kind => <Link key={kind} component={RouterLink} to={`/legal/${kind}${search}`}
      target={newTab ? '_blank' : undefined} rel={newTab ? 'noopener noreferrer' : undefined}
      onClick={event => event.stopPropagation()} sx={{ minHeight: 44, display: 'inline-flex', alignItems: 'center' }}>{t(`legal.${kind}`)}</Link>)}
  </Box>
}
