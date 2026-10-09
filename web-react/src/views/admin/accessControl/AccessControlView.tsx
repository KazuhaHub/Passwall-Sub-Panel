import { useRef, useState } from 'react'
import { Alert, Box, Button, Skeleton, Stack, Tab, Tabs } from '@mui/material'
import { Navigate, useLocation, useSearchParams } from 'react-router'
import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import HelpTip from '@/components/HelpTip'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { useCan } from '@/utils/permissions'
import { accessVerdict, type NodeFilter } from '@/utils/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { scopeKey, type QueryScope } from '@/query/session'
import { useAccessControlSettings, useDestinationPolicies, useDestinationPublication, useDestinationStatus } from '@/query/accessControl'
import { useDrawerParam } from '@/hooks/useDrawerParam'
import { destinationError } from './errors'
import { pauseExecutionCopy } from './confirmCopy'
import AccessSettingsDialog from './AccessSettingsDialog'
import PoliciesTab from './policies/PoliciesTab'
import NodeCoverageDrawer from './NodeCoverageDrawer'
import PendingActionGuard from './PendingActionGuard'
import ListsTab from './lists/ListsTab'
import ExemptionsSheet from './sheets/ExemptionsSheet'
import RiskUserDrawer from '../risk/drawer/RiskUserDrawer'
import TestSheet from './sheets/TestSheet'
import type { DrawerHistoryState } from '@/hooks/useDrawerParam'
import type { AccessControlSettingKey } from '@/api/accessControl'
import AccessPageHeader from './AccessPageHeader'
import StatusOverview from './StatusOverview'
const P = 'admin:access_control.'
export default function AccessControlView() {
  const scope = useQueryScope()
  const can = useCan('access.view')
  if (!can) return <Navigate to="/admin/dashboard" replace />
  return <AccessControlPage key={scopeKey(scope)} scope={scope} />
}
function AccessControlPage({ scope }: { scope: QueryScope }) {
  const { t } = useAccessTranslation(['admin', 'common'])
  const definitions = useDestinationPolicies(scope)
  const status = useDestinationStatus(scope)
  const settings = useAccessControlSettings(scope, true)
  const publication = useDestinationPublication(scope)
  const sheet = useDrawerParam<'nodes' | 'settings' | 'list' | 'exemptions' | 'test'>('sheet', { parse: raw => raw === 'nodes' || raw === 'settings' || raw === 'list' || raw === 'exemptions' || raw === 'test' ? raw : null, exclusive: ['user'], clearOnClose: ['node_state', 'list'] })
  const user = useDrawerParam('user', { exclusive: ['sheet'], clearOnClose: ['sheet', 'list', 'node_state'] })
  const activeSheet = user.id === null ? sheet.id : null
  const [params, setParams] = useSearchParams(), location = useLocation()
  const tab = params.get('tab') === 'lists' ? 'lists' : 'policies'
  const [newListRequest, setNewListRequest] = useState(0)
  const [policyRequest, setPolicyRequest] = useState<{ id: number; token: number } | null>(null)
  const policySequence = useRef(0)
  const [settingsFocus, setSettingsFocus] = useState<AccessControlSettingKey | undefined>(undefined)
  const openSettings = (focus?: AccessControlSettingKey) => {
    setSettingsFocus(focus)
    const replacing = sheet.id !== null || user.id !== null, state = { ...location.state }
    delete state.prefill
    if (!replacing || state.drawer === 'sheet' || state.drawer === 'user') state.drawer = 'sheet'
    else delete state.drawer
    setParams(prev => { const next = new URLSearchParams(prev); next.set('sheet', 'settings'); next.delete('user'); next.delete('list'); next.delete('node_state'); return next }, { replace: replacing, state: Object.keys(state).length ? state : null })
  }
  const openTest = (target?: string) => {
    const replacing = sheet.id !== null || user.id !== null, state = { ...location.state, prefill: target ? { target } : undefined }
    if (!replacing || state.drawer === 'sheet' || state.drawer === 'user') state.drawer = 'sheet'
    else delete state.drawer
    setParams(prev => { const next = new URLSearchParams(prev); next.set('sheet', 'test'); next.delete('user'); next.delete('list'); next.delete('node_state'); return next }, { replace: replacing, state })
  }
  const openPolicy = (id: number) => {
    const state = { ...location.state }; delete state.drawer; delete state.prefill
    setParams(prev => { const next = new URLSearchParams(prev); next.set('tab', 'policies'); next.delete('sheet'); next.delete('list'); next.delete('node_state'); next.delete('user'); return next }, { replace: true, state: Object.keys(state).length ? state : null })
    setPolicyRequest({ id, token: ++policySequence.current })
  }
  const consumePolicyRequest = (token: number) => setPolicyRequest(current => current?.token === token ? null : current)
  const openNodes = (filter?: NodeFilter) => {
    const replacing = sheet.id !== null || user.id !== null, state = { ...location.state }
    delete state.prefill
    if (!replacing || state.drawer === 'sheet' || state.drawer === 'user') state.drawer = 'sheet'
    else delete state.drawer
    setParams(prev => { const next = new URLSearchParams(prev); next.set('sheet', 'nodes'); next.delete('user'); next.delete('list'); if (filter) next.set('node_state', filter); else next.delete('node_state'); return next }, { replace: replacing, state: Object.keys(state).length ? state : null })
  }
  const openLists = (problem: boolean) => {
    const state = { ...location.state }; delete state.drawer; delete state.prefill
    setParams(prev => { const next = new URLSearchParams(prev); next.set('tab', 'lists'); next.delete('sheet'); next.delete('user'); next.delete('list'); next.delete('node_state'); if (problem) next.set('lst_state', 'problem'); else next.delete('lst_state'); return next }, { replace: true, state: Object.keys(state).length ? state : null })
  }
  const createList = () => { setNewListRequest(value => value + 1); setParams(prev => { const next = new URLSearchParams(prev); next.set('tab', 'lists'); return next }, { replace: true, state: location.state }) }
  const rawList = params.get('list'), listId = rawList && /^\d+$/.test(rawList) && Number.isSafeInteger(Number(rawList)) && Number(rawList) > 0 ? Number(rawList) : null
  const openList = (id: number) => setParams(prev => { const next = new URLSearchParams(prev); next.set('sheet', 'list'); next.set('list', String(id)); next.delete('node_state'); next.delete('user'); return next }, { replace: sheet.id === 'list', state: { ...location.state, drawer: 'sheet' } })
  const openUser = (id: number) => {
    const replacing = sheet.id !== null || user.id !== null
    const state = { ...location.state }
    delete state.prefill
    if (!replacing || state.drawer === 'sheet' || state.drawer === 'user') state.drawer = 'user'
    else delete state.drawer
    setParams(prev => { const next = new URLSearchParams(prev); next.set('user', String(id)); next.delete('sheet'); next.delete('node_state'); next.delete('list'); return next }, { replace: replacing, state: Object.keys(state).length ? state : null })
  }
  const [busy, setBusy] = useState(false)
  const admission = useRef(false)
  const verdict = status.data ? accessVerdict(status.data, definitions.data) : null
  const unavailable = [definitions.data ? null : definitions.error, status.data ? null : status.error].some(error => destinationError(error).status === 503)
  const perform = async (paused?: boolean) => {
    if (admission.current) return
    admission.current = true; setBusy(true)
    try {
      if (paused !== undefined && !(await confirm(pauseExecutionCopy(t, paused)))) return
      await publication.mutateAsync({ paused }); pushSnack(t(`${P}publication_requested`), 'success')
    } catch (error) { const details = destinationError(error); pushSnack(t(`${P}${details.error === 'dest_policy_pause_saved' || details.error === 'dest_pause_saved' || (details.status === 503 && (error as { response?: { data?: { pause_saved?: boolean } } })?.response?.data?.pause_saved) ? 'pause_saved' : 'write_failed'}`, { error: details.error }), 'error') }
    finally { admission.current = false; setBusy(false) }
  }
  return <Box sx={{ p: { xs: 2, sm: 3 }, minWidth: 0 }}>
    {busy && <PendingActionGuard />}
    <AccessPageHeader userId={user.id} paused={status.data?.paused} busy={busy} onTest={() => openTest()} onOpenUser={openUser} onSettings={() => openSettings()} onPause={perform} />
    {unavailable ? <Alert severity="info">{t(`${P}unwired`)}</Alert> : <>
      <StatusOverview data={status.data} verdict={verdict} failed={!!status.error} refreshing={status.isFetching} readAt={status.dataUpdatedAt} busy={busy} onRetry={() => status.refetch()} onOpenNodes={openNodes} onOpenLists={openLists} onPublish={() => perform()} onPause={perform} />
      <Stack direction="row" sx={{ mb: 2, minWidth: 0, alignItems: 'center' }}>
        <Tabs value={tab} variant="scrollable" scrollButtons={false} sx={{ flex: '1 1 auto', minWidth: 0 }} onChange={(_, value) => setParams(prev => { const next = new URLSearchParams(prev); next.set('tab', value); return next }, { replace: true, state: location.state })} aria-label={t(`${P}tabs`)}><Tab value="policies" label={t(`${P}policies.title`)} /><Tab value="lists" label={t(`${P}lists.title`)} /></Tabs>
        <HelpTip key={tab} textKey={tab === 'lists' ? 'admin:access_control.help.lists' : 'admin:access_control.help.policies'} labelKey="admin:access_control.help.label" labelValues={{ name: t(`${P}${tab}.title`) }} textValues={{ max_regexps: definitions.data?.budget.regexps.limit ?? '—', seconds: settings.data?.effective.dest_policy_apply_min_seconds ?? '—', hours: settings.data?.effective.dest_list_refresh_hours ?? '—' }} />
      </Stack>
      {tab === 'policies' && (definitions.data ? <>{definitions.error && <Alert sx={{ mb: 2 }} severity="warning" action={<Button onClick={() => void definitions.refetch()}>{t('common:actions.retry')}</Button>}>{t(`${P}definitions_stale`)}</Alert>}<PoliciesTab data={definitions.data} status={status.data} seconds={settings.data?.effective.dest_policy_apply_min_seconds} onCreateList={createList} onExemptions={() => sheet.open('exemptions')} openRequest={policyRequest} onOpenRequestHandled={consumePolicyRequest} /></> : definitions.error ? <Alert severity="error" action={<Button onClick={() => void definitions.refetch()}>{t('common:actions.retry')}</Button>}>{t(`${P}definitions_failed`)}</Alert> : <Stack spacing={1} aria-busy="true">{[0,1,2,3,4].map(key => <Skeleton key={key} variant="rounded" height={56} />)}</Stack>)}
      <ListsTab active={tab === 'lists'} selectedId={activeSheet === 'list' ? listId : null} onCloseSheet={sheet.close} onOpenList={openList} onOpenPolicy={openPolicy} onSettings={() => openSettings('dest_list_refresh_hours')} onTest={openTest} newListRequest={newListRequest} policies={definitions.data} status={status.data} />
    </>}
    {activeSheet === 'nodes' && <NodeCoverageDrawer status={status.data} loading={status.isPending} failed={!!status.error} refreshing={status.isFetching} onRetryRead={() => status.refetch()} onClose={sheet.close} onLists={() => openLists(false)} onSettings={() => openSettings('dest_policy_apply_min_seconds')} applySeconds={settings.data?.effective.dest_policy_apply_min_seconds} />}
    {activeSheet === 'exemptions' && <ExemptionsSheet onClose={sheet.close} onOpenUser={openUser} etaMs={status.data?.apply_eta_ms} />}
    {activeSheet === 'test' && <TestSheet onClose={sheet.close} onOpenPolicy={openPolicy} status={status.data} prefill={(location.state as DrawerHistoryState | null)?.prefill} />}
    <RiskUserDrawer userId={user.id} onClose={user.close} host="access" />
    <AccessSettingsDialog open={activeSheet === 'settings'} onClose={sheet.close} focusKey={settingsFocus} />
  </Box>
}
