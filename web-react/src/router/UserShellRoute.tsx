import { useMemo } from 'react'

import ConsentDialog from '@/components/ConsentDialog'
import SiteFooter from '@/components/SiteFooter'
import { LanguageMenu } from '@/components/shell/LanguageMenu'
import { ThemeMenu } from '@/components/shell/ThemeMenu'
import { UserShell } from '@/components/shell/UserShell'
import { selectLabel, useAuthStore } from '@/stores/auth'
import { useBrand, useSiteBootstrap } from './shellBootstrap'

const TOP_BAR_ACTIONS = <><LanguageMenu /><ThemeMenu /></>

// The site footer is the existing component (it also closes the login and register pages);
// only its frame is styled here, with theme-token colours so it follows light and dark.
const FOOTER = (
  <SiteFooter
    component="footer"
    sx={{ py: 1.25, textAlign: 'center', fontSize: 11, color: 'text.secondary', borderTop: 1, borderColor: 'divider', bgcolor: 'background.paper' }}
  />
)

/**
 * THE USER LAYOUT ROUTE: mounts the user shell around the portal pages, with the consent
 * dialog (asks an account of role user to accept the current terms while it is pending)
 * and the site footer around the page.
 */
export default function UserShellRoute() {
  const role = useAuthStore(s => s.role)
  const label = useAuthStore(selectLabel)
  const logout = useAuthStore(s => s.logout)
  const brand = useBrand()
  useSiteBootstrap()

  const user = useMemo(() => ({ label, role, onLogout: logout }), [label, role, logout])

  return (
    <>
      <UserShell
        brand={brand}
        user={user}
        topBarActions={TOP_BAR_ACTIONS}
        footer={FOOTER}
        // MeView centres itself at 1200px with its own padding, as it did under the old layout.
        contentClassName="p-0 sm:p-0 max-w-none"
      />
      <ConsentDialog />
    </>
  )
}
