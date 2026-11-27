import { useEffect, useMemo } from 'react'

import { LanguageMenu } from '@/components/shell/LanguageMenu'
import { NotificationBell } from '@/components/shell/NotificationBell'
import { ThemeMenu } from '@/components/shell/ThemeMenu'
import { AppShell } from '@/components/shell/AppShell'
import { ADMIN_NAV } from '@/components/shell/adminNav'
import { visibleItems } from '@/components/shell/nav'
import { useVersionInfo } from '@/components/shell/useVersionInfo'
import { prefetchView, prefetchViewsWhenIdle } from '@/router/prefetch'
import { selectLabel, useAuthStore } from '@/stores/auth'
import { useBrand, useSiteBootstrap } from './shellBootstrap'

// The bell, language and appearance menus in the order the top bar has always had them;
// the log button and the account menu are the shell's own.
const TOP_BAR_ACTIONS = <><NotificationBell /><LanguageMenu /><ThemeMenu /></>

/**
 * THE ADMIN LAYOUT ROUTE: mounts the shell around the /admin pages. Everything the shell
 * is given (navigation, who is signed in, the brand, the version) is decided here, so the
 * shell stays free of the router's route table and of the stores.
 */
export default function AdminShellRoute() {
  // Per-field subscriptions, not the whole store: an unrelated change (the appearance
  // density, say) must not re-render the whole admin chrome.
  const role = useAuthStore(s => s.role)
  const label = useAuthStore(selectLabel)
  const logout = useAuthStore(s => s.logout)
  const brand = useBrand()
  const version = useVersionInfo()
  useSiteBootstrap()

  // WARM THE VIEWS THIS ROLE CAN OPEN while the browser is idle, one chunk at a time, so a
  // later click renders at once instead of waiting on the network. The items under the
  // pointer or focus are warmed immediately (onNavIntent); this covers the ones nobody has
  // pointed at yet. The role filter is the sidebar's own, so an operator never fetches the
  // chunks of pages they cannot open.
  useEffect(() => prefetchViewsWhenIdle(visibleItems(ADMIN_NAV, role).map(item => item.to)), [role])

  const user = useMemo(() => ({ label, role, onLogout: logout }), [label, role, logout])

  return (
    <AppShell
      nav={ADMIN_NAV}
      role={role}
      brand={brand}
      user={user}
      version={version}
      onNavIntent={prefetchView}
      topBarActions={TOP_BAR_ACTIONS}
      // Every page still carries its own padding and its own width (it did under the old
      // layout, which gave the content area none). Giving the shell's defaults on top of
      // that would pad twice and cap wide screens at the shell's max width.
      contentClassName="p-0 sm:p-0 max-w-none"
    />
  )
}
