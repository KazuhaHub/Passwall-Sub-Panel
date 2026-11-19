import type { ComponentProps, ReactNode } from 'react'
import { IconSpinner } from '@/lib/icons'

import { Button } from '@/components/ui/button'
import { IconButton } from './IconButton'
import { usePendingClick, type ClickHandler } from './usePendingClick'

// The same pending semantics as components/AsyncButton.tsx (see usePendingClick),
// on the shadcn button. A separate file, not an edit of that one, because its
// callers are MUI pages that pass MUI props.

export type AsyncButtonProps = Omit<ComponentProps<typeof Button>, 'onClick'> & {
  onClick?: ClickHandler
  /** Show progress from outside as well, e.g. a query that is refetching. */
  pending?: boolean
  /** Leading icon; swapped for a spinner while the button is busy. */
  icon?: ReactNode
}

export function AsyncButton({ onClick, pending: external = false, disabled, icon, children, ...rest }: AsyncButtonProps) {
  const { pending, handle } = usePendingClick(onClick)
  const busy = pending || external
  return (
    <Button {...rest} disabled={disabled || busy} aria-busy={busy || undefined} onClick={handle}>
      {busy ? <IconSpinner aria-hidden className="animate-spin" /> : icon}
      {children}
    </Button>
  )
}

export type AsyncIconButtonProps = Omit<ComponentProps<typeof IconButton>, 'onClick'> & {
  onClick?: ClickHandler
  pending?: boolean
}

export function AsyncIconButton({ onClick, pending: external = false, disabled, children, ...rest }: AsyncIconButtonProps) {
  const { pending, handle } = usePendingClick(onClick)
  const busy = pending || external
  return (
    <IconButton {...rest} disabled={disabled || busy} aria-busy={busy || undefined} onClick={handle}>
      {busy ? <IconSpinner aria-hidden className="animate-spin" /> : children}
    </IconButton>
  )
}
