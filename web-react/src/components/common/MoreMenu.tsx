import type { ComponentType, ReactNode } from 'react'
import { Fragment } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { IconMore } from '@/lib/icons'

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { IconButton } from './IconButton'

export interface MoreMenuItem {
  id: string
  label: string
  icon?: ComponentType<{ className?: string }>
  /** Runs on select. Either this or `to`. */
  onSelect?: () => void
  /** Opens a route instead (a real link, so it can open in a new tab). */
  to?: string
  disabled?: boolean
  /** A destructive item: drawn in the danger colour and set apart below a divider. */
  destructive?: boolean
}

/**
 * The standard "更多" menu for a row or a page header: everything that is not the
 * one or two actions worth a button. Destructive items always come last, below a
 * divider, so a delete is never next to a harmless action (plan 3.1).
 * `children` are extra items rendered before the destructive group.
 */
export function MoreMenu({ items, label, size = 'icon-sm', align = 'end', children }: {
  items: MoreMenuItem[]
  /** The accessible name and tooltip; defaults to "更多". */
  label?: string
  size?: 'icon' | 'icon-sm' | 'icon-xs'
  align?: 'start' | 'center' | 'end'
  children?: ReactNode
}) {
  const { t } = useTranslation('common')
  const normal = items.filter(i => !i.destructive)
  const danger = items.filter(i => i.destructive)
  const row = (i: MoreMenuItem) => {
    const Icon = i.icon
    const inner = <>{Icon && <Icon />}{i.label}</>
    return i.to ? (
      <DropdownMenuItem key={i.id} disabled={i.disabled} render={<Link to={i.to} />}>{inner}</DropdownMenuItem>
    ) : (
      <DropdownMenuItem key={i.id} disabled={i.disabled} variant={i.destructive ? 'destructive' : 'default'} onClick={i.onSelect}>{inner}</DropdownMenuItem>
    )
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<IconButton label={label ?? t('ui.shell.more')} size={size} />}>
        <IconMore />
      </DropdownMenuTrigger>
      <DropdownMenuContent align={align} className="w-auto min-w-40">
        {normal.map(row)}
        {children}
        {danger.length > 0 && (
          <Fragment>
            {(normal.length > 0 || children) && <DropdownMenuSeparator />}
            {danger.map(row)}
          </Fragment>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
