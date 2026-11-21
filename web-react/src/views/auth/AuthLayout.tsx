import type { ReactNode } from 'react'

import SiteFooter from '@/components/SiteFooter'
import { LanguageMenu } from '@/components/shell/LanguageMenu'
import { ThemeMenu } from '@/components/shell/ThemeMenu'
import { BrandLogo } from '@/components/shell/BrandMark'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Card, CardContent } from '@/components/ui/card'
import { useBrand, useSiteBootstrap } from '@/router/shellBootstrap'
import { cn } from '@/lib/utils'

/**
 * THE FRAME OF EVERY PAGE OUTSIDE THE APP SHELL (sign-in, registration, password reset,
 * 2FA enrolment, the SSO result pages, signed-out): a card centred on the page background
 * with the site's logo, the language and appearance menus in the corner, and the footer
 * (site text and the terms links) at the bottom.
 *
 * It loads the site branding and applies the site's theme colour itself, so a page using it
 * does not repeat that. The tooltip provider is here because the two menus are icon
 * buttons with tooltips and no shell is mounted around these pages to supply one.
 *
 * `icon` is for the status pages (signed out, SSO failed): a round mark above the title.
 * `wide` is for a page that holds a few actions side by side rather than a form.
 */
export function AuthLayout({ title, subtitle, icon, wide, children }: {
  title: string
  subtitle?: ReactNode
  icon?: ReactNode
  wide?: boolean
  children?: ReactNode
}) {
  const brand = useBrand()
  useSiteBootstrap()
  return (
    <TooltipProvider delay={300}>
      <div className="flex min-h-svh flex-col bg-background text-foreground">
        <div className="flex justify-end gap-1 p-3">
          <LanguageMenu />
          <ThemeMenu />
        </div>
        <main className="flex flex-1 items-center justify-center px-4 py-4">
          <Card className={cn('w-full [--card-spacing:--spacing(6)] sm:[--card-spacing:--spacing(8)]', wide ? 'max-w-md' : 'max-w-sm')}>
            <CardContent className="flex flex-col gap-6">
              <div className="flex flex-col items-center gap-1.5 text-center">
                {icon
                  ? <div aria-hidden className="mb-1 flex size-14 items-center justify-center rounded-full bg-muted text-muted-foreground [&_svg]:size-7">{icon}</div>
                  : <BrandLogo brand={brand} className="mb-2 h-10 max-w-48" />}
                <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
                {subtitle && <p className="text-sm text-muted-foreground">{subtitle}</p>}
              </div>
              {children}
            </CardContent>
          </Card>
        </main>
        <SiteFooter component="footer" className="px-4 py-4 text-center text-xs text-muted-foreground" />
      </div>
    </TooltipProvider>
  )
}
