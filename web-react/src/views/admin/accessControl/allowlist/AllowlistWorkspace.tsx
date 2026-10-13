import { useState } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router'
import type { DestinationGroupSummary } from '@/api/destinationGroups'
import type { QueryScope } from '@/query/session'
import AllowlistTab from './AllowlistTab'
import AllowlistModeDialog from './AllowlistModeDialog'
import EnableAllowlistDialog from './EnableAllowlistDialog'
import TrialReport from './TrialReport'

export function allowlistID(raw: string | null): number | null { const id = Number(raw); return raw && /^[1-9]\d*$/.test(raw) && Number.isSafeInteger(id) ? id : null }
const viewKeys = ['al_group', 'al_view', 'al_days', 'al_enable']
export default function AllowlistWorkspace({ scope, active, retentionDays, etaMs, onList, onNodes, onTest }: {
  scope: QueryScope; active: boolean; retentionDays?: number; etaMs?: number
  onList: (id: number) => void; onNodes: (groupID: number) => void; onTest: (site: string, groupID: number) => void
}) {
  const [params, setParams] = useSearchParams(), location = useLocation(), navigate = useNavigate()
  const [wizard, setWizard] = useState<{ groupID?: number; editing?: boolean } | null>(null)
  const [mode, setMode] = useState<{ id: number; target: 'trial' | 'enforce' | 'open' } | null>(null)
  const groupID = allowlistID(params.get('al_group')), enableID = allowlistID(params.get('al_enable'))
  const activeWizard = wizard ?? (enableID !== null ? { groupID: enableID } : null)
  if (!active) return null
  const clearOverlayState = () => { const state = { ...location.state }; delete state.drawer; delete state.prefill; return state }
  const closeWizard = () => {
    setWizard(null)
    if (enableID !== null) setParams(prev => { const next = new URLSearchParams(prev); next.delete('al_enable'); return next }, { replace: true, state: location.state })
  }
  const openReport = async (id: number, fromWizard = false) => {
    setMode(null); setWizard(null)
    const base = new URLSearchParams(params), state = clearOverlayState()
    for (const key of [...viewKeys, 'sheet', 'list', 'node_state', 'node_group', 'user']) base.delete(key)
    base.set('tab', 'allowlist'); delete state.allowlistReport
    if (fromWizard && enableID !== null) await navigate({ pathname: location.pathname, search: `?${base}` }, { replace: true, state })
    const next = new URLSearchParams(base); next.set('al_group', String(id)); next.set('al_view', 'trial')
    await navigate({ pathname: location.pathname, search: `?${next}` }, { state: { ...state, allowlistReport: true } })
  }
  const denied = (id: number) => {
    const next = new URLSearchParams(params), state = clearOverlayState(); delete state.allowlistReport
    for (const key of [...next.keys()]) if (viewKeys.includes(key) || key.startsWith('rec_') || ['sheet', 'list', 'node_state', 'node_group', 'user'].includes(key)) next.delete(key)
    next.set('tab', 'records'); next.set('rec_source', `g${id}`); next.set('rec_action', 'deny'); next.set('rec_since', '24h')
    setParams(next, { state })
  }
  const back = () => {
    if (location.state?.allowlistReport) { void navigate(-1); return }
    const next = new URLSearchParams(params); for (const key of viewKeys) next.delete(key); next.set('tab', 'allowlist')
    const state = clearOverlayState(); delete state.allowlistReport
    setParams(next, { replace: true, state })
  }
  const changeMode = (group: DestinationGroupSummary, target: 'trial' | 'enforce' | 'open') => setMode({ id: group.group_id, target })
  return <>
    {groupID !== null ? <TrialReport key={groupID} scope={scope} groupID={groupID} days={Number(params.get('al_days')) || 7} retentionDays={retentionDays}
      onDays={days => setParams(prev => { const next = new URLSearchParams(prev); next.set('al_days', String(days)); return next }, { replace: true, state: location.state })}
      onBack={back} onEnforce={id => setMode({ id, target: 'enforce' })} onList={onList} onTest={onTest} onDenied={denied} /> : <AllowlistTab scope={scope} active onEnable={() => setWizard({})} onList={onList}
      onReport={group => { void openReport(group.group_id) }} onDenied={group => denied(group.group_id)} onEdit={group => setWizard({ groupID: group.group_id, editing: true })}
      onChangeMode={changeMode} onOpenGroup={group => { void navigate(`/admin/groups?edit=${group.group_id}&dtab=destination`) }} />}
    {activeWizard && <EnableAllowlistDialog key={`${activeWizard.groupID ?? 'new'}-${!!activeWizard.editing}`} scope={scope} initialGroupID={activeWizard.groupID} editing={activeWizard.editing} onClose={closeWizard}
      onStarted={id => { if (activeWizard.editing) closeWizard(); else void openReport(id, true) }} onList={id => { closeWizard(); onList(id) }} />}
    {mode && <AllowlistModeDialog key={`${mode.id}-${mode.target}`} scope={scope} groupID={mode.id} target={mode.target} retentionDays={retentionDays} etaMs={etaMs} onClose={() => setMode(null)} onReport={id => { void openReport(id) }} onNodes={id => { setMode(null); onNodes(id) }} />}
  </>
}
