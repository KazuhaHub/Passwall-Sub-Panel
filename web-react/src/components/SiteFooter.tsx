import { Fragment, type ElementType } from 'react'
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
        <RouterLink
          to={`/legal/${kind}`}
          className="inline-flex min-h-11 items-center px-1 underline-offset-4 hover:underline focus-visible:underline sm:min-h-0"
        >{t(`legal.kind.${kind}`)}</RouterLink>
      </Fragment>
    ))}
  </>
}

/**
 * The page footer of the pages outside the shell and of the portal: the site's footer
 * text, then the terms and privacy links when the feature is on. It renders when
 * either exists, so a site with no footer text still carries the links. The caller
 * styles the frame through `className` (text size, colour, border).
 */
export default function SiteFooter({ component, className }: { component?: ElementType; className?: string }) {
  const footerText = useSiteStore(s => s.footerText)
  const legalEnabled = useSiteStore(s => s.legalEnabled)
  if (!footerText && !legalEnabled) return null
  const Tag = component ?? 'div'
  return (
    <Tag className={className}>
      {footerText}
      {footerText && legalEnabled && ' · '}
      {legalEnabled && <LegalLinks />}
    </Tag>
  )
}
