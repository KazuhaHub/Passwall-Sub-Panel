import type { ComponentProps, ReactNode } from 'react'
import { IconSpinner } from '@/lib/icons'
import { Button } from '@/components/ui/button'

/**
 * A button whose wait is the FLOW'S state, not the click's promise. The installation requests are aborted when the
 * choice changes; AsyncButton would stay busy until the aborted promise settled, so a button the flow has already
 * released would still look locked.
 */
export function BusyButton({ busy, icon, children, disabled, ...rest }: Omit<ComponentProps<typeof Button>, 'onClick'> & { busy: boolean; icon?: ReactNode; onClick: () => void }) {
  return (
    <Button {...rest} disabled={disabled || busy} aria-busy={busy || undefined}>
      {busy ? <IconSpinner aria-hidden className="animate-spin" /> : icon}
      {children}
    </Button>
  )
}
