import { useEffect } from 'react'
import { PageHeader } from '@/components/common/PageHeader'
import { ErrorState } from '@/components/feedback/ErrorState'
import { PageSkeleton } from '@/components/feedback/LoadingState'
import { pushSnack } from '@/components/feedback/toasts'
import { panelPath } from '@/panelPath'
import { useUISettings } from '@/query/settings'
import { useQueryScope } from '@/query/useQueryScope'
import { useAdminT } from './useAdminT'
import { SettingsPage } from './settings/SettingsPage'
import { UiDataProvider } from './settings/useUiSection'

/**
 * System settings: everything that is not in config.yaml. The page reads the panel-wide record once and hands it to
 * its sections; mail, SAML and OIDC are separate records read by their own sections (see ./settings).
 */
export default function SettingsView() {
  const t = useAdminT()
  const scope = useQueryScope()
  const query = useUISettings(scope)
  // Read fresh when the page opens: a cached copy up to minutes old is only a starting point for a draft.
  useEffect(() => { void query.refetch() }, []) // eslint-disable-line react-hooks/exhaustive-deps
  // Right after a panel path change this page is reloaded at the new address: remind the admin about the IdP callbacks once.
  const loaded = !!query.data
  useEffect(() => {
    if (!loaded) return
    const changed = sessionStorage.getItem('psp_panel_path_changed')
    if (changed !== null && changed === (panelPath || '/')) {
      sessionStorage.removeItem('psp_panel_path_changed')
      pushSnack(t('settings.panel_path_sso_notice'), 'warning')
    }
  }, [loaded]) // eslint-disable-line react-hooks/exhaustive-deps

  // A failed read is not an empty settings page.
  if (query.isError && !query.data) {
    return <div className="p-4 sm:p-6"><PageHeader title={t('settings.title')} /><ErrorState title={t('settings.load_failed')} reason={query.error} retry={() => query.refetch()} /></div>
  }
  if (!query.data) return <div className="p-4 sm:p-6"><PageHeader title={t('settings.title')} /><PageSkeleton /></div>
  return <UiDataProvider raw={query.data}><SettingsPage /></UiDataProvider>
}
