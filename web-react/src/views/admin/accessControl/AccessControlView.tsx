import { useEffect, useRef, useState } from 'react'
import { Alert, Box, Button, Skeleton, Stack, Tab, Tabs, Typography } from '@mui/material'
import { Navigate, useLocation, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'
import PageHeader from '@/components/PageHeader'
import StatusLine from '@/components/StatusLine'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { useCan } from '@/utils/permissions'
import { accessVerdict } from '@/utils/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { scopeKey, type QueryScope } from '@/query/session'
import { useAccessControlSettings, useDestinationPolicies, useDestinationPublication, useDestinationStatus } from '@/query/accessControl'
import { useDrawerParam } from '@/hooks/useDrawerParam'
import { destinationError } from './errors'
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
import UserAutocomplete from '@/components/UserAutocomplete'
const P = 'admin:access_control.'
export default function AccessControlView() {
  const scope = useQueryScope()
  const can = useCan('access.view')
  if (!can) return <Navigate to="/admin/dashboard" replace />
  return <AccessControlPage key={scopeKey(scope)} scope={scope} />
}
function AccessControlPage({ scope }: { scope: QueryScope }) {
  const { t } = useTranslation(['admin', 'common'])
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
  const [now, setNow] = useState(Date.now())
  useEffect(() => { if (!status.data?.next_publish_at) return; const timer = window.setInterval(() => setNow(Date.now()), 1000); return () => window.clearInterval(timer) }, [status.data?.next_publish_at])
  const verdict = status.data ? accessVerdict(status.data, definitions.data) : null
  const title = verdict ? t(`${P}verdict.${verdict.kind}`, verdict as unknown as Record<string, unknown>) : t(`${P}status_unknown`)
  const unavailable = [definitions.error, status.error].some(error => destinationError(error).status === 503)
  const perform = async (paused?: boolean) => {
    if (admission.current) return
    admission.current = true; setBusy(true)
    try {
      if (paused !== undefined && !(await confirm({ title: t(`${P}confirm.${paused ? 'pause' : 'resume'}_title`), message: t(`${P}confirm.${paused ? 'pause' : 'resume'}_message`), confirmText: t(`${P}${paused ? 'pause' : 'resume'}`), destructive: paused }))) return
      await publication.mutateAsync({ paused }); pushSnack(t(`${P}publication_requested`), 'success')
    } catch (error) { const details = destinationError(error); pushSnack(t(`${P}${details.error === 'dest_policy_pause_saved' || details.error === 'dest_pause_saved' || (details.status === 503 && (error as { response?: { data?: { pause_saved?: boolean } } })?.response?.data?.pause_saved) ? 'pause_saved' : 'write_failed'}`, { error: details.error }), 'error') }
    finally { admission.current = false; setBusy(false) }
  }
  return <Box sx={{ p: { xs: 2, sm: 3 } }}>
    {busy && <PendingActionGuard />}
    <PageHeader title={t(`${P}title`)} subtitle={t(`${P}subtitle`)} actions={<Stack direction="row" sx={{ flexWrap: 'wrap', alignItems: 'center', gap: 1 }}><Button onClick={() => openTest()}>{t(`${P}test.title`)}</Button><UserAutocomplete key={user.id ?? 'lookup'} label={t(`${P}view_account`)} value={null} onChange={id => { if (id) openUser(id) }} /><Button onClick={() => sheet.open('nodes')}>{t(`${P}coverage.open`)}</Button><Button onClick={() => openSettings()}>{t(`${P}settings.title`)}</Button></Stack>} />
    {unavailable ? <Alert severity="info">{t(`${P}unwired`)}</Alert> : <>
      <StatusLine stackActionsOnMobile tone={status.error ? 'attention' : verdict?.tone ?? 'quiet'} title={status.error ? t(`${P}status_failed`) : title} announcement={status.error ? t(`${P}status_failed`) : title}
        detail={<>{status.data?.next_publish_at != null && <Typography component="span" aria-label={t(`${P}publish_at`, { time: new Date(status.data.next_publish_at).toLocaleString() })}>{t(`${P}countdown`, { seconds: Math.max(0, Math.ceil((status.data.next_publish_at - now) / 1000)) })}</Typography>}{status.data?.publish_error && <Typography component="span">{t(`${P}publish_error`, { kind: status.data.publish_error.kind, used: status.data.publish_error.used, limit: status.data.publish_error.limit })}</Typography>}</>}
        actions={<Stack direction="row" sx={{ flexWrap: 'wrap', gap: 1 }}>{status.error && <Button onClick={() => void status.refetch()}>{t('common:actions.retry')}</Button>}{status.data && <><Button disabled={busy} onClick={() => void perform()}>{t(`${P}publish`)}</Button><Button disabled={busy} color={status.data.paused ? 'primary' : 'error'} onClick={() => void perform(!status.data!.paused)}>{t(`${P}${status.data.paused ? 'resume' : 'pause'}`)}</Button></>}</Stack>} />
      <Tabs value={tab} sx={{ mb: 2 }} onChange={(_, value) => setParams(prev => { const next = new URLSearchParams(prev); next.set('tab', value); return next }, { replace: true, state: location.state })} aria-label={t(`${P}tabs`)}><Tab value="policies" label={t(`${P}policies.title`)} /><Tab value="lists" label={t(`${P}lists.title`)} /></Tabs>
      {tab === 'policies' && (definitions.data ? <>{definitions.error && <Alert sx={{ mb: 2 }} severity="warning" action={<Button onClick={() => void definitions.refetch()}>{t('common:actions.retry')}</Button>}>{t(`${P}definitions_stale`)}</Alert>}<PoliciesTab data={definitions.data} status={status.data} seconds={settings.data?.effective.dest_policy_apply_min_seconds} onCreateList={createList} onExemptions={() => sheet.open('exemptions')} openRequest={policyRequest} onOpenRequestHandled={consumePolicyRequest} /></> : definitions.error ? <Alert severity="error" action={<Button onClick={() => void definitions.refetch()}>{t('common:actions.retry')}</Button>}>{t(`${P}definitions_failed`)}</Alert> : <Stack spacing={1} aria-busy="true">{[0,1,2,3,4].map(key => <Skeleton key={key} variant="rounded" height={56} />)}</Stack>)}
      <ListsTab active={tab === 'lists'} selectedId={activeSheet === 'list' ? listId : null} onCloseSheet={sheet.close} onOpenList={openList} onOpenPolicy={openPolicy} onSettings={() => openSettings('dest_list_refresh_hours')} onTest={openTest} newListRequest={newListRequest} policies={definitions.data} status={status.data} />
    </>}
    {activeSheet === 'nodes' && <NodeCoverageDrawer status={status.data} onClose={sheet.close} />}
    {activeSheet === 'exemptions' && <ExemptionsSheet onClose={sheet.close} onOpenUser={openUser} etaMs={status.data?.apply_eta_ms} />}
    {activeSheet === 'test' && <TestSheet onClose={sheet.close} onOpenPolicy={openPolicy} status={status.data} prefill={(location.state as DrawerHistoryState | null)?.prefill} />}
    <RiskUserDrawer userId={user.id} onClose={user.close} host="access" />
    <AccessSettingsDialog open={activeSheet === 'settings'} onClose={sheet.close} focusKey={settingsFocus} />
  </Box>
}
