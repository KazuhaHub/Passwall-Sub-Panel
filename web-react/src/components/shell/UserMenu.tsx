import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import type { Role } from '@/api/types'
import { IconLogout } from '@/lib/icons'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { IconButton } from '@/components/common/IconButton'
import { ToastLogMenuItem } from './ToastLogMenuItem'

export interface ShellUser {
  /** The display name or login name. */
  label: string
  role: Role | ''
  onLogout: () => void
  /** Items between the identity and the sign-out (a profile page, say). */
  items?: ReactNode
}

/** The avatar button and its menu: who is signed in, their role, and signing out. */
export function UserMenu({ user, logInMenu = false }: { user: ShellUser; /** Show the message log as a menu item (the bar has no room for its button). */ logInMenu?: boolean }) {
  const { t } = useTranslation('common')
  const role = user.role ? t(`ui.shell.role.${user.role}`) : ''
  const initial = (user.label.trim()[0] ?? '?').toUpperCase()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<IconButton label={`${t('ui.shell.user_menu')}: ${user.label}`} tooltip={user.label} size="icon" className="rounded-full" data-psp-account-menu="" />}
      >
        <span className="flex size-7 items-center justify-center rounded-full bg-brand text-xs font-semibold text-brand-foreground">{initial}</span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuGroup>
          <DropdownMenuLabel className="space-y-0.5">
            <span className="block truncate text-sm font-medium text-foreground">{user.label}</span>
            {role && <span className="block text-xs font-normal">{role}</span>}
          </DropdownMenuLabel>
        </DropdownMenuGroup>
        {(user.items || logInMenu) && <><DropdownMenuSeparator />{logInMenu && <ToastLogMenuItem />}{user.items}</>}
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={user.onLogout}>
          <IconLogout />
          {t('auth.logout')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
