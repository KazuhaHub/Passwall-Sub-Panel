import { useEffect, useRef, useState, type MouseEvent, type ReactNode } from 'react'
import { Button, CircularProgress, IconButton, type ButtonProps, type IconButtonProps } from '@mui/material'

// BUTTONS THAT SHOW THEIR OWN WAIT.
//
// On a slow link a row action (delete, renew, reset, retry) answered seconds
// after the click, and until then the icon sat unchanged and clickable, so it
// read as a dead button and invited a second, duplicate request. These buttons
// track the promise their handler returns: while it is pending the button is
// disabled, marked aria-busy, and its icon becomes a spinner; further clicks are
// ignored. A handler that returns nothing behaves as a plain button.
//
// A rejected action only ends the wait. The failure itself is the caller's to
// report, and the shared client already toasts request errors.

type Handler = (event: MouseEvent<HTMLButtonElement>) => unknown

function isThenable(value: unknown): value is PromiseLike<unknown> {
  return typeof (value as PromiseLike<unknown> | null)?.then === 'function'
}

function usePendingClick(onClick: Handler | undefined) {
  const [pending, setPending] = useState(false)
  // A ref as well as state: two clicks inside one frame both see the state
  // from before the first one re-rendered.
  const running = useRef(false)
  const mounted = useRef(true)
  useEffect(() => () => { mounted.current = false }, [])

  async function handle(event: MouseEvent<HTMLButtonElement>) {
    if (running.current || !onClick) return
    const result = onClick(event)
    if (!isThenable(result)) return
    running.current = true
    setPending(true)
    try {
      await result
    } catch {
      // Reported by whoever started the action; see the note above.
    } finally {
      running.current = false
      if (mounted.current) setPending(false)
    }
  }
  return { pending, handle }
}

export type AsyncIconButtonProps = Omit<IconButtonProps, 'onClick'> & {
  onClick?: Handler
  /** Show progress from outside as well, e.g. a query that is refetching. */
  pending?: boolean
}

export function AsyncIconButton({ onClick, pending: external = false, disabled, children, size, ...rest }: AsyncIconButtonProps) {
  const { pending, handle } = usePendingClick(onClick)
  const busy = pending || external
  return <IconButton {...rest} size={size} disabled={disabled || busy} aria-busy={busy || undefined} onClick={handle}>
    {busy ? <CircularProgress size={size === 'small' ? 18 : 20} color="inherit" /> : children}
  </IconButton>
}

export type AsyncButtonProps = Omit<ButtonProps, 'onClick'> & {
  onClick?: Handler
  /** Show progress from outside as well, e.g. a query that is refetching. */
  pending?: boolean
  children?: ReactNode
}

export function AsyncButton({ onClick, pending: external = false, disabled, startIcon, children, ...rest }: AsyncButtonProps) {
  const { pending, handle } = usePendingClick(onClick)
  const busy = pending || external
  return <Button {...rest} disabled={disabled || busy} aria-busy={busy || undefined} onClick={handle}
    startIcon={busy ? <CircularProgress size={16} color="inherit" /> : startIcon}>
    {children}
  </Button>
}
