import { Box, Tab, Tabs, useTheme } from '@mui/material'
import { Navigate, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'
import PageHeader from '@/components/PageHeader'
import { useCan } from '@/utils/permissions'
import GeoAnomaliesTab from './GeoAnomaliesTab'
import RiskSignalsTab from './RiskSignalsTab'

const TABS = ['geo', 'risk'] as const
type RiskTab = (typeof TABS)[number]
const DEFAULT_TAB: RiskTab = 'geo'

function parseTab(raw: string | null): RiskTab {
  return (TABS as readonly string[]).includes(raw ?? '') ? raw as RiskTab : DEFAULT_TAB
}

/**
 * THE RISK CENTER: one admin-only page for everything the panel observes about
 * who is using an account.
 *
 * The location and risk tabs used to sit on the Logs page, gated per tab. They
 * moved here because they are not logs — they are verdicts about people, read
 * by the one role their endpoints admit — and a page of their own can be gated
 * whole: the `risk.view` capability, ADMIN_ONLY_ROUTES and the nav item's
 * adminOnly flag all name it.
 *
 * The URL owns the tab. `useTabParam` is not used because it drops the default
 * from the URL; here every switch writes `tab` explicitly, so a copied link, a
 * bell entry (`?tab=geo`) or an old Logs link sent on by LogsRoute means the
 * same tab whatever the page's default is.
 */
export default function RiskCenterView() {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const canView = useCan('risk.view')
  const [params, setParams] = useSearchParams()
  const tab = parseTab(params.get('tab'))

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

  return (
    <Box sx={{ p: 3 }}>
      <PageHeader title={t('admin:risk_center.title')} subtitle={t('admin:risk_center.subtitle')} />
      <Tabs value={tab} onChange={(_, v: RiskTab) => setTab(v)}
        sx={{ mb: 2, borderBottom: `1px solid ${md.outlineVariant}` }}>
        <Tab value="geo" label={t('admin:risk_center.tab_geo')} />
        <Tab value="risk" label={t('admin:risk_center.tab_risk')} />
      </Tabs>
      {/* Only the open tab mounts, so only its list is read. */}
      {tab === 'geo' && <GeoAnomaliesTab />}
      {/* Each risk row carries the account's concurrent-location verdict;
          its chip opens that tab, where the evidence behind it is. */}
      {tab === 'risk' && <RiskSignalsTab onOpenGeo={() => setTab('geo')} />}
    </Box>
  )
}
