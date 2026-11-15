import { Fragment } from 'react'
import { Box, type SxProps, type Theme } from '@mui/material'
import { Link as RouterLink } from 'react-router'
import { useTranslation } from 'react-i18next'

import { useSiteStore } from '@/stores/site'

const KINDS = ['terms', 'privacy'] as const

/** The links to the two documents. A 44px-high target on a phone, where the
 *  footer's 11-12px text would otherwise be a sliver to hit. */
export function LegalLinks() {
  const { t } = useTranslation('auth')
  return <>
    {KINDS.map((kind, i) => (
      <Fragment key={kind}>
        {i > 0 && ' · '}
        <Box component={RouterLink} to={`/legal/${kind}`} sx={{
          color: 'inherit', display: 'inline-flex', alignItems: 'center', minHeight: { xs: 44, sm: 'auto' }, px: 0.5,
        }}>{t(`legal.kind.${kind}`)}</Box>
      </Fragment>
    ))}
  </>
}

/**
 * The page footer of the login, register and portal pages: the site's footer
 * text, then the terms and privacy links when the feature is on. It renders when
 * either exists, so a site with no footer text still carries the links.
 */
export default function SiteFooter({ component, sx }: { component?: React.ElementType; sx?: SxProps<Theme> }) {
  const footerText = useSiteStore(s => s.footerText)
  const legalEnabled = useSiteStore(s => s.legalEnabled)
  if (!footerText && !legalEnabled) return null
  return (
    <Box component={component ?? 'div'} sx={sx}>
      {footerText}
      {footerText && legalEnabled && ' · '}
      {legalEnabled && <LegalLinks />}
    </Box>
  )
}
