import { Box, Tab, Tabs, useTheme } from '@mui/material'
import { Navigate, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'
import PageHeader from '@/components/PageHeader'
import { useCan } from '@/utils/permissions'
import FlagRecordsTab from './FlagRecordsTab'
import GeoAnomaliesTab from './GeoAnomaliesTab'
import LiveConnectionsTab from './LiveConnectionsTab'
import RiskSignalsTab from './RiskSignalsTab'
import UserLookupTab from './UserLookupTab'

const TABS = ['connections', 'geo', 'risk', 'flags', 'user'] as const
type RiskTab = (typeof TABS)[number]
const DEFAULT_TAB: RiskTab = 'connections'

function parseTab(raw: string | null): RiskTab {
  return (TABS as readonly string[]).includes(raw ?? '') ? raw as RiskTab : DEFAULT_TAB
}

/** The looked-up account from ?id=, or null for anything that is not a
 *  positive integer: a malformed link asks for nobody rather than for #0. */
function parseId(raw: string | null): number | null {
  if (!raw || !/^\d+$/.test(raw)) return null
  const id = Number(raw)
  return Number.isSafeInteger(id) && id > 0 ? id : null
}

/**
 * THE RISK CENTER: one admin-only page for everything the panel observes about
 * who is using an account — who is connected now, the concurrent-location and
 * risk verdicts, the record of every flag, and one account's everything.
 *
 * The location and risk tabs used to sit on the Logs page, gated per tab. They
 * moved here because they are not logs — they are verdicts about people, read
 * by the one role their endpoints admit — and a page of their own can be gated
 * whole: the `risk.view` capability, ADMIN_ONLY_ROUTES and the nav item's
 * adminOnly flag all name it.
 *
 * The URL owns the tab and the looked-up account. `useTabParam` is not used
 * because it drops the default from the URL; here every switch writes `tab`
 * explicitly, so a copied link, a bell entry (`?tab=geo`) or an old Logs link
 * sent on by LogsRoute means the same tab whatever the page's default is.
 */
export default function RiskCenterView() {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const canView = useCan('risk.view')
  const [params, setParams] = useSearchParams()
  const tab = parseTab(params.get('tab'))
  const userId = parseId(params.get('id'))

  // Replace, not push, like every other tabbed page: a tab switch is a view
  // of the same page, and Back should leave the page rather than step through
  // the tabs visited on it.
  const setTab = (next: RiskTab) => setParams(prev => {
    const out = new URLSearchParams(prev)
    out.set('tab', next)
    return out
  }, { replace: true })

  // Opening an account from a row is a drill-down, so it PUSHES: Back returns
  // to the list it was opened from. Tab and id are ONE update — two would
  // pass through a URL naming the lookup with no account (or an account on
  // the tab it came from), and the second write could read a stale first.
  const openUser = (id: number) => setParams({ tab: 'user', id: String(id) })

  // Picking another account inside the lookup is the same view, like a tab
  // switch: it replaces.
  const pickUser = (id: number | null) => setParams(
    id ? { tab: 'user', id: String(id) } : { tab: 'user' }, { replace: true })

  // After the hooks, so they run in the same order on every render. The route
  // is admin-only already (RequireAuth bounces an operator on
  // ADMIN_ONLY_ROUTES); this is the page's own check, so it never asks an
  // adminGroup endpoint for an answer that can only be 403.
  if (!canView) return <Navigate to="/admin/dashboard" replace />

  return (
    <Box sx={{ p: 3 }}>
      <PageHeader title={t('admin:risk_center.title')} subtitle={t('admin:risk_center.subtitle')} />
      <Tabs value={tab} onChange={(_, v: RiskTab) => setTab(v)} variant="scrollable" allowScrollButtonsMobile
        sx={{ mb: 2, borderBottom: `1px solid ${md.outlineVariant}` }}>
        <Tab value="connections" label={t('admin:risk_center.tab_connections')} />
        <Tab value="geo" label={t('admin:risk_center.tab_geo')} />
        <Tab value="risk" label={t('admin:risk_center.tab_risk')} />
        <Tab value="flags" label={t('admin:risk_center.tab_flags')} />
        <Tab value="user" label={t('admin:risk_center.tab_user')} />
      </Tabs>
      {/* Only the open tab mounts, so only its lists are read. */}
      {tab === 'connections' && <LiveConnectionsTab onOpenUser={openUser} />}
      {tab === 'geo' && <GeoAnomaliesTab onOpenUser={openUser} />}
      {/* Each risk row carries the account's concurrent-location verdict;
          its chip opens that tab, where the evidence behind it is. */}
      {tab === 'risk' && <RiskSignalsTab onOpenGeo={() => setTab('geo')} onOpenUser={openUser} />}
      {tab === 'flags' && <FlagRecordsTab onOpenUser={openUser} />}
      {tab === 'user' && <UserLookupTab userId={userId} onPick={pickUser} />}
    </Box>
  )
}
