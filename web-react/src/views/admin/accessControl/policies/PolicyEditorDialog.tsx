import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import {
  Alert, Autocomplete, Box, Button, Checkbox, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, IconButton,
  Skeleton, TextField, ToggleButton, ToggleButtonGroup, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import { useTranslation } from 'react-i18next'

import {
  destErrorOf, previewDestPolicy, type DestAction, type DestBudget, type DestList, type DestPolicy, type DestPolicyInput,
  type DestScope, type DestState,
} from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import FieldHint from '@/components/FieldHint'
import ToneBadge from '@/components/ToneBadge'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import {
  useCreateDestPolicy, useDestLists, useDestPolicies, useDestStatus, useUpdateDestPolicy,
} from '@/query/accessControl'
import { useAllGroups } from '@/query/groups'
import { useServersList } from '@/query/servers'
import { useQueryScope } from '@/query/useQueryScope'
import { pushSnack } from '@/components/SnackbarHost'
import { accessTone, anyBudgetOver, applyMinutes, enforcingNodeCount, pipelineSteps } from '@/utils/accessControl'
import { discardChangesConfirm } from '../confirmCopy'
import { destErrorText } from '../destErrors'
import { matchSummary, summaryText } from './matchSummary'
import QuotaMeters from './QuotaMeters'
import { useFirstPublishGate } from './useFirstPublish'

// The IP-range box is a code editor (plain: a list is not YAML), loaded only
// when an editor opens.
const CodeEditor = lazy(() => import('@/components/CodeEditor'))

const D = 'admin:access_control.policies.editor.'
const PREVIEW_DEBOUNCE_MS = 400
/** The server's cap on a description, in UTF-8 bytes. */
const DESCRIPTION_MAX_BYTES = 512
const NAME_MAX_BYTES = 128
const utf8Bytes = (text: string) => new TextEncoder().encode(text).length
/** The error keys a rendered input shows; any other field falls back to the dialog's top error. */
const RENDERED_FIELDS = new Set(['name', 'description', 'list_ids', 'cidrs', 'ports', 'match', 'group_ids', 'panel_ids', 'scope'])

export interface PolicyForm {
  name: string
  description: string
  action: DestAction
  state: DestState
  listIds: number[]
  cidrs: string
  ports: string
  network: '' | 'tcp' | 'udp'
  bittorrent: boolean
  private: boolean
  scope: DestScope
  groupIds: number[]
  panelIds: number[]
  templateKey: string
}

export const emptyForm = (action: DestAction = 'block'): PolicyForm => ({
  name: '', description: '', action, state: 'off', listIds: [], cidrs: '', ports: '', network: '', bittorrent: false,
  private: false, scope: 'all', groupIds: [], panelIds: [], templateKey: '',
})

export function formOf(p: DestPolicy): PolicyForm {
  return {
    name: p.name, description: p.description, action: p.action, state: p.state === 'on' ? 'on' : 'off', listIds: p.list_ids,
    cidrs: (p.inline.cidrs ?? []).join('\n'), ports: p.inline.ports ?? '',
    network: p.inline.network === 'tcp' || p.inline.network === 'udp' ? p.inline.network : '',
    bittorrent: (p.inline.protocols ?? []).includes('bittorrent'), private: p.inline.private === true,
    scope: p.scope, groupIds: p.group_ids, panelIds: p.panel_ids, templateKey: p.template_key,
  }
}

/** The request body of a form; `updatedAt` only on an edit. */
export function inputOf(f: PolicyForm, extra: { id?: number; updatedAt?: number } = {}): DestPolicyInput {
  const cidrs = f.cidrs.split('\n').map(s => s.trim()).filter(Boolean)
  return {
    name: f.name.trim(), description: f.description.trim(), action: f.action, state: f.state, list_ids: f.listIds,
    inline: {
      ...(cidrs.length ? { cidrs } : {}), ...(f.ports.trim() ? { ports: f.ports.trim() } : {}),
      ...(f.network ? { network: f.network } : {}), ...(f.bittorrent ? { protocols: ['bittorrent'] } : {}),
      ...(f.private ? { private: true } : {}),
    },
    scope: f.scope, group_ids: f.scope === 'groups' ? f.groupIds : [], panel_ids: f.scope === 'panels' ? f.panelIds : [],
    ...(f.templateKey ? { template_key: f.templateKey } : {}),
    ...(extra.id ? { id: extra.id } : {}), ...(extra.updatedAt !== undefined ? { updated_at: extra.updatedAt } : {}),
  }
}

/** Whether a change reaches the nodes: only a rename or a new description does not. */
function changesWire(a: PolicyForm, b: PolicyForm): boolean {
  return JSON.stringify({ ...a, name: '', description: '' }) !== JSON.stringify({ ...b, name: '', description: '' })
}

const lineNumbersOf = (text: string) => text.split('\n').map((l, i) => [l.trim(), i + 1] as const).filter(([l]) => l)

/**
 * THE POLICY EDITOR (S3): create or edit one block or allow policy. Before
 * saving it shows where the policy lands in the match order, what the quotas
 * will be after, how many nodes restart and what each choice means. Closing
 * with unsaved changes asks (useDirtyClose; a route change asks through
 * useLeaveGuard). Save is always "Save": the apply is debounced, so a "save
 * and apply" would promise an immediacy it does not have.
 */
export default function PolicyEditorDialog({ open, policy, initial, onClose, onSaved }: {
  open: boolean
  /** The policy being edited, or null to create. */
  policy: DestPolicy | null
  /** A form to start from when creating (a new block policy, a duplicate). */
  initial?: PolicyForm
  onClose: () => void
  onSaved: (saved: DestPolicy) => void
}) {
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const phone = useMediaQuery(theme.breakpoints.down('sm'))
  const scope = useQueryScope()
  const lists = useDestLists(scope)
  const policies = useDestPolicies(scope)
  const status = useDestStatus(scope)
  const groups = useAllGroups(scope)
  const servers = useServersList(scope, { page: 1, page_size: 200 })
  const create = useCreateDestPolicy(scope)
  const update = useUpdateDestPolicy(scope)
  const gate = useFirstPublishGate()

  const seed = useMemo(() => (policy ? formOf(policy) : initial ?? emptyForm()), [policy, initial])
  const [form, setForm] = useState<PolicyForm>(seed)
  const [base, setBase] = useState<PolicyForm>(seed)
  const [updatedAt, setUpdatedAt] = useState<number | undefined>(policy?.updated_at)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [topError, setTopError] = useState<string | null>(null)
  const [stale, setStale] = useState(false)
  const [preview, setPreview] = useState<{ budget: DestBudget | null; loading: boolean; failed: boolean }>({ budget: null, loading: false, failed: false })
  const [retry, setRetry] = useState(0)
  const [touched, setTouched] = useState(false)
  // Group and panel ids the seeded policy named that no longer exist: removed from the form, counted here.
  const [removed, setRemoved] = useState(0)

  const set = <K extends keyof PolicyForm>(k: K, v: PolicyForm[K]) => { setForm(f => ({ ...f, [k]: v })); setErrors(e => ({ ...e })) }
  const dirty = JSON.stringify(form) !== JSON.stringify(base)
  const guard = useDirtyClose(dirty, discardChangesConfirm(t))
  useLeaveGuard(open && dirty, discardChangesConfirm(t), (next, cur) => next.pathname !== cur.pathname)

  // Re-seed when the dialog is opened on another policy.
  const seededFor = useRef<unknown>(null)
  useEffect(() => {
    if (!open || seededFor.current === seed) return
    seededFor.current = seed
    setForm(seed); setBase(seed); setUpdatedAt(policy?.updated_at); setErrors({}); setTopError(null); setStale(false); setTouched(false); setRemoved(0)
    setPreview({ budget: null, loading: false, failed: false })
  }, [open, seed, policy])

  // Once the groups and servers are known, drop the ids of ones that were deleted.
  const prunedFor = useRef<unknown>(null)
  useEffect(() => {
    // A partial server list (more servers than one page) cannot tell a deleted panel from one on
    // another page: skip the panel pruning then rather than drop live ids.
    if (!open || !policy || prunedFor.current === seed || !groups.data || !servers.data) return
    prunedFor.current = seed
    const gone = (ids: number[], known: Set<number>) => ids.filter(id => !known.has(id))
    const knownGroups = new Set(groups.data.map(g => g.id))
    const knownPanels = new Set(servers.data.items.map(x => x.id))
    const deadG = gone(seed.groupIds, knownGroups)
    const complete = !(servers.data.total > servers.data.items.length)
    const deadP = complete ? gone(seed.panelIds, knownPanels) : []
    if (deadG.length + deadP.length === 0) return
    setForm(f => ({ ...f, groupIds: f.groupIds.filter(id => knownGroups.has(id)), panelIds: complete ? f.panelIds.filter(id => knownPanels.has(id)) : f.panelIds }))
    setRemoved(deadG.length + deadP.length)
  }, [open, policy, seed, groups.data, servers.data])

  const allLists: DestList[] = (lists.data?.items ?? []).filter(l => l.owner_group_id === 0 && l.side === 'dest')
  const listById = new Map(allLists.map(l => [l.id, l]))
  const notReady = form.listIds.map(id => listById.get(id)).filter((l): l is DestList => !!l && (l.state === 'pending' || (l.state === 'failed' && l.last_fetched_at === null)))
  const input = inputOf(form, { id: policy?.id })

  // A policy made from a catalog template keeps its matching: the mapping is the catalog's, not the admin's.
  const locked = !!form.templateKey
  const descTooLong = utf8Bytes(form.description) > DESCRIPTION_MAX_BYTES
  const nameTooLong = utf8Bytes(form.name) > NAME_MAX_BYTES
  const noScopePick = (form.scope === 'groups' && form.groupIds.length === 0) || (form.scope === 'panels' && form.panelIds.length === 0)

  const noMatch = form.listIds.length === 0 && !form.cidrs.trim() && !form.private && !form.bittorrent && !form.ports.trim() && !form.network
  const cidrLines = lineNumbersOf(form.cidrs)

  // The quota preview, after typing pauses. Failure is shown as "—" and never blocks saving: the server judges again.
  const key = JSON.stringify(input)
  useEffect(() => {
    if (!open || !form.name.trim() || noMatch) return
    const ctl = new AbortController()
    const timer = setTimeout(() => {
      setPreview(p => ({ ...p, loading: true, failed: false }))
      previewDestPolicy(inputOf(form, { id: policy?.id }), { signal: ctl.signal })
        .then(r => setPreview({ budget: r.budget, loading: false, failed: false }))
        .catch(() => { if (!ctl.signal.aborted) setPreview(p => ({ ...p, loading: false, failed: true })) })
    }, PREVIEW_DEBOUNCE_MS)
    return () => { clearTimeout(timer); ctl.abort() }
    // `key` is the serialised input: the effect runs when what is previewed changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, key, retry])

  const over = anyBudgetOver(preview.budget ?? undefined)
  const busy = create.isPending || update.isPending

  // Where it lands: the step and its place among that action's policies.
  const position = useMemo(() => {
    const all = [...(policies.data?.allow ?? []), ...(policies.data?.block ?? [])]
    const same = all.filter(p => p.action === form.action)
    const idx = policy && policy.action === form.action ? same.findIndex(p => p.id === policy.id) : same.length
    return { index: idx >= 0 ? idx + 1 : same.length + 1, step: pipelineSteps().find(s => s.id === form.action)?.n ?? 1 }
  }, [policies.data, policy, form.action])

  const summary = summaryText(t, matchSummary({
    listNames: form.listIds.map(id => listById.get(id)?.name ?? ''), cidrs: cidrLines.length, private: form.private,
    bittorrent: form.bittorrent, ports: form.ports, network: form.network,
  }))
  const split = matchSummary({ listNames: form.listIds.map(String), cidrs: cidrLines.length, private: form.private, bittorrent: form.bittorrent, ports: form.ports, network: form.network }).split

  const executors = enforcingNodeCount(status.data)
  const minutes = applyMinutes(status.data)
  let footer: string
  if (form.state !== 'on' && base.state !== 'on') footer = t(`${D}footer.disabled`)
  else if (policy && !changesWire(form, base)) footer = t(`${D}footer.rename_only`)
  else if (executors === null || minutes === null) footer = t(`${D}footer.applies_unknown`)
  else footer = t(`${D}footer.applies`, { count: executors, minutes })

  const validate = (): boolean => {
    const next: Record<string, string> = {}
    if (!form.name.trim()) next.name = t(`${D}errors.name_required`)
    if (noMatch) next.match = t(`${D}errors.no_match`)
    if (form.scope === 'groups' && form.groupIds.length === 0) next.scope = t(`${D}errors.scope_groups`)
    if (form.scope === 'panels' && form.panelIds.length === 0) next.scope = t(`${D}errors.scope_panels`)
    setErrors(next)
    return Object.keys(next).length === 0
  }

  const save = async () => {
    setTouched(true); setTopError(null)
    if (!validate() || over) return
    if (!(await gate(form.state === 'on' && (!policy || policy.state !== 'on')))) return
    try {
      const saved = policy
        ? await update.mutateAsync({ id: policy.id, input: inputOf(form, { updatedAt }) })
        : await create.mutateAsync(inputOf(form))
      pushSnack(t(`${D}saved`, { name: saved.name }), 'success')
      setBase(form)
      onSaved(saved)
    } catch (err) {
      const e = destErrorOf(err)
      if (e?.code === 'dest_policy_stale') { setStale(true); return }
      if (e?.code === 'dest_name_taken') { setErrors({ name: t('admin:access_control.errors.dest_name_taken') }); return }
      const field = e?.field?.replace(/^inline\./, '')
      if (e?.status === 400 && field && RENDERED_FIELDS.has(field)) {
        const bad = Array.isArray(e.body.bad) ? ` (${(e.body.bad as string[]).join(', ')})` : ''
        setErrors({ [field]: destErrorText(t, err) + bad }); return
      }
      setTopError(destErrorText(t, err))
    }
  }

  const reload = async () => {
    const fresh = await policies.refetch()
    const p = [...(fresh.data?.allow ?? []), ...(fresh.data?.block ?? [])].find(x => x.id === policy?.id)
    if (!p) { onClose(); return }
    const f = formOf(p)
    setForm(f); setBase(f); setUpdatedAt(p.updated_at); setStale(false); setErrors({})
  }

  const title = policy ? t(`${D}title_edit`, { name: policy.name }) : t(`${D}title_new`)
  const groupOptions = groups.data ?? []
  const panelOptions = (servers.data?.items ?? []).filter(s => s.panel_type === 'psp')
  const num = new Intl.NumberFormat(i18n.language)

  return (
    <Dialog open={open} onClose={() => guard(onClose)} fullWidth maxWidth={false} fullScreen={phone}
      slotProps={{ paper: { sx: { maxWidth: phone ? undefined : 720, bgcolor: md.surfaceContainerHigh, borderRadius: phone ? 0 : 3 } } }}>
      <DialogTitle sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        <Box component="span" sx={{ flex: 1, minWidth: 0, overflowWrap: 'anywhere' }}>{title}</Box>
        <IconButton aria-label={t(`${D}close`)} onClick={() => guard(onClose)}><CloseIcon /></IconButton>
      </DialogTitle>
      <DialogContent dividers sx={{ display: 'grid', gap: 2.5 }}>
        {stale && (
          <Alert severity="warning" action={<Button color="inherit" size="small" onClick={() => void reload()}>{t(`${D}reload`)}</Button>}>
            {t(`${D}stale`)}
          </Alert>
        )}
        {topError && <Alert severity="error">{topError}</Alert>}
        <fieldset disabled={busy} style={{ border: 0, padding: 0, margin: 0, display: 'grid', gap: 20, minWidth: 0 }}>
          <TextField label={t(`${D}name`)} value={form.name} onChange={e => set('name', e.target.value)} required fullWidth
            error={nameTooLong || !!errors.name}
            helperText={nameTooLong ? t(`${D}errors.name_too_long`, { max: NAME_MAX_BYTES }) : errors.name} />
          <TextField label={t(`${D}description`)} value={form.description} onChange={e => set('description', e.target.value)} fullWidth
            multiline minRows={1} error={descTooLong || !!errors.description}
            helperText={descTooLong ? t(`${D}errors.description_too_long`, { max: DESCRIPTION_MAX_BYTES }) : errors.description} />

          <Box>
            <Typography variant="body2" sx={{ mb: 0.5, fontWeight: 600 }}>{t(`${D}action`)}</Typography>
            <ToggleButtonGroup exclusive disabled={locked} value={form.action} fullWidth={phone} onChange={(_, v: DestAction | null) => { if (v) setForm(f => ({ ...f, action: v, bittorrent: v === 'allow' ? false : f.bittorrent })) }}
              aria-label={t(`${D}action`)}>
              <ToggleButton value="block">{t('admin:access_control.policies.action.block')}</ToggleButton>
              <ToggleButton value="allow">{t('admin:access_control.policies.action.allow')}</ToggleButton>
            </ToggleButtonGroup>
            <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mt: 0.5 }}>{t(`${D}action_help.${form.action}`)}</Typography>
            {form.action === 'allow' && (
              <Box sx={{ mt: 0.5 }}><FieldHint tone="amber" summary={t(`${D}allow_hint.summary`)} detail={t(`${D}allow_hint.detail`)} /></Box>
            )}
          </Box>

          <Box>
            <Typography variant="body2" sx={{ mb: 0.5, fontWeight: 600 }}>{t(`${D}state`)}</Typography>
            <ToggleButtonGroup exclusive value={form.state} fullWidth={phone} onChange={(_, v: DestState | null) => { if (v) set('state', v) }}
              aria-label={t(`${D}state`)}>
              <ToggleButton value="off">{t('admin:access_control.policies.state.off')}</ToggleButton>
              <ToggleButton value="on">{t('admin:access_control.policies.state.on')}</ToggleButton>
            </ToggleButtonGroup>
          </Box>

          <Box sx={{ display: 'grid', gap: 1.5 }}>
            <Typography component="h3" sx={{ fontSize: 14, fontWeight: 600 }}>{t(`${D}match`)}</Typography>
            {locked && <FieldHint tone="muted" summary={t(`${D}template_locked.summary`)} detail={t(`${D}template_locked.detail`)} />}
            <Autocomplete multiple disabled={locked} options={allLists} value={form.listIds.map(id => listById.get(id)).filter((l): l is DestList => !!l)}
              loading={lists.isPending} getOptionLabel={l => l.name}
              getOptionDisabled={l => l.empty && l.kind === 'custom'}
              isOptionEqualToValue={(a, b) => a.id === b.id}
              onChange={(_, v) => set('listIds', v.map(l => l.id))}
              renderOption={(props, l) => {
                const { key: k, ...rest } = props as typeof props & { key: string }
                const pending = l.state === 'pending' || (l.state === 'failed' && l.last_fetched_at === null)
                return (
                  <li key={k} {...rest}>
                    <Box sx={{ display: 'flex', gap: 1, alignItems: 'center', flexWrap: 'wrap' }}>
                      <span>{t(`${D}list_option`, { name: l.name, kind: t(`admin:access_control.policies.list_kind.${l.kind}`), entries: num.format(l.entry_count), regexps: l.regexp_count })}</span>
                      {pending && <ToneBadge tone={accessTone(theme, 'attention')} label={t(`${D}list_not_ready`)} />}
                    </Box>
                  </li>
                )
              }}
              renderInput={params => <TextField {...params} label={t(`${D}lists`)} error={!!errors.list_ids} helperText={errors.list_ids} />} />
            {notReady.length > 0 && <FieldHint tone="amber" summary={t(`${D}not_ready_hint.summary`)} detail={t(`${D}not_ready_hint.detail`)} />}

            <Box>
              <Typography variant="body2" sx={{ mb: 0.5 }}>{t(`${D}cidrs`)}</Typography>
              <Suspense fallback={<Skeleton variant="rounded" height={128} />}>
                <CodeEditor value={form.cidrs} onChange={v => set('cidrs', v)} language="plain" minRows={6} readOnly={locked} />
              </Suspense>
              {errors.cidrs
                ? <Typography variant="caption" sx={{ color: md.error }}>{errors.cidrs}</Typography>
                : <FieldHint tone="muted" summary={t(`${D}cidr_hint.summary`)} detail={t(`${D}cidr_hint.detail`)} />}
            </Box>
            <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'flex-start' }}>
              <TextField label={t(`${D}ports`)} disabled={locked} value={form.ports} onChange={e => set('ports', e.target.value)}
                error={!!errors.ports} helperText={errors.ports ?? t(`${D}ports_hint`)} sx={{ flex: '1 1 200px' }} />
              <Box sx={{ flex: '1 1 200px' }}>
                <Typography variant="body2" sx={{ mb: 0.5 }}>{t(`${D}network`)}</Typography>
                <ToggleButtonGroup exclusive size="small" disabled={locked} value={form.network || 'all'} onChange={(_, v: string | null) => { if (v) set('network', v === 'all' ? '' : v as 'tcp' | 'udp') }}
                  aria-label={t(`${D}network`)}>
                  <ToggleButton value="all">{t(`${D}network_all`)}</ToggleButton>
                  <ToggleButton value="tcp">TCP</ToggleButton>
                  <ToggleButton value="udp">UDP</ToggleButton>
                </ToggleButtonGroup>
              </Box>
            </Box>
            <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
              <Box>
                <FormControlLabel control={<Checkbox disabled={locked || form.action === 'allow'} checked={form.bittorrent} onChange={(_, c) => set('bittorrent', c)} />} label={t(`${D}bittorrent`)} />
                <Typography variant="caption" sx={{ display: 'block', color: md.onSurfaceVariant, ml: 4 }}>{form.action === 'allow' ? t(`${D}bittorrent_block_only`) : t(`${D}bittorrent_hint`)}</Typography>
              </Box>
              <FormControlLabel control={<Checkbox disabled={locked} checked={form.private} onChange={(_, c) => set('private', c)} />} label={t(`${D}private`)} />
            </Box>
            {errors.match && touched && <Typography variant="body2" role="alert" sx={{ color: md.error }}>{errors.match}</Typography>}
          </Box>

          <Box sx={{ display: 'grid', gap: 1 }}>
            <Typography component="h3" sx={{ fontSize: 14, fontWeight: 600 }}>{t(`${D}scope`)}</Typography>
            <ToggleButtonGroup exclusive value={form.scope} fullWidth={phone} onChange={(_, v: DestScope | null) => { if (v) set('scope', v) }} aria-label={t(`${D}scope`)}>
              <ToggleButton value="all">{t(`${D}scope_all`)}</ToggleButton>
              <ToggleButton value="groups">{t(`${D}scope_groups`)}</ToggleButton>
              <ToggleButton value="panels">{t(`${D}scope_panels`)}</ToggleButton>
            </ToggleButtonGroup>
            {form.scope === 'groups' && (
              <Autocomplete multiple options={groupOptions} getOptionLabel={g => g.name} isOptionEqualToValue={(a, b) => a.id === b.id}
                value={groupOptions.filter(g => form.groupIds.includes(g.id))} onChange={(_, v) => set('groupIds', v.map(g => g.id))}
                renderInput={params => <TextField {...params} label={t(`${D}groups`)} error={!!errors.group_ids || !!errors.scope} helperText={errors.group_ids ?? errors.scope} />} />
            )}
            {form.scope === 'panels' && (
              <Autocomplete multiple options={panelOptions} getOptionLabel={s => s.name} isOptionEqualToValue={(a, b) => a.id === b.id}
                value={panelOptions.filter(s => form.panelIds.includes(s.id))} onChange={(_, v) => set('panelIds', v.map(s => s.id))}
                renderInput={params => <TextField {...params} label={t(`${D}panels`)} error={!!errors.panel_ids || !!errors.scope} helperText={errors.panel_ids ?? errors.scope} />} />
            )}
            {removed > 0 && <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${D}removed_deleted`, { count: removed })}</Typography>}
          </Box>

          <QuotaMeters variant="full" budget={preview.budget ?? undefined} loading={preview.loading} failed={false} />
          {preview.failed && (
            <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>
              {t(`admin:access_control.policies.quota.failed`)} <Button size="small" onClick={() => setRetry(n => n + 1)}>{t('admin:access_control.retry')}</Button>
            </Typography>
          )}
        </fieldset>
      </DialogContent>
      <DialogActions sx={{ flexDirection: 'column', alignItems: 'stretch', gap: 1, p: 2, position: phone ? 'sticky' : undefined, bottom: 0, bgcolor: md.surfaceContainerHigh }}>
        <Box>
          <Typography variant="body2" data-testid="editor-summary" sx={{ fontWeight: 600 }}>
            {[t(`admin:access_control.policies.action.${form.action}`), summary, t(`${D}position`, { step: position.step, index: position.index })].filter(Boolean).join(' · ')}
          </Typography>
          {split && <Typography variant="caption" sx={{ display: 'block', color: md.onSurfaceVariant }}>{t(`${D}split`)}</Typography>}
          <Typography variant="caption" sx={{ display: 'block', color: md.onSurfaceVariant }}>{footer}</Typography>
        </Box>
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1 }}>
          <Button onClick={() => guard(onClose)}>{t(`${D}cancel`)}</Button>
          <AsyncButton variant="contained" onClick={save} disabled={over || noScopePick || descTooLong || nameTooLong || (!!policy && !dirty)}>{t(`${D}save`)}</AsyncButton>
        </Box>
      </DialogActions>
    </Dialog>
  )
}
