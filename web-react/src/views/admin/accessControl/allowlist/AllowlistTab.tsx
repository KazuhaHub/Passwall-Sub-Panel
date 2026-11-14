import { useEffect, useMemo, useRef, useState } from 'react'
import { Alert, Box, Button, Skeleton, Typography, useTheme } from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import { useLocation, useNavigate, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'

import { destErrorOf, type DestGroup, type DestState } from '@/api/accessControl'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { useDestGroups, useDestStatus, usePutDestGroup } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes } from '@/utils/accessControl'
import { allowlistSearch, parseAllowlistParams, type SettingsFocus } from '../accessParams'
import { allowlistBackToReportConfirm, allowlistOffConfirm, minutesWords } from '../confirmCopy'
import { reportDestError } from '../destErrors'
import { recordsHref } from '../records/recordsParams'
import AllowlistGroupCard from './AllowlistGroupCard'
import AllowlistReport from './AllowlistReport'
import EnableAllowlistDialog, { type EnableSaved } from './EnableAllowlistDialog'
import TurnOnDialog from './TurnOnDialog'
import { groupHasAllowlist, stateDay } from './allowlistState'

const A = 'admin:access_control.allowlist.'
const ORDER: Record<DestState, number> = { on: 0, report: 1, off: 2 }

export interface AllowlistTabProps {
  /** Opens a list in the entries sheet. */
  onOpenEntries: (listId: number) => void
  /** Opens the node coverage sheet. */
  onOpenNodes: () => void
  onOpenSettings: (focus?: SettingsFocus) => void
  /** Opens the test sheet on a destination. */
  onTestDestination: (target: string) => void
}

/**
 * THE ALLOWLIST TAB (S8): the groups whose members can reach only the
 * destinations in their lists. One card per group that has an allowlist (a group
 * that is off keeps its card, quiet, so its lists are not lost), the enable
 * dialog, and the report of what a report-only group would have denied. The
 * state of a group is one word the admin picks (off, report-only, on), any to
 * any; only turning ON asks through a checklist, the other changes loosen.
 */
export default function AllowlistTab({ onOpenEntries, onOpenNodes, onOpenSettings, onTestDestination }: AllowlistTabProps) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const navigate = useNavigate()
  const location = useLocation()
  const [params, setParams] = useSearchParams()
  const al = parseAllowlistParams(params)
  const groups = useDestGroups(scope)
  const status = useDestStatus(scope)
  const put = usePutDestGroup(scope)
  const [enable, setEnable] = useState<{ groupId: number | null } | null>(null)
  const [turningOn, setTurningOn] = useState<DestGroup | null>(null)
  const [now] = useState(() => Date.now())

  // A link that asks for the enable dialog on a group (the groups page, "enable" on a card): open it once, then drop the param.
  const handled = useRef<string | null>(null)
  useEffect(() => {
    if (al.enable === null || !groups.data || handled.current === location.key) return
    handled.current = location.key
    setEnable({ groupId: al.enable })
    setParams(allowlistSearch(params, { enable: null }), { replace: true })
  }, [al.enable, groups.data, location.key, params, setParams])

  const all = useMemo(() => groups.data ?? [], [groups.data])
  const cards = useMemo(() => all.filter(groupHasAllowlist).sort((a, b) => ORDER[a.state] - ORDER[b.state] || a.group_name.localeCompare(b.group_name)), [all])
  const droppedOn = (id: number) => (status.data?.nodes ?? []).filter(n => (n.sniffing_groups_dropped ?? []).some(g => g.id === id)).map(n => n.panel_name)
  const minutes = applyMinutes(status.data)

  const openReport = (id: number) => setParams(allowlistSearch(params, { group: id, report: true }))
  const leaveReport = () => {
    // The report was pushed, so Back leaves it; a link that opened it directly has nowhere to go back to.
    if (location.key !== 'default') void navigate(-1)
    else setParams(allowlistSearch(params, { group: null, report: false, days: null }), { replace: true })
  }
  const viewDenied = (id: number) => void navigate({ pathname: location.pathname, search: recordsHref({ source: `g${id}`, action: 'block', since: '24h' }) }, { replace: true })
  const openGroup = (id: number) => void navigate(`/admin/groups?edit=${id}&dtab=destination`)

  /** A write of one group's state, from the card. The lists are kept; the updated_at is the one the card was read at. */
  const write = async (g: DestGroup, state: DestState) => {
    await put.mutateAsync({ id: g.group_id, input: { state, list_ids: g.list_ids, ...(g.updated_at !== null ? { updated_at: g.updated_at } : {}) } })
  }
  const change = async (g: DestGroup, state: 'report' | 'off') => {
    const ask = state === 'report' ? allowlistBackToReportConfirm({ t, minutes }, g.group_name) : allowlistOffConfirm({ t, minutes }, g.group_name, g.ineligible.length)
    if (!(await confirm(ask))) return
    try {
      await write(g, state)
      pushSnack(t(state === 'report' ? `${A}snack.report` : `${A}snack.off`, { group: g.group_name, minutes: minutesWords(t, minutes) }), 'success')
    } catch (err) {
      if (destErrorOf(err)?.code === 'dest_group_stale') pushSnack(t(`${A}snack.stale`), 'warning')
      else reportDestError(t, err)
    }
  }
  const turnedOn = async (g: DestGroup) => {
    await write(g, 'on')
    pushSnack(t(`${A}snack.on`, { group: g.group_name, minutes: minutesWords(t, minutes) }), 'success')
    setTurningOn(null)
  }
  const saved = (s: EnableSaved) => { if (s.created && s.state === 'report') openReport(s.groupId) }

  if (al.report && al.group !== null) {
    const g = all.find(x => x.group_id === al.group)
    return (
      <>
        <AllowlistReport groupId={al.group} onBack={leaveReport} onOpenEntries={onOpenEntries} onTestDestination={onTestDestination}
          onOpenSettings={onOpenSettings} onTurnOn={() => g && setTurningOn(g)} onViewDenied={() => viewDenied(al.group as number)} />
        {turningOn && (
          <TurnOnDialog open group={{ id: turningOn.group_id, name: turningOn.group_name, members: turningOn.members }} listIds={turningOn.list_ids}
            reportDay={stateDay(turningOn.state_changed_at, now)} onConfirm={() => turnedOn(turningOn)} onClose={() => setTurningOn(null)}
            onViewReport={() => setTurningOn(null)} onViewNodes={() => { setTurningOn(null); onOpenNodes() }} />
        )}
      </>
    )
  }

  const enableButton = (
    <Button variant="contained" startIcon={<AddIcon />} onClick={() => setEnable({ groupId: null })}>{t(`${A}enable_button`)}</Button>
  )

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1, flexWrap: 'wrap', mb: 2 }}>
        <Typography variant="body2" sx={{ color: md.onSurfaceVariant, flex: 1, minWidth: 200 }}>{t(`${A}intro`)}</Typography>
        {cards.length > 0 && enableButton}
      </Box>

      {groups.isPending && [0, 1].map(i => <Skeleton key={i} data-testid="allowlist-skeleton" variant="rounded" height={140} sx={{ mb: 1.5 }} />)}
      {groups.isError && (
        <Alert severity="error" action={<Button color="inherit" size="small" onClick={() => void groups.refetch()}>{t('admin:access_control.retry')}</Button>}>
          {t(`${A}read_failed`)}
        </Alert>
      )}
      {groups.data && cards.length === 0 && (
        <Box sx={{ display: 'grid', gap: 1.5, justifyItems: 'start' }}>
          <Typography component="h2" sx={{ fontSize: 16, fontWeight: 600 }}>{t(`${A}empty.title`)}</Typography>
          <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${A}empty.body`)}</Typography>
          {enableButton}
        </Box>
      )}
      {cards.length > 0 && (
        <Box sx={{ display: 'grid', gap: 1.5 }}>
          {cards.map(g => (
            <AllowlistGroupCard key={g.group_id} group={g} now={now} highlighted={al.group === g.group_id} droppedOn={droppedOn(g.group_id)}
              onOpenEntries={onOpenEntries} onOpenNodes={onOpenNodes} onReport={() => openReport(g.group_id)} onViewDenied={() => viewDenied(g.group_id)}
              onTurnOn={() => setTurningOn(g)} onBackToReport={() => void change(g, 'report')} onTurnOff={() => void change(g, 'off')}
              onEditLists={() => setEnable({ groupId: g.group_id })} onOpenGroup={() => openGroup(g.group_id)} />
          ))}
        </Box>
      )}

      {enable && (
        <EnableAllowlistDialog open groups={all} initialGroupId={enable.groupId} onClose={() => setEnable(null)} onSaved={saved}
          onOpenGroupReport={openReport} onViewNodes={onOpenNodes} />
      )}
      {turningOn && (
        <TurnOnDialog open group={{ id: turningOn.group_id, name: turningOn.group_name, members: turningOn.members }} listIds={turningOn.list_ids}
          reportDay={stateDay(turningOn.state_changed_at, now)} onConfirm={() => turnedOn(turningOn)} onClose={() => setTurningOn(null)}
          onViewReport={() => { const id = turningOn.group_id; setTurningOn(null); openReport(id) }}
          onViewNodes={() => { setTurningOn(null); onOpenNodes() }} />
      )}
    </Box>
  )
}
