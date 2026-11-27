import { Navigate, useLocation, useSearchParams } from 'react-router'
import { HelpTip } from '@/components/common/HelpTip'
import { PageHeader } from '@/components/common/PageHeader'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import UserAutocomplete from '@/components/UserAutocomplete'
import { useCan } from '@/utils/permissions'
import RiskUserDialog from './dialog/RiskUserDialog'
import { useDrawerParam } from './drawerParam'
import LiveConnectionsTab from './LiveConnectionsTab'
import PolicyTab from './policy/PolicyTab'
import QueueTab from './queue/QueueTab'
import RecordsTab from './RecordsTab'
import { legacyRedirect, parseRiskTab, type RiskTab } from './riskParams'
import { useRiskT } from './useRiskT'

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
  const { t } = useRiskT()
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
    <div className="p-4 sm:p-6">
      <PageHeader title={t('admin:risk_center.title')} description={t('admin:risk_center.subtitle')}
        // Any account, from any tab: the picker opens the drawer, whether or not the account is on the
        // list in front of the admin.
        secondary={<UserAutocomplete value={null} onChange={id => { if (id) drawer.open(id) }}
          label={t('admin:risk_center.pick_user')} />} />
      <Tabs value={tab} onValueChange={v => setTab(v as RiskTab)} className="gap-3">
        <div className="flex items-center justify-between gap-2">
          <div className="min-w-0 overflow-x-auto">
            <TabsList>
              <TabsTrigger value="queue">{t('admin:risk_center.tab_queue')}</TabsTrigger>
              <TabsTrigger value="live">{t('admin:risk_center.tab_live')}</TabsTrigger>
              <TabsTrigger value="records">{t('admin:risk_center.tab_records')}</TabsTrigger>
              <TabsTrigger value="policy">{t('admin:risk_center.tab_policy')}</TabsTrigger>
            </TabsList>
          </div>
          <HelpTip text={t(HELP_KEY[tab])} />
        </div>
        {/* Only the open tab mounts, so only its lists are read. */}
        <TabsContent value="queue">
          <QueueTab onOpenUser={drawer.open} onOpenLive={() => setTab('live')} onOpenPolicy={() => setTab('policy')} />
        </TabsContent>
        <TabsContent value="live"><LiveConnectionsTab onOpenUser={drawer.open} /></TabsContent>
        <TabsContent value="records"><RecordsTab onOpenUser={drawer.open} /></TabsContent>
        <TabsContent value="policy"><PolicyTab /></TabsContent>
      </Tabs>
      <RiskUserDialog userId={drawer.id} onClose={drawer.close} host="risk" />
    </div>
  )
}
