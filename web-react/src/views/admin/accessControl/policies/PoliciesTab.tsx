import { useEffect, useMemo, useRef, useState } from 'react'
import { Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, Skeleton, Typography, useTheme } from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import { useLocation, useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import { destErrorOf, isDestUnavailable, type DestPolicy, type DestState, type DestTemplate } from '@/api/accessControl'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import {
  useDeleteDestPolicy, useDestCatalog, useDestLists, useDestPolicies, useDestStatus, useInstantiateDestTemplate,
  useReorderDestPolicies, useUpdateDestPolicy,
} from '@/query/accessControl'
import { useAllGroups } from '@/query/groups'
import { useServersList } from '@/query/servers'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes, pipelineSteps, reorderWithin, reportNotDeployedCount, splitBlockByState } from '@/utils/accessControl'
import type { AccessNavState } from '../accessParams'
import { recordsHref, relativeSince } from '../records/recordsParams'
import { deletePolicyConfirm } from '../confirmCopy'
import { destErrorText, reportDestError } from '../destErrors'
import ConvertToBlockDialog from './ConvertToBlockDialog'
import PolicyEditorDialog, { emptyForm, formOf, inputOf, type PolicyForm } from './PolicyEditorDialog'
import PolicyRow, { type RowMove } from './PolicyRow'
import { RailStep } from './PolicyRail'
import QuotaMeters from './QuotaMeters'
import TemplateGrid, { TemplateChooser, UnavailableDialog } from './TemplateGrid'
import TemplateMenu from './TemplateMenu'
import { useFirstPublishGate } from './useFirstPublish'

export interface PoliciesTabProps {
  /** Opens the exemptions sheet (`sheet=exemptions`). */
  onOpenExemptions: () => void
  /** Opens the settings dialog, focused on a field. */
  onOpenSettings: (focus?: 'apply' | 'refresh' | 'rules') => void
  /** Switches to the lists tab, optionally opening the new-list dialog of a kind (S4's "no generic category" card). */
  onGoToLists: (open?: 'new_custom' | 'new_remote' | 'new_geosite') => void
}

const P = 'admin:access_control.policies.'
const HIGHLIGHT_MS = 2000

/**
 * THE POLICIES TAB (S2): the match order as a rail — allow, exempt accounts,
 * block, report-only, everything else — with each step's policies as two-line
 * rows. Report-only is its own step because it is checked last of the rules in
 * force, which is also why it can never change an outcome.
 * Ordering is up/down in the row menu (no dragging, R11) and only decides
 * which policy a hit is credited to within one action. A policy is always
 * written whole with the `updated_at` it was read at (R18).
 */
export default function PoliciesTab({ onOpenExemptions, onOpenSettings, onGoToLists }: PoliciesTabProps) {
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const scope = useQueryScope()
  const location = useLocation()
  const navigate = useNavigate()
  const policies = useDestPolicies(scope)
  const catalog = useDestCatalog(scope)
  const lists = useDestLists(scope)
  const status = useDestStatus(scope)
  const groups = useAllGroups(scope)
  const servers = useServersList(scope, { page: 1, page_size: 200 })
  const update = useUpdateDestPolicy(scope)
  const del = useDeleteDestPolicy(scope)
  const reorder = useReorderDestPolicies(scope)
  const instantiate = useInstantiateDestTemplate(scope)
  const gate = useFirstPublishGate()

  const [editing, setEditing] = useState<{ policy: DestPolicy | null; initial?: PolicyForm } | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)
  const [highlight, setHighlight] = useState<number | null>(null)
  const [converting, setConverting] = useState<DestPolicy | null>(null)
  const [chooser, setChooser] = useState<DestTemplate | null>(null)
  const [chooserError, setChooserError] = useState<{ text: string; needsData: boolean } | null>(null)
  const [unavailableOpen, setUnavailableOpen] = useState(false)
  /** A block policy being switched on whose earlier state the page does not know: the admin says which. */
  const [choosing, setChoosing] = useState<DestPolicy | null>(null)
  /** What a policy was before its switch turned it off, so turning it on restores that (a report-only policy never becomes enforced by it). */
  const stateBeforeOff = useRef(new Map<number, DestState>())
  // One instant per read of the list, so the rows' relative times do not drift apart while it is on screen.
  const readAt = policies.dataUpdatedAt
  const now = useMemo(() => Date.now(), [readAt]) // eslint-disable-line react-hooks/exhaustive-deps

  const all = useMemo(() => [...(policies.data?.allow ?? []), ...(policies.data?.block ?? [])], [policies.data])

  // History state asks for a policy: open its editor and/or scroll to and mark
  // its row. Handled once PER REQUEST (the location entry's key), so a second
  // request while the tab stays mounted works too; cleared by a replace that
  // keeps the URL.
  const handled = useRef<string | null>(null)
  const highlightTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => { if (highlightTimer.current) clearTimeout(highlightTimer.current) }, [])
  useEffect(() => {
    const nav = location.state as AccessNavState | null
    if (!nav || (nav.openPolicy === undefined && nav.highlightPolicy === undefined) || !policies.data) return
    if (handled.current === location.key) return
    handled.current = location.key
    const target = all.find(p => p.id === (nav.openPolicy ?? nav.highlightPolicy))
    void navigate({ pathname: location.pathname, search: location.search }, { replace: true, state: null })
    if (!target) return
    setHighlight(target.id)
    document.querySelector(`[data-policy-id="${target.id}"]`)?.scrollIntoView?.({ block: 'center' })
    if (highlightTimer.current) clearTimeout(highlightTimer.current)
    highlightTimer.current = setTimeout(() => { setHighlight(null); highlightTimer.current = null }, HIGHLIGHT_MS)
    if (nav.openPolicy !== undefined) setEditing({ policy: target })
  }, [location, navigate, policies.data, all])

  if (policies.isPending) {
    return (
      <Box sx={{ display: 'grid', gap: 1 }}>
        <Skeleton variant="text" width="60%" />
        {[0, 1, 2, 3].map(i => <Skeleton key={i} variant="rounded" height={56} />)}
      </Box>
    )
  }
  if (policies.isError || !policies.data) {
    return isDestUnavailable(policies.error)
      ? <Alert severity="info">{t('admin:access_control.unwired')}</Alert>
      : <Alert severity="error" action={<Button color="inherit" size="small" onClick={() => void policies.refetch()}>{t('admin:access_control.retry')}</Button>}>{t(`${P}read_failed`)}</Alert>
  }

  const data = policies.data
  const listNames = Object.fromEntries((lists.data?.items ?? []).map(l => [l.id, l.name]))
  const groupName = new Map((groups.data ?? []).map(g => [g.id, g.name]))
  const panelName = new Map((servers.data?.items ?? []).map(s => [s.id, s.name]))
  const deleted = (kind: 'group' | 'panel') => t(`${P}row.deleted_${kind}`)
  const scopeText = (p: DestPolicy) => p.scope === 'all'
    ? t(`${P}row.scope_all`)
    : p.scope === 'groups'
      ? p.group_ids.map(id => groupName.get(id) ?? deleted('group')).join(t('admin:access_control.policies.separator'))
      : p.panel_ids.map(id => panelName.get(id) ?? deleted('panel')).join(t('admin:access_control.policies.separator'))

  const split = splitBlockByState(data.block)
  /** The rows of a rail section: the report-only step is the block action's rows in that state. */
  const section = (k: 'allow' | 'block' | 'report'): DestPolicy[] => (k === 'allow' ? data.allow : k === 'block' ? split.enforced : split.report)
  const steps = pipelineSteps({ report: true })
  const minutes = applyMinutes(status.data)
  const shed = reportNotDeployedCount(status.data)
  const hasNoExecutor = status.data ? !status.data.nodes.some(n => n.kind === 'psp' && n.supports.policy && n.state !== 'offline' && n.state !== 'unsupported_version') : false
  const nothingYet = data.allow.length === 0 && data.block.length === 0

  /** The one place a policy's state is written: the whole policy, with the updated_at it was read at (R18). */
  const setState = async (p: DestPolicy, state: DestState): Promise<boolean> => {
    if (!(await gate(p.state === 'off' && state !== 'off'))) return false
    setBusyId(p.id)
    try {
      await update.mutateAsync({ id: p.id, input: { ...inputOf(formOf(p), { id: p.id, updatedAt: p.updated_at }), state } })
      return true
    } catch (err) {
      reportDestError(t, err) // a stale policy reloads (the mutation settles through the policies key) and the control is back where it was
      return false
    } finally {
      setBusyId(null)
    }
  }

  // The switch is "deployed or not". A policy switched off here comes back as it was. When the page does not know what
  // it was (a reload since), a block policy asks rather than guess: guessing "on" would silently enforce a report-only one.
  const turnOn = async (p: DestPolicy, state: DestState) => {
    if (await setState(p, state)) stateBeforeOff.current.delete(p.id)
  }
  const toggle = async (p: DestPolicy) => {
    if (p.state === 'off') {
      const before = stateBeforeOff.current.get(p.id)
      if (before === undefined && p.action === 'block') { setChoosing(p); return }
      await turnOn(p, before ?? 'on')
      return
    }
    if (await setState(p, 'off')) {
      stateBeforeOff.current.set(p.id, p.state)
      pushSnack(t(`${P}snack.disabled`, { name: p.name }), 'success')
    }
  }
  const choose = (state: DestState) => {
    const p = choosing
    setChoosing(null)
    if (p) void turnOn(p, state)
  }

  const toReport = async (p: DestPolicy) => {
    if (await setState(p, 'report')) pushSnack(t(`${P}snack.reported`, { name: p.name }), 'success')
  }

  // Report to on is a plain state change; the dialog shows the numbers first and this is its one button.
  const convert = async (p: DestPolicy) => {
    // Closed either way: a refused write (a stale policy above all) reloads, and the dialog holds the old copy.
    const ok = await setState(p, 'on')
    setConverting(null)
    // It keeps its place in the action's one order: its slot among the enforced rows is the count of them ahead of it, plus one.
    const ahead = data.block.slice(0, data.block.findIndex(x => x.id === p.id)).filter(x => x.state !== 'report').length
    if (ok) pushSnack(t(`${P}snack.converted`, { n: ahead + 1 }), 'success')
  }

  const viewRecords = (p: DestPolicy) => void navigate({
    pathname: location.pathname,
    search: recordsHref({
      source: `p${p.id}`, ...(p.state === 'report' ? { action: 'observe' as const } : {}), since: relativeSince(data.hit_window_days, undefined),
    }),
  }, { replace: true })

  const move = async (p: DestPolicy, key: 'allow' | 'block' | 'report', m: RowMove) => {
    // The server's order of an action is one list; a section's move rewrites only its own slots of it.
    const whole = (p.action === 'allow' ? data.allow : data.block).map(x => x.id)
    const ids = reorderWithin(whole, section(key).map(x => x.id), p.id, m)
    if (!ids) return
    try {
      await reorder.mutateAsync({ action: p.action, ids })
      const sectionRows = section(key)
      const to = ids.filter(id => sectionRows.some(r => r.id === id)).indexOf(p.id)
      pushSnack(t(`${P}snack.moved`, { section: t(key === 'report' ? `${P}state.report` : `${P}action.${p.action}`), n: to + 1 }), 'success')
    } catch (err) {
      reportDestError(t, err)
    }
  }

  const remove = async (p: DestPolicy) => {
    if (!(await confirm(deletePolicyConfirm({ t, minutes }, p.name)))) return
    try { await del.mutateAsync(p.id) } catch (err) { reportDestError(t, err) }
  }

  const duplicate = (p: DestPolicy) => setEditing({ policy: null, initial: { ...formOf(p), name: t(`${P}row.copy_name`, { name: p.name }), state: 'off', templateKey: '' } })

  const confirmTemplate = async (state: DestState) => {
    if (!chooser) return
    if (!(await gate(state !== 'off'))) return
    setChooserError(null)
    try {
      const locale = i18n.language || 'zh-CN'
      await instantiate.mutateAsync({ key: chooser.key, state, locale })
      pushSnack(t(`${P}snack.template_added`), 'success')
      setChooser(null)
    } catch (err) {
      const e = destErrorOf(err)
      setChooserError({
        text: e?.code === 'dest_name_taken' ? t(`${P}template_name_taken`) : destErrorText(t, err),
        needsData: e?.code === 'dest_geosite_unavailable',
      })
    }
  }
  const useTemplate = (tpl: DestTemplate) => { setChooserError(null); setChooser(tpl) }

  /** What "add" opens in a step: a block policy (already report-only for the report step), or an allow policy. */
  const blank = (k: 'allow' | 'block' | 'report'): PolicyForm => (k === 'report' ? { ...emptyForm('block'), state: 'report' } : emptyForm(k === 'allow' ? 'allow' : 'block'))
  const renderSection = (k: 'allow' | 'block' | 'report') => {
    const rows = section(k)
    return rows.length === 0
      ? (
        <Box sx={{ border: `1px dashed ${md.outlineVariant}`, borderRadius: 3, p: 1.5, display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
          <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${P}rail.empty_step`)}</Typography>
          <Button size="small" onClick={() => setEditing({ policy: null, initial: blank(k) })}>{t(`${P}rail.add_${k}`)}</Button>
        </Box>
      )
      : (
        <Box sx={{ display: 'grid', gap: 1 }}>
          {rows.map((p, i) => (
            <PolicyRow key={p.id} policy={p} index={i} count={rows.length} listNames={listNames} scopeText={scopeText(p)}
              now={now} notDeployed={shed} busy={busyId === p.id} moving={reorder.isPending} highlighted={highlight === p.id} hitDays={data.hit_window_days}
              onOpen={() => setEditing({ policy: p })} onToggle={() => void toggle(p)} onMove={m => void move(p, k, m)}
              onDuplicate={() => duplicate(p)} onDelete={() => void remove(p)} onReport={() => void toReport(p)}
              onConvert={() => setConverting(p)} onViewRecords={() => viewRecords(p)} />
          ))}
        </Box>
      )
  }
  const addButton = (k: 'allow' | 'block' | 'report') => (
    <Button size="small" startIcon={<AddIcon />} onClick={() => setEditing({ policy: null, initial: blank(k) })}
      aria-label={t(`${P}rail.add_${k}_aria`)}>{t(`${P}rail.add_${k}`)}</Button>
  )

  return (
    <Box>
      <Box sx={{ mb: 2 }}>
        <QuotaMeters budget={data.budget} variant="compact" suffix={hasNoExecutor ? t(`${P}quota.no_executor`) : undefined} />
      </Box>

      {nothingYet && catalog.data
        ? (
          <Box sx={{ display: 'grid', gap: 1.5 }}>
            <Typography component="h2" sx={{ fontSize: 16, fontWeight: 600 }}>{t(`${P}empty.title`)}</Typography>
            <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${P}empty.body`)}</Typography>
            <TemplateGrid catalog={catalog.data} onUse={useTemplate} onGoToLists={() => onGoToLists('new_custom')} />
            <Box><Button variant="outlined" startIcon={<AddIcon />} onClick={() => setEditing({ policy: null, initial: emptyForm('block') })}>{t(`${P}empty.blank`)}</Button></Box>
          </Box>
        )
        : (
          <>
            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1, flexWrap: 'wrap', mb: 2 }}>
              <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${P}rail.order`)}</Typography>
              <TemplateMenu catalog={catalog.data} onBlank={() => setEditing({ policy: null, initial: emptyForm('block') })}
                onUse={useTemplate} onShowUnavailable={() => setUnavailableOpen(true)} />
            </Box>
            {steps.map((s, i) => {
              const last = i === steps.length - 1
              switch (s.id) {
                case 'allow':
                  return <RailStep key={s.id} n={s.n} title={t(`${P}rail.allow`)} caption={t(`${P}rail.allow_caption`)} actions={addButton('allow')}>{renderSection('allow')}</RailStep>
                case 'exempt':
                  return (
                    <RailStep key={s.id} n={s.n}
                      title={data.exemptions.count > 0 ? t(`${P}rail.exempt`, { count: data.exemptions.count }) : t(`${P}rail.exempt_none`)}
                      caption={t(`${P}rail.exempt_caption`)}
                      actions={<Button size="small" onClick={onOpenExemptions}>{t(`${P}rail.manage`)}</Button>} />
                  )
                case 'block':
                  return <RailStep key={s.id} n={s.n} title={t(`${P}rail.block`)} actions={addButton('block')}>{renderSection('block')}</RailStep>
                case 'report':
                  return (
                    <RailStep key={s.id} n={s.n} title={t(`${P}rail.report`)} caption={(
                      <>
                        {t(`${P}rail.report_caption`)}
                        {shed > 0 && <Box component="span" data-testid="rail-report-not-deployed">{t(`${P}row.not_deployed_nodes`, { count: shed })}</Box>}
                      </>
                    )} actions={addButton('report')}>
                      {renderSection('report')}
                    </RailStep>
                  )
                default:
                  return <RailStep key={s.id} n={s.n} title={t(`${P}rail.rest`)} last={last} />
              }
            })}
          </>
        )}

      {editing && (
        <PolicyEditorDialog open policy={editing.policy} initial={editing.initial} onClose={() => setEditing(null)} onSaved={() => setEditing(null)} />
      )}
      <Dialog open={choosing !== null} onClose={() => setChoosing(null)} fullWidth maxWidth="xs"
        slotProps={{ paper: { sx: { bgcolor: md.surfaceContainerHigh, borderRadius: 3 } } }}>
        <DialogTitle>{t(`${P}turn_on.title`, { name: choosing?.name ?? '' })}</DialogTitle>
        <DialogContent><Typography variant="body2">{t(`${P}turn_on.body`)}</Typography></DialogContent>
        <DialogActions sx={{ flexWrap: 'wrap', gap: 1, '& > :not(style) ~ :not(style)': { ml: 0 } }}>
          <Button onClick={() => setChoosing(null)}>{t(`${P}turn_on.cancel`)}</Button>
          <Button onClick={() => choose('on')}>{t(`${P}turn_on.block`)}</Button>
          <Button variant="contained" autoFocus onClick={() => choose('report')}>{t(`${P}turn_on.report`)}</Button>
        </DialogActions>
      </Dialog>
      <ConvertToBlockDialog policy={converting} days={data.hit_window_days} onClose={() => setConverting(null)} onConfirm={convert}
        onViewRecords={p => { setConverting(null); viewRecords(p) }} />
      <TemplateChooser template={chooser} onClose={() => setChooser(null)} onConfirm={confirmTemplate} error={chooserError}
        onOpenSettings={() => { setChooser(null); onOpenSettings('rules') }} />
      <UnavailableDialog open={unavailableOpen} catalog={catalog.data} onClose={() => setUnavailableOpen(false)} onGoToLists={() => onGoToLists('new_custom')} />
    </Box>
  )
}
