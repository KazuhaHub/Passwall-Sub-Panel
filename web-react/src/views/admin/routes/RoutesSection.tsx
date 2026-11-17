import { useEffect, useMemo, useRef, useState, type Dispatch, type SetStateAction } from 'react'
import {
  Alert, Autocomplete, Box, Button, Chip, IconButton, LinearProgress, MenuItem, TextField, Tooltip, Typography, useTheme,
} from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import ArrowDownwardIcon from '@mui/icons-material/ArrowDownward'
import ArrowUpwardIcon from '@mui/icons-material/ArrowUpward'
import DeleteIcon from '@mui/icons-material/DeleteOutlined'
import { useTranslation } from 'react-i18next'
import { checkRoutes, type RouteBudget, type RouteLink, type RouteView, type RouteWarning, type RoutesView } from '@/api/routes'
import type { DestList } from '@/api/accessControl'
import { confirm } from '@/components/ConfirmHost'
import InfoTip from '@/components/InfoTip'
import { pushSnack } from '@/components/SnackbarHost'
import { useDestLists } from '@/query/accessControl'
import { useNodeRoutes, useSaveRoutes } from '@/query/routes'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatDualTz } from '@/utils/datetime'
import RouteLinkStatus from './RouteLinkStatus'
import RouteTunnelSettings from './RouteTunnelSettings'
import {
  draftIssues, draftOf, emptyRoute, inputOf, isDirty, moveRoute, targetKey, type DraftRoute,
} from './routeDraft'
import { issueText, noteText, routeErrorText, supportText, warningText, type RouteLabeler } from './routeErrors'

const K = 'admin:nodes.routes.'
const CHECK_DEBOUNCE_MS = 600
const NO_ROWS: DraftRoute[] = []

export interface RouteServerOption { id: number; name: string }

interface TargetOption { key: string; label: string }

const sameRoutes = (a: readonly RouteView[], b: readonly RouteView[]) =>
  JSON.stringify(inputOf(draftOf(a))) === JSON.stringify(inputOf(draftOf(b)))

/**
 * A node's routes by destination: an ordered list, first match wins, each
 * sending its destinations out through another server or keeping them on this
 * node's own server. Everything no route matches leaves through the default
 * exit, which is edited elsewhere in the node editor and shown here for
 * context. The section has its own Save: the routes are a separate write.
 */
export default function RoutesSection({ nodeId, entryPanelId, servers, pendingExit = 0, defaultExitNodesOf, setup }: {
  nodeId: number
  /** The node's own server: the entry end of every tunnel. */
  entryPanelId: number
  /** Passwall Node servers a route may target; the node's own server is not among them. */
  servers: RouteServerOption[]
  /** The default exit chosen in the editor and not saved yet (0: this server). */
  pendingExit?: number
  /** Names of the nodes on the entry server that use this exit server as their default exit. */
  defaultExitNodesOf?: (exitPanelId: number) => string[]
  setup?: { rows: DraftRoute[]; setRows: Dispatch<SetStateAction<DraftRoute[]>> }
}) {
  const { t } = useTranslation(['admin', 'common'])
  const panelTz = useSiteStore(s => s.timezone)
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const q = useNodeRoutes(scope, nodeId, !setup)
  const lists = useDestLists(scope)
  const save = useSaveRoutes(scope, nodeId)
  const data: RoutesView | undefined = setup ? {
    supported: true, routes: [], sent: false, running: false, warnings: [],
    default_exit: pendingExit ? { kind: 'server', panel_id: pendingExit } : { kind: 'local' },
  } : q.data

  const [savedDraft, setSavedDraft] = useState<DraftRoute[] | null>(null)
  const draft = setup?.rows ?? savedDraft
  const setDraft: typeof setSavedDraft = setup
    ? update => setup.setRows(previous => (typeof update === 'function' ? update(previous) : update) ?? [])
    : setSavedDraft
  const base = useRef<RouteView[]>([])
  // Adopt what the server holds when the editor opens and whenever it changes
  // while the admin has no edits of their own; edits are never overwritten.
  useEffect(() => {
    if (setup || !data) return
    if (draft === null || (!isDirty(draft, base.current) && !sameRoutes(data.routes, base.current))) setDraft(draftOf(data.routes))
    base.current = data.routes
  // The draft is read, not a trigger: only a new server answer re-adopts.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data])

  const rows = draft ?? NO_ROWS
  const dirty = !setup && !!data && draft !== null && isDirty(draft, data.routes)
  const issues = useMemo(() => draftIssues(rows), [rows])
  const supported = data?.supported ?? false

  // The dry run: warnings and the budget for the draft before it is saved.
  const [check, setCheck] = useState<RoutesView | null>(null)
  const [checkFailed, setCheckFailed] = useState('')
  const [saved, setSaved] = useState<{ warnings: RouteWarning[]; budget?: RouteBudget } | null>(null)
  const [saveError, setSaveError] = useState('')
  const draftJson = JSON.stringify(inputOf(rows))
  useEffect(() => {
    // The save's own warnings and refusal stay until the admin edits again.
    if (dirty) { setSaved(null); setSaveError('') }
    if (!dirty || !supported || issues.length > 0) { setCheck(null); setCheckFailed(''); return }
    const controller = new AbortController()
    const timer = setTimeout(() => {
      checkRoutes(nodeId, inputOf(rows), { signal: controller.signal })
        .then(view => { if (!controller.signal.aborted) { setCheck(view); setCheckFailed('') } })
        .catch(err => { if (!controller.signal.aborted) { setCheck(null); setCheckFailed(routeErrorText(t, err, labelOf)) } })
    }, CHECK_DEBOUNCE_MS)
    return () => { controller.abort(); clearTimeout(timer) }
  // The JSON of the draft is the trigger; rows and labelOf follow from it.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draftJson, dirty, supported, issues.length, nodeId])

  const options: TargetOption[] = useMemo(() => [
    { key: targetKey.local, label: t(`${K}target.local`) as string },
    ...servers.map(s => ({ key: targetKey.server(s.id), label: s.name })),
  ], [servers, t])
  const serverName = (id: number, link?: RouteLink) =>
    servers.find(s => s.id === id)?.name ?? link?.server_name ?? `#${id}`
  const optionOf = (key: string): TargetOption | null => {
    if (!key) return null
    const found = options.find(o => o.key === key)
    if (found) return found
    const m = /^server:(\d+)$/.exec(key)
    // A target that is no longer on the list (a server removed): still named, so it is not shown as empty.
    return m ? { key, label: serverName(Number(m[1]), data?.routes.find(r => r.target.kind === 'server' && targetKey.server(r.target.panel_id) === key)?.link) } : null
  }

  const destLists = (lists.data?.items ?? []).filter(l => l.owner_group_id === 0 && l.side === 'dest')
  const listById = new Map(destLists.map(l => [l.id, l]))

  // Rows are named by position, with the label when there is one; the server
  // refers to routes by id.
  const labelOf: RouteLabeler = ref => {
    if (typeof ref === 'number') return `#${ref}`
    const i = rows.findIndex(r => r.id === ref)
    if (i < 0) return String(ref ?? '')
    return rows[i].name.trim() ? `#${i + 1} ${rows[i].name.trim()}` : `#${i + 1}`
  }

  const patch = (key: string, p: Partial<DraftRoute>) => setDraft(d => (d ?? []).map(r => (r.key === key ? { ...r, ...p } : r)))

  async function doSave(routes: DraftRoute[]) {
    setSaveError('')
    try {
      const view = await save.mutateAsync(inputOf(routes))
      base.current = view.routes
      setDraft(draftOf(view.routes))
      setSaved({ warnings: view.warnings, budget: view.budget })
      pushSnack(t(`${K}saved`), 'success')
    } catch (err) {
      setSaveError(routeErrorText(t, err, labelOf))
    }
  }

  async function clearAll() {
    const ok = await confirm({
      title: t(`${K}clear_title`), message: t(`${K}clear_message`, { count: data?.routes.length ?? 0 }),
      destructive: true, confirmText: t(`${K}clear`),
    })
    if (ok) await doSave([])
  }

  const header = (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
      <Typography sx={{ fontSize: 14, fontWeight: 600 }}>{t(`${K}title`)}</Typography>
      <InfoTip text={t(`${K}help.what`) as string} />
    </Box>
  )

  if (!setup && q.isPending) return <Box data-testid="routes-section"><Box sx={{ mb: 1 }}>{header}</Box><LinearProgress /></Box>
  if ((!setup && q.isError) || !data) {
    return (
      <Box data-testid="routes-section" sx={{ display: 'grid', gap: 1 }}>
        {header}
        <Alert severity="error" action={<Button size="small" color="inherit" onClick={() => q.refetch()}>{t('admin:access_control.retry')}</Button>}>
          {t(`${K}load_failed`)}
        </Alert>
      </Box>
    )
  }

  const dirtyView = dirty && check && check.routes.length === rows.length ? check : null
  const infoOf = (row: DraftRoute, i: number): RouteView | undefined =>
    dirtyView ? dirtyView.routes[i] : data.routes.find(r => r.id === row.id)
  const warnings = dirty ? (check?.warnings ?? []) : (saved?.warnings ?? data.warnings)
  const budget = dirty ? (check?.budget ?? data.budget) : (saved?.budget ?? data.budget)
  const defaultName = data.default_exit.kind === 'local' ? t(`${K}exit.local`) as string : serverName(data.default_exit.panel_id, data.default_link)
  const savedExit = data.default_exit.kind === 'server' ? data.default_exit.panel_id : 0
  // One tunnel block per distinct exit server a saved route uses (an exit that no longer exists has no tunnel to edit).
  const exitIds = [...new Set(data.routes.flatMap(r => (r.target.kind === 'server' && !r.link?.gone ? [r.target.panel_id] : [])))]
  const canSave = supported && dirty && issues.length === 0 && !save.isPending
  const blocked = !supported

  const appliedState = !data.sent ? 'not_sent' : data.running ? 'running' : 'waiting'
  const at = (iso?: string) => (iso ? formatDualTz(iso, panelTz) : '')

  return (
    <Box data-testid="routes-section" sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1, flexWrap: 'wrap' }}>
        {header}
        <Box sx={{ display: 'flex', gap: 1, alignItems: 'center' }}>
          {dirty && <Chip data-testid="routes-unsaved" size="small" color="warning" variant="outlined" label={t(`${K}unsaved`)} />}
          <Button size="small" startIcon={<AddIcon />} disabled={blocked || save.isPending}
            onClick={() => setDraft(d => [...(d ?? []), emptyRoute()])}>{t(`${K}add`)}</Button>
        </Box>
      </Box>
      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t(`${K}hint`)}</Typography>

      {blocked && (
        <Alert severity="info" data-testid="routes-unsupported" icon={false}
          action={<InfoTip text={t(`${K}help.unsupported`) as string} />}>
          {supportText(t, data.reason)}
        </Alert>
      )}

      {!setup && <Box sx={{ border: `1px solid ${md.outlineVariant}`, borderRadius: 2, p: 1.5, display: 'grid', gap: 0.75 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, flexWrap: 'wrap' }}>
          <Typography data-testid="routes-default-exit" sx={{ fontSize: 13, fontWeight: 600 }}>
            {t(`${K}default_exit`, { name: defaultName })}
          </Typography>
          <InfoTip text={t(`${K}help.default_exit`) as string} />
        </Box>
        {data.default_link && <RouteLinkStatus link={data.default_link} testId="routes-default-link" />}
        {pendingExit !== savedExit && (
          <Typography data-testid="routes-exit-pending" sx={{ fontSize: 12, color: md.tertiary }}>{t(`${K}default_exit_pending`)}</Typography>
        )}
        <Typography data-testid="routes-applied" data-state={appliedState} sx={{ fontSize: 12, color: appliedState === 'running' ? md.primary : md.onSurfaceVariant }}>
          {t(`${K}applied.${appliedState}`)}
          {appliedState === 'waiting' && data.sent_at ? ` · ${t(`${K}applied.sent_at`, { time: at(data.sent_at) })}` : ''}
          {appliedState === 'running' && data.applied_at ? ` · ${t(`${K}applied.applied_at`, { time: at(data.applied_at) })}` : ''}
        </Typography>
      </Box>}

      {rows.length === 0 && (
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, fontStyle: 'italic' }}>{t(`${K}empty`)}</Typography>
      )}

      {rows.map((row, i) => {
        const info = infoOf(row, i)
        const rowIssues = issues.filter(x => x.route === i + 1)
        const notes = info?.notes ?? []
        const link = info?.link
        const target = optionOf(row.target)
        return (
          <Box key={row.key} data-testid="route-row" sx={{ border: `1px solid ${md.outlineVariant}`, borderRadius: 2, p: 1.5, display: 'flex', flexDirection: 'column', gap: 1 }}>
            <Box sx={{ display: 'flex', gap: 1, alignItems: 'center' }}>
              <Typography sx={{ fontSize: 13, fontWeight: 600, minWidth: 28 }}>#{i + 1}</Typography>
              <TextField size="small" label={t(`${K}name`)} value={row.name} disabled={blocked} sx={{ flex: 1 }}
                onChange={e => patch(row.key, { name: e.target.value })} />
              <Tooltip title={t(`${K}move_up`)}><span>
                <IconButton size="small" aria-label={t(`${K}move_up`)} data-testid={`route-up-${i}`} disabled={blocked || i === 0}
                  onClick={() => setDraft(d => moveRoute(d ?? [], i, i - 1))}><ArrowUpwardIcon fontSize="small" /></IconButton>
              </span></Tooltip>
              <Tooltip title={t(`${K}move_down`)}><span>
                <IconButton size="small" aria-label={t(`${K}move_down`)} data-testid={`route-down-${i}`} disabled={blocked || i === rows.length - 1}
                  onClick={() => setDraft(d => moveRoute(d ?? [], i, i + 1))}><ArrowDownwardIcon fontSize="small" /></IconButton>
              </span></Tooltip>
              <Tooltip title={t(`${K}remove`)}><span>
                <IconButton size="small" aria-label={t(`${K}remove`)} data-testid={`route-remove-${i}`} disabled={blocked}
                  onClick={() => setDraft(d => (d ?? []).filter(r => r.key !== row.key))}><DeleteIcon fontSize="small" /></IconButton>
              </span></Tooltip>
            </Box>

            <Autocomplete multiple size="small" disabled={blocked} options={destLists}
              value={row.listIds.map(id => listById.get(id)).filter((l): l is DestList => !!l)}
              loading={lists.isPending} getOptionLabel={l => l.name}
              isOptionEqualToValue={(a, b) => a.id === b.id}
              onChange={(_, v) => patch(row.key, { listIds: v.map(l => l.id) })}
              renderOption={(props, l) => {
                const { key: k, ...rest } = props as typeof props & { key: string }
                return <li key={k} {...rest}>{t(`${K}list_option`, { name: l.name, kind: t(`admin:access_control.policies.list_kind.${l.kind}`), entries: l.entry_count })}</li>
              }}
              renderInput={params => <TextField {...params} label={t(`${K}lists`)} helperText={t(`${K}lists_hint`)} />} />
            <TextField size="small" multiline minRows={2} disabled={blocked} label={t(`${K}domains`)} value={row.domains}
              placeholder={'domain:example.com\nfull:www.example.com\nkeyword:ads'}
              helperText={t(`${K}domains_hint`)} onChange={e => patch(row.key, { domains: e.target.value })} />
            <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap' }}>
              <TextField size="small" multiline minRows={2} disabled={blocked} label={t(`${K}cidrs`)} value={row.cidrs}
                sx={{ flex: '1 1 200px' }} onChange={e => patch(row.key, { cidrs: e.target.value })} />
              <TextField size="small" disabled={blocked} label={t(`${K}ports`)} value={row.ports} sx={{ flex: '1 1 160px' }}
                placeholder="25,465,587" helperText={t(`${K}ports_hint`)} onChange={e => patch(row.key, { ports: e.target.value })} />
              <TextField select size="small" disabled={blocked} label={t(`${K}network`)} value={row.network} sx={{ flex: '0 1 140px' }}
                onChange={e => patch(row.key, { network: e.target.value as DraftRoute['network'] })}>
                <MenuItem value="">{t(`${K}network_any`)}</MenuItem>
                <MenuItem value="tcp">TCP</MenuItem>
                <MenuItem value="udp">UDP</MenuItem>
              </TextField>
            </Box>

            <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
              <Autocomplete size="small" disabled={blocked} options={options} value={target} sx={{ flex: 1 }}
                getOptionLabel={o => o.label} isOptionEqualToValue={(a, b) => a.key === b.key}
                onChange={(_, o) => patch(row.key, { target: o?.key ?? '' })}
                renderInput={params => <TextField {...params} label={t(`${K}target.label`)} helperText={row.target === targetKey.local ? t(`${K}target.local_hint`) : undefined} />} />
              <InfoTip text={t(`${K}help.local`) as string} />
            </Box>

            <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 1 }}>
              {link && <RouteLinkStatus link={link} testId="route-link" />}
              {info && (
                <Typography component="span" data-testid="route-entries" sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
                  {t(`${K}entries`, { count: info.entries })}
                </Typography>
              )}
              {dirty
                ? <Chip data-testid="route-sent" data-state="unsaved" size="small" variant="outlined" label={t(`${K}sent.unsaved`)} />
                : info && <Chip data-testid="route-sent" data-state={info.sent ? 'sent' : 'not_sent'} size="small" variant="outlined"
                  color={info.sent ? 'default' : 'warning'} label={t(info.sent ? `${K}sent.sent` : `${K}sent.not_sent`)} />}
            </Box>
            {notes.map((n, j) => (
              <Typography key={j} data-testid="route-note" data-code={n.code} sx={{ fontSize: 12, color: md.tertiary }}>{noteText(t, n)}</Typography>
            ))}
            {rowIssues.map((x, j) => (
              <Typography key={j} data-testid="route-issue" sx={{ fontSize: 12, color: md.error }}>{issueText(t, x, labelOf)}</Typography>
            ))}
          </Box>
        )
      })}

      {exitIds.length > 0 && (
        <Box data-testid="route-tunnels" sx={{ display: 'grid', gap: 1 }}>
          {exitIds.map(id => <RouteTunnelSettings key={id} entry={entryPanelId} exit={id} name={serverName(id, data.routes.find(r => r.link?.panel_id === id)?.link)}
            defaultExitNodes={defaultExitNodesOf?.(id) ?? []} />)}
        </Box>
      )}

      {budget && (
        <Box data-testid="routes-budget" sx={{ display: 'grid', gap: 0.5 }}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
            <Typography sx={{ fontSize: 13, fontWeight: 600 }}>{t(`${K}budget.title`, { engine: budget.engine })}</Typography>
            <InfoTip text={t(`${K}help.xray`) as string} />
          </Box>
          {(['entries', 'rules'] as const).map(k => {
            const used = budget[k]
            const max = budget[k === 'entries' ? 'max_entries' : 'max_rules']
            return (
              <Box key={k} data-testid={`routes-budget-${k}`}>
                <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t(`${K}budget.${k}`, { used, max })}</Typography>
                <LinearProgress variant="determinate" value={max > 0 ? Math.min(100, (used / max) * 100) : 0}
                  color={max > 0 && used / max >= 0.9 ? 'warning' : 'primary'} aria-label={t(`${K}budget.${k}`, { used, max })} />
              </Box>
            )
          })}
          <Typography data-testid="routes-budget-bytes" sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
            {t(`${K}budget.bytes`, { used: budget.bytes, max: budget.max_bytes })}
          </Typography>
        </Box>
      )}

      {checkFailed && <Alert severity="error" data-testid="routes-check-error">{checkFailed}</Alert>}
      {warnings.map((w, i) => (
        <Alert key={`${w.code}:${i}`} severity="warning" data-testid="route-warning" data-code={w.code}>{warningText(t, w, labelOf)}</Alert>
      ))}
      {saveError && <Alert severity="error" data-testid="routes-save-error">{saveError}</Alert>}

      {!setup && <Box sx={{ display: 'flex', gap: 1, justifyContent: 'flex-end', flexWrap: 'wrap' }}>
        <Button size="small" color="error" data-testid="routes-clear" disabled={save.isPending || (data.routes.length === 0 && rows.length === 0)}
          onClick={() => void clearAll()}>{t(`${K}clear`)}</Button>
        <Button size="small" disabled={!dirty || save.isPending} onClick={() => setDraft(draftOf(data.routes))}>{t(`${K}revert`)}</Button>
        <Button size="small" variant="contained" data-testid="routes-save" disabled={!canSave} onClick={() => void doSave(rows)}>{t(`${K}save`)}</Button>
      </Box>}
    </Box>
  )
}
