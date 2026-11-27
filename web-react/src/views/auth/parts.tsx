import type { ReactNode } from 'react'
import { Link as RouterLink } from 'react-router'
import { useTranslation } from 'react-i18next'

import { IconArrowLeft, IconError, IconIssue, IconSuccess } from '@/lib/icons'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Separator } from '@/components/ui/separator'

/**
 * The one message box of the pages outside the shell. The tone is carried by the icon
 * and the words as well as the colour (never colour alone): `error` for a refusal,
 * `warning` for a lockout, `success` for a completed step. Always role=alert, so a
 * message that appears after a submit is announced.
 */
export function FormAlert({ tone, children }: { tone: 'error' | 'warning' | 'success'; children: ReactNode }) {
  const Icon = tone === 'success' ? IconSuccess : tone === 'warning' ? IconIssue : IconError
  return (
    <Alert variant={tone === 'error' ? 'destructive' : 'default'}>
      <Icon aria-hidden />
      <AlertDescription>{children}</AlertDescription>
    </Alert>
  )
}

/** The way back to the sign-in page, as a link (it is navigation, not an action of the form). */
export function BackToLogin() {
  const { t } = useTranslation('auth')
  return (
    <div className="text-center">
      <RouterLink
        to="/login"
        className="inline-flex min-h-11 items-center gap-1 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline sm:min-h-0"
      >
        <IconArrowLeft aria-hidden className="size-3.5" />{t('back_to_login')}
      </RouterLink>
    </div>
  )
}

/**
 * After a failed submit, moves focus to the first field the page marked invalid, so a
 * keyboard or screen-reader user lands on the problem instead of hunting for it.
 * Deferred one tick: the invalid marks are set by the render the failed submit causes.
 */
export function focusFirstInvalid(form: HTMLFormElement | null): void {
  setTimeout(() => form?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus(), 0)
}

/** The "or" between two ways of doing the same thing (SSO and the local form). Decorative: both sides are labelled buttons. */
export function OrDivider() {
  const { t } = useTranslation('auth')
  return (
    <div aria-hidden className="flex items-center gap-3 text-xs text-muted-foreground">
      <Separator className="flex-1" />
      {t('or', { defaultValue: '或' })}
      <Separator className="flex-1" />
    </div>
  )
}
