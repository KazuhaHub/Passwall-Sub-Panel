import { Fragment, Suspense, useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link, Outlet, useLocation, useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import type { Role } from '@/api/types'
import type { VersionInfo } from '@/api/version'
import { IconSearch } from '@/lib/icons'
import { IconButton } from '@/components/common/IconButton'
import { PageSkeleton } from '@/components/feedback/LoadingState'
import { setSyncPendingNavigator } from '@/components/feedback/syncPendingToast'
import { Button } from '@/components/ui/button'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarSeparator,
  SidebarTrigger,
  useSidebar,
} from '@/components/ui/sidebar'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'
import { BrandIcon, BrandLogo, type Brand } from './BrandMark'
import { CommandPalette, useCommandPaletteHotkey } from './CommandPalette'
import { navigationProvider, type CommandProvider } from './commands'
import { activeItem, isNavActive, visibleSections, type NavSection } from './nav'
import { ToastLogButton } from './ToastLogButton'
import { UserMenu, type ShellUser } from './UserMenu'

export interface AppShellProps {
  /** The navigation model: sections of items. The shell never reads a route table. */
  nav: readonly NavSection[]
  role: Role | ''
  brand: Brand
  user: ShellUser
  /** Top-bar controls before the user menu (bell, language, theme). */
  topBarActions?: ReactNode
  /** Build identity for the sidebar footer; null or omitted shows nothing. */
  version?: VersionInfo | null
  /** Called when the pointer, keyboard focus or a touch reaches a nav item, before the click. */
  onNavIntent?: (to: string) => void
  /** More command-palette providers (searches, actions). The navigation one is built in. */
  commandProviders?: readonly CommandProvider[]
  /** Class for the content area: pages that carry their own padding pass `p-0`. */
  contentClassName?: string
  /** What fills the content area; the router's outlet by default. */
  children?: ReactNode
}

// The mobile sheet and the desktop rail are two presentations of one sidebar; this
// closes the sheet whenever the route changes, so a tap on a link both navigates and
// gets the menu out of the way.
function CloseMobileOnNavigate() {
  const { setOpenMobile } = useSidebar()
  const { pathname } = useLocation()
  useEffect(() => setOpenMobile(false), [pathname, setOpenMobile])
  return null
}

function VersionFooter({ version }: { version: VersionInfo }) {
  const { t } = useTranslation('common')
  const channel = ['stable', 'rc', 'beta', 'pending', 'testing'].includes(version.channel ?? '') ? version.channel : 'unknown'
  return (
    <Tooltip>
      <TooltipTrigger
        render={<span className="block truncate px-2 font-mono text-[11px] text-muted-foreground select-none group-data-[collapsible=icon]:hidden" />}
      >
        {version.version || 'dev'}
      </TooltipTrigger>
      <TooltipContent side="top" className="flex-col items-start gap-0.5 text-[11px]">
        <span>{version.version || 'dev'} · {t(`release.${channel}`)}</span>
        {version.commit && <span>commit: {version.commit}</span>}
        {version.build_date && <span>built: {version.build_date}</span>}
      </TooltipContent>
    </Tooltip>
  )
}

function AppSidebar({ nav, role, brand, version, onNavIntent }: Pick<AppShellProps, 'nav' | 'role' | 'brand' | 'version' | 'onNavIntent'>) {
  const { t } = useTranslation(['nav', 'common'])
  const { pathname } = useLocation()
  const sections = useMemo(() => visibleSections(nav, role), [nav, role])
  const current = useMemo(() => activeItem(nav, role, pathname), [nav, role, pathname])

  return (
    <Sidebar
      collapsible="icon"
      mobileTitle={t('common:ui.shell.sidebar_title')}
      mobileDescription={t('common:ui.shell.sidebar_description')}
    >
      <SidebarHeader className="h-14 flex-row items-center gap-2.5 border-b px-3 py-0 group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0">
        <span className="hidden group-data-[collapsible=icon]:block"><BrandIcon brand={brand} /></span>
        <span className="flex min-w-0 items-center gap-2.5 group-data-[collapsible=icon]:hidden">
          <BrandLogo brand={brand} />
          <span className="truncate text-sm font-semibold">{brand.name}</span>
        </span>
      </SidebarHeader>
      <SidebarContent aria-label={t('common:ui.shell.main_nav')} role="navigation">
        {sections.map((section, i) => (
          <Fragment key={section.labelKey ?? `top-${i}`}>
            {/* A divider opens every group after the first. On the expanded rail a
                labelled group has its subheader, so the divider is only drawn when there
                is none, or when the rail is collapsed to icons and the subheader is gone. */}
            {i > 0 && (
              <SidebarSeparator className={section.labelKey ? 'hidden group-data-[collapsible=icon]:block' : undefined} />
            )}
            <SidebarGroup className="py-1">
              {section.labelKey && <SidebarGroupLabel className="text-[11px] font-semibold tracking-wide uppercase">{t(section.labelKey)}</SidebarGroupLabel>}
              <SidebarGroupContent>
                <SidebarMenu>
                  {section.items.map(item => {
                    const Icon = item.icon
                    const label = t(item.labelKey)
                    const active = item.to === current?.to
                    return (
                      <SidebarMenuItem key={item.to}>
                        <SidebarMenuButton
                          isActive={active}
                          tooltip={label}
                          // Intent precedes the click by a few hundred milliseconds;
                          // the owner of the model starts fetching the view's chunk then.
                          onPointerEnter={() => onNavIntent?.(item.to)}
                          onFocus={() => onNavIntent?.(item.to)}
                          onTouchStart={() => onNavIntent?.(item.to)}
                          render={<Link to={item.to} aria-current={active ? 'page' : undefined} />}
                        >
                          <Icon />
                          <span>{label}</span>
                        </SidebarMenuButton>
                      </SidebarMenuItem>
                    )
                  })}
                </SidebarMenu>
              </SidebarGroupContent>
            </SidebarGroup>
          </Fragment>
        ))}
      </SidebarContent>
      {version && (
        <SidebarFooter className="border-t py-1.5">
          <VersionFooter version={version} />
        </SidebarFooter>
      )}
    </Sidebar>
  )
}

const IS_MAC = typeof navigator !== 'undefined' && /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent)

/** The search box in the top bar: a button that opens the palette, with its shortcut. */
function PaletteTrigger({ onOpen }: { onOpen: () => void }) {
  const { t } = useTranslation('common')
  return (
    <>
      <Button
        type="button"
        variant="outline"
        onClick={onOpen}
        aria-keyshortcuts={IS_MAC ? 'Meta+K' : 'Control+K'}
        className="hidden w-full max-w-64 justify-start gap-2 font-normal text-muted-foreground md:inline-flex"
      >
        <IconSearch aria-hidden />
        <span className="truncate">{t('ui.shell.search')}</span>
        <kbd className="ml-auto rounded border bg-muted px-1.5 font-mono text-[10px]">{IS_MAC ? '⌘K' : 'Ctrl K'}</kbd>
      </Button>
      <IconButton label={t('ui.shell.palette.open')} tooltip={`${t('ui.shell.palette.title')} (${IS_MAC ? '⌘K' : 'Ctrl K'})`} onClick={onOpen} className="md:hidden">
        <IconSearch />
      </IconButton>
    </>
  )
}

function TopBar({ title, onOpenPalette, actions, user }: { title?: string; onOpenPalette: () => void; actions?: ReactNode; user: ShellUser }) {
  const { t } = useTranslation('common')
  return (
    <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center gap-2 border-b bg-background/85 px-3 backdrop-blur sm:px-4">
      <SidebarTrigger label={t('ui.shell.toggle_sidebar')} />
      {title && <h2 className="min-w-0 truncate text-sm font-medium md:hidden">{title}</h2>}
      <div className="flex min-w-0 flex-1 items-center md:px-1">
        <PaletteTrigger onOpen={onOpenPalette} />
      </div>
      <div className="flex shrink-0 items-center gap-0.5">
        <ToastLogButton />
        {actions}
        <UserMenu user={user} />
      </div>
    </header>
  )
}

/**
 * THE ADMIN SHELL: a collapsible sidebar, a top bar and the content outlet. It is
 * data-driven: it is given the navigation model, the role, the brand and the user, and
 * slots for the top-bar controls, and draws them. It imports no route table and no
 * page, so changing what the app offers never touches it, and the role filter
 * (nav.ts) is the same one the command palette uses.
 *
 * Desktop: the sidebar collapses to an icon rail (tooltips carry the labels) and
 * remembers that; Ctrl/Cmd+B toggles it, except while typing. Phone width: the sidebar
 * is a sheet that closes when a link is followed. ⌘K / Ctrl+K opens the palette.
 */
export function AppShell({ nav, role, brand, user, topBarActions, version, onNavIntent, commandProviders, contentClassName, children }: AppShellProps) {
  const { t } = useTranslation(['nav', 'common'])
  const navigate = useNavigate()
  const { pathname } = useLocation()
  const [paletteOpen, setPaletteOpen] = useState(false)
  useCommandPaletteHotkey(paletteOpen, setPaletteOpen)

  // The sync-pending toast links to the sync tasks page through the router, not a page
  // load, and only a shell that can open that page registers the link.
  useEffect(() => setSyncPendingNavigator(navigate), [navigate])

  const providers = useMemo(() => [
    navigationProvider(nav, role, t, t('common:ui.shell.palette.pages')),
    ...(commandProviders ?? []),
  ], [nav, role, t, commandProviders])

  const go = useCallback((to: string) => { void navigate(to) }, [navigate])
  const current = activeItem(nav, role, pathname)

  return (
    <TooltipProvider delay={300}>
      <SidebarProvider>
        <a
          href="#main-content"
          className="sr-only z-50 rounded-md bg-background px-3 py-2 text-sm font-medium shadow-md ring-1 ring-foreground/10 focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
        >
          {t('common:ui.shell.skip_to_content')}
        </a>
        <CloseMobileOnNavigate />
        <AppSidebar nav={nav} role={role} brand={brand} version={version} onNavIntent={onNavIntent} />
        <SidebarInset className="min-w-0">
          <TopBar
            title={current ? t(current.labelKey) : undefined}
            onOpenPalette={() => setPaletteOpen(true)}
            actions={topBarActions}
            user={user}
          />
          <div id="main-content" tabIndex={-1} className={cn('mx-auto w-full max-w-[100rem] flex-1 p-4 outline-none sm:p-6', contentClassName)}>
            {/* Per-route Suspense boundary INSIDE the shell, so switching pages only swaps
                the content: an outer one would unmount the whole shell (and its
                mount-time effects) on every cross-section navigation. */}
            <Suspense fallback={<PageSkeleton />}>
              {children ?? <Outlet />}
            </Suspense>
          </div>
        </SidebarInset>
        <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} providers={providers} navigate={go} role={role} />
      </SidebarProvider>
    </TooltipProvider>
  )
}

// Re-exported so a layout needs one import for the whole shell vocabulary.
export { isNavActive }
