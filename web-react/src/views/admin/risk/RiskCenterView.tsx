import { Box, Tab, Tabs, useTheme } from '@mui/material'
import { Navigate, useLocation, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'
import PageHeader from '@/components/PageHeader'
import UserAutocomplete from '@/components/UserAutocomplete'
import { useCan } from '@/utils/permissions'
import RiskUserDrawer from './drawer/RiskUserDrawer'
import { useDrawerParam } from '@/hooks/useDrawerParam'
import HelpTip from '@/components/HelpTip'
import LiveConnectionsTab from './LiveConnectionsTab'
import PolicyTab from './policy/PolicyTab'
import QueueTab from './queue/QueueTab'
import RecordsTab from './RecordsTab'
import { legacyRedirect, parseRiskTab, type RiskTab } from './riskParams'

const HELP_KEY: Record<RiskTab, string> = {
  queue: 'admin:risk_center.help.queue',
  live: 'admin:risk_center.help.live',
  records: 'admin:risk_center.help.records',
  policy: 'admin:risk_center.help.policy',
}

/**
 * THE RISK CENTER: one admin-only page for what the panel observes about who
 * is using an account — the accounts that need a look (待处理), who is
 * connected now (在线), the record of every change (记录) and what the
 * detectors judge by (策略) — with one drawer, over any tab, for everything
 * about one account.
 *
 * The location and risk verdicts used to sit on the Logs page, gated per
 * tab. They moved here because they are not logs — they are verdicts about
 * people, read by the one role their endpoints admit — and a page of their
 * own can be gated whole: the `risk.view` capability, ADMIN_ONLY_ROUTES and
 * the nav item's adminOnly flag all name it.
 *
 * The URL owns the tab, every tab's filters (riskParams) and the drawer's
 * account (`user=`, useDrawerParam). `useTabParam` is not used because it
 * drops the default from the URL; here every switch writes `tab` explicitly,
 * so a copied link, the bell's entry (`?tab=queue&urgent=1`) or an old Logs
 * link sent on by LogsRoute means the same tab whatever the page's default
 * is. Links written for the old five tabs are rewritten before any tab
 * mounts (legacyRedirect).
 */
export default function RiskCenterView() {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const canView = useCan('risk.view')
  const location = useLocation()
  const [params, setParams] = useSearchParams()
  const tab = parseRiskTab(params.get('tab'))
  // The drawer's account (?user=), over whichever tab is open. Opening an
  // account from a row or the picker is a drill-down: it PUSHES `user=`
  // (every other param kept) so Back closes it and the list underneath is
  // exactly as it was.
  const drawer = useDrawerParam('user')

  // Replace, not push, like every other tabbed page: a tab switch is a view
  // of the same page, and Back should leave the page rather than step through
  // the tabs visited on it.
  const setTab = (next: RiskTab) => setParams(prev => {
    const out = new URLSearchParams(prev)
    out.set('tab', next)
    return out
  }, { replace: true })

  // After the hooks, so they run in the same order on every render. The route
  // is admin-only already (RequireAuth bounces an operator on
  // ADMIN_ONLY_ROUTES); this is the page's own check, so it never asks an
  // adminGroup endpoint for an answer that can only be 403.
  if (!canView) return <Navigate to="/admin/dashboard" replace />

  // An old link lands on the tab that answers it now, in one replace and
  // before any tab mounts: nothing is read with the old link's meaning.
  const redirect = legacyRedirect(params)
  if (redirect !== null) return <Navigate to={{ pathname: location.pathname, search: redirect }} replace />

  return (
    <Box sx={{ p: 3 }}>
      <PageHeader title={t('admin:risk_center.title')} subtitle={t('admin:risk_center.subtitle')}
        actions={
          // Any account, from any tab: the picker opens the drawer, whether or
          // not the account is on the list in front of the admin.
          <UserAutocomplete value={null} onChange={id => { if (id) drawer.open(id) }}
            label={t('admin:risk_center.pick_user')} width={260} />
        } />
      <Box sx={{ display: 'flex', alignItems: 'stretch', mb: 2, borderBottom: `1px solid ${md.outlineVariant}` }}>
        <Tabs value={tab} onChange={(_, v: RiskTab) => setTab(v)} variant="scrollable" allowScrollButtonsMobile
          sx={{ flex: 1, minWidth: 0 }}>
          <Tab value="queue" label={t('admin:risk_center.tab_queue')} />
          <Tab value="live" label={t('admin:risk_center.tab_live')} />
          <Tab value="records" label={t('admin:risk_center.tab_records')} />
          <Tab value="policy" label={t('admin:risk_center.tab_policy')} />
        </Tabs>
        <HelpTip textKey={HELP_KEY[tab]} />
      </Box>
      {/* Only the open tab mounts, so only its lists are read. */}
      {tab === 'queue' && (
        <QueueTab onOpenUser={drawer.open} onOpenLive={() => setTab('live')} onOpenPolicy={() => setTab('policy')} />
      )}
      {tab === 'live' && (
        <LiveConnectionsTab onOpenUser={drawer.open} />
      )}
      {tab === 'records' && (
        <RecordsTab onOpenUser={drawer.open} />
      )}
      {tab === 'policy' && (
        <PolicyTab />
      )}
      <RiskUserDrawer userId={drawer.id} onClose={drawer.close} host="risk" />
    </Box>
  )
}
