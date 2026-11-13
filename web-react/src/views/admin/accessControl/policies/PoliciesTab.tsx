import { useEffect, useMemo, useRef, useState } from 'react'
import { Alert, Box, Button, Skeleton, Typography, useTheme } from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import { useLocation, useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import { destErrorOf, isDestUnavailable, type DestAction, type DestPolicy, type DestTemplate } from '@/api/accessControl'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import {
  useDeleteDestPolicy, useDestCatalog, useDestLists, useDestPolicies, useDestStatus, useInstantiateDestTemplate,
  useReorderDestPolicies, useUpdateDestPolicy,
} from '@/query/accessControl'
import { useAllGroups } from '@/query/groups'
import { useServersList } from '@/query/servers'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes, pipelineSteps } from '@/utils/accessControl'
import type { AccessNavState } from '../accessParams'
import { deletePolicyConfirm } from '../confirmCopy'
import { destErrorText, reportDestError } from '../destErrors'
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
 * block, everything else — with each step's policies as two-line rows.
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
  const [chooser, setChooser] = useState<DestTemplate | null>(null)
  const [chooserError, setChooserError] = useState<{ text: string; needsData: boolean } | null>(null)
  const [unavailableOpen, setUnavailableOpen] = useState(false)

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

  const section = (a: DestAction) => (a === 'allow' ? data.allow : data.block)
  const steps = pipelineSteps()
  const minutes = applyMinutes(status.data)
  const hasNoExecutor = status.data ? !status.data.nodes.some(n => n.kind === 'psp' && n.supports.policy && n.state !== 'offline' && n.state !== 'unsupported_version') : false
  const nothingYet = data.allow.length === 0 && data.block.length === 0

  const toggle = async (p: DestPolicy) => {
    const turnsOn = p.state !== 'on'
    if (!(await gate(turnsOn))) return
    setBusyId(p.id)
    try {
      await update.mutateAsync({ id: p.id, input: { ...inputOf(formOf(p), { id: p.id, updatedAt: p.updated_at }), state: turnsOn ? 'on' : 'off' } })
      if (!turnsOn) pushSnack(t(`${P}snack.disabled`, { name: p.name }), 'success')
    } catch (err) {
      reportDestError(t, err) // a stale policy reloads (the mutation settles through the policies key) and the switch is back where it was
    } finally {
      setBusyId(null)
    }
  }

  const move = async (p: DestPolicy, m: RowMove) => {
    const ids = section(p.action).map(x => x.id)
    const i = ids.indexOf(p.id)
    const to = m === 'up' ? i - 1 : m === 'down' ? i + 1 : m === 'top' ? 0 : ids.length - 1
    if (to < 0 || to >= ids.length || to === i) return
    ids.splice(i, 1)
    ids.splice(to, 0, p.id)
    try {
      await reorder.mutateAsync({ action: p.action, ids })
      pushSnack(t(`${P}snack.moved`, { section: t(`${P}action.${p.action}`), n: to + 1 }), 'success')
    } catch (err) {
      reportDestError(t, err)
    }
  }

  const remove = async (p: DestPolicy) => {
    if (!(await confirm(deletePolicyConfirm({ t, minutes }, p.name)))) return
    try { await del.mutateAsync(p.id) } catch (err) { reportDestError(t, err) }
  }

  const duplicate = (p: DestPolicy) => setEditing({ policy: null, initial: { ...formOf(p), name: t(`${P}row.copy_name`, { name: p.name }), state: 'off', templateKey: '' } })

  const confirmTemplate = async (state: 'off' | 'on') => {
    if (!chooser) return
    if (!(await gate(state === 'on'))) return
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

  const renderSection = (a: DestAction) => {
    const rows = section(a)
    return rows.length === 0
      ? (
        <Box sx={{ border: `1px dashed ${md.outlineVariant}`, borderRadius: 3, p: 1.5, display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
          <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${P}rail.empty_step`)}</Typography>
          <Button size="small" onClick={() => setEditing({ policy: null, initial: emptyForm(a) })}>{t(`${P}rail.add_${a}`)}</Button>
        </Box>
      )
      : (
        <Box sx={{ display: 'grid', gap: 1 }}>
          {rows.map((p, i) => (
            <PolicyRow key={p.id} policy={p} index={i} count={rows.length} listNames={listNames} scopeText={scopeText(p)}
              busy={busyId === p.id} moving={reorder.isPending} highlighted={highlight === p.id}
              onOpen={() => setEditing({ policy: p })} onToggle={() => void toggle(p)} onMove={m => void move(p, m)}
              onDuplicate={() => duplicate(p)} onDelete={() => void remove(p)} />
          ))}
        </Box>
      )
  }
  const addButton = (a: DestAction) => (
    <Button size="small" startIcon={<AddIcon />} onClick={() => setEditing({ policy: null, initial: emptyForm(a) })}
      aria-label={t(`${P}rail.add_${a}_aria`)}>{t(`${P}rail.add_${a}`)}</Button>
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
                default:
                  return <RailStep key={s.id} n={s.n} title={t(`${P}rail.rest`)} last={last} />
              }
            })}
          </>
        )}

      {editing && (
        <PolicyEditorDialog open policy={editing.policy} initial={editing.initial} onClose={() => setEditing(null)} onSaved={() => setEditing(null)} />
      )}
      <TemplateChooser template={chooser} onClose={() => setChooser(null)} onConfirm={confirmTemplate} error={chooserError}
        onOpenSettings={() => { setChooser(null); onOpenSettings('rules') }} />
      <UnavailableDialog open={unavailableOpen} catalog={catalog.data} onClose={() => setUnavailableOpen(false)} onGoToLists={() => onGoToLists('new_custom')} />
    </Box>
  )
}
