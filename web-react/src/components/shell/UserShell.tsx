import { Suspense, type ReactNode } from 'react'
import { Outlet } from 'react-router'

import { PageSkeleton } from '@/components/feedback/LoadingState'
import { TooltipProvider } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'
import { BrandLogo, type Brand } from './BrandMark'
import { MAIN_ID, SkipLink } from './SkipLink'
import { ToastLogButton } from './ToastLogButton'
import { useLogInMenu } from './ToastLogMenuItem'
import { UserMenu, type ShellUser } from './UserMenu'

/**
 * THE USER-SIDE SHELL: no sidebar, a top bar and the content, designed for a phone first.
 * A signed-in subscriber has one page, so a navigation rail would be empty weight; the
 * brand, the few top-bar controls and the account menu are all there is. The content
 * column is narrow and centred on a desktop, and fills the width on a phone.
 */
export function UserShell({ brand, user, topBarActions, footer, contentClassName, children }: {
  brand: Brand
  user: ShellUser
  topBarActions?: ReactNode
  /** Below the content (the site footer, legal links). */
  footer?: ReactNode
  contentClassName?: string
  children?: ReactNode
}) {
  const logInMenu = useLogInMenu()
  return (
    <TooltipProvider delay={300}>
      <div className="flex min-h-svh flex-col bg-background">
        <SkipLink />
        <header className="sticky top-0 z-30 flex h-(--shell-bar-h) shrink-0 items-center gap-1 border-b bg-background/85 px-2 backdrop-blur sm:gap-2 sm:px-4">
          <div className="flex min-w-0 flex-1 items-center gap-2.5">
            <BrandLogo brand={brand} />
            <span className="truncate text-sm font-semibold">{brand.name}</span>
          </div>
          <div className="flex shrink-0 items-center gap-0.5">
            {!logInMenu && <ToastLogButton />}
            {topBarActions}
            <UserMenu user={user} logInMenu={logInMenu} />
          </div>
        </header>
        <main id={MAIN_ID} tabIndex={-1} className={cn('mx-auto w-full max-w-3xl flex-1 p-4 outline-none sm:p-6', contentClassName)}>
          <Suspense fallback={<PageSkeleton />}>
            {children ?? <Outlet />}
          </Suspense>
        </main>
        {footer}
      </div>
    </TooltipProvider>
  )
}
