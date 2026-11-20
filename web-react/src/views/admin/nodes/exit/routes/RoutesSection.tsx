import { useEffect, useMemo, useRef } from 'react'
import { IconAdd } from '@/lib/icons'
import type { RouteLink, RouteView, RoutesView } from '@/api/routes'
import { confirm } from '@/components/ConfirmHost'
import { InfoTip } from '@/components/common/HelpTip'
import { StatusBadge } from '@/components/common/StatusBadge'
import { ErrorState } from '@/components/feedback/ErrorState'
import { LoadingState } from '@/components/feedback/LoadingState'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useDestLists } from '@/query/accessControl'
import { useNodeRoutes } from '@/query/routes'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatDualTz } from '@/utils/datetime'
import { useNodesT } from '../../shared/useNodesT'
import { RouteBudget } from './RouteBudget'
import { RouteLinkStatus } from './RouteLinkStatus'
import { RouteRow, type TargetOption } from './RouteRow'
import { RouteTunnelSettings } from './RouteTunnelSettings'
import { draftOf, emptyRoute, moveRoute, targetKey, type DraftRoute } from './routeDraft'
import { supportText, warningText } from './routeErrors'
import { useRoutesDraft, type SetupRows } from './useRoutesDraft'

const K = 'nodes.routes.'

export interface RouteServerOption { id: number; name: string }

/**
 * A node's routes by destination: an ordered list, first match wins, each sending its destinations out through
 * another server or keeping them on this node's own server. Everything no route matches leaves through the default
 * exit, which is edited elsewhere in the node editor and shown here for context. The section has its own Save: the
 * routes are a separate write.
 */
export function RoutesSection({ nodeId, entryPanelId, servers, pendingExit = 0, defaultExitNodesOf, setup, onDirtyChange }: {
  nodeId: number
  /** The node's own server: the entry end of every tunnel. */
  entryPanelId: number
  /** Passwall Node servers a route may target; the node's own server is not among them. */
  servers: RouteServerOption[]
  /** The default exit chosen in the editor and not saved yet (0: this server). */
  pendingExit?: number
  /** Names of the nodes on the entry server that use this exit server as their default exit. */
  defaultExitNodesOf?: (exitPanelId: number) => string[]
  /** The create wizard holds the rows itself: nothing is fetched and there is no Save here. */
  setup?: SetupRows
  /** Called whenever the draft gains or loses unsaved changes, and with false when the section goes away. */
  onDirtyChange?: (dirty: boolean) => void
}) {
  const t = useNodesT()
  const panelTz = useSiteStore(s => s.timezone)
  const scope = useQueryScope()
  const q = useNodeRoutes(scope, nodeId, !setup)
  const lists = useDestLists(scope)
  const data: RoutesView | undefined = setup ? {
    supported: true, routes: [], sent: false, running: false, warnings: [],
    default_exit: pendingExit ? { kind: 'server', panel_id: pendingExit } : { kind: 'local' },
  } : q.data
  const { rows, setDraft, dirty, issues, supported, labelOf, check, checkFailed, saved, saveError, saving, doSave } = useRoutesDraft({ nodeId, data, setup })

  // The page guards leaving with this; the ref keeps an inline callback from re-firing the effect.
  const notify = useRef(onDirtyChange)
  useEffect(() => { notify.current = onDirtyChange })
  useEffect(() => { notify.current?.(dirty) }, [dirty])
  useEffect(() => () => notify.current?.(false), [])

  const serverName = (id: number, link?: RouteLink) => servers.find(s => s.id === id)?.name ?? link?.server_name ?? `#${id}`
  const targets: TargetOption[] = useMemo(() => [
    { key: targetKey.local, label: t(`${K}target.local`) as string },
    ...servers.map(s => ({ key: targetKey.server(s.id), label: s.name })),
  ], [servers, t])
  // A target that is no longer on the list (a server removed) is still named, so it is not shown as empty.
  const targetsFor = (row: DraftRoute): TargetOption[] => {
    const m = /^server:(\d+)$/.exec(row.target)
    if (!m || targets.some(o => o.key === row.target)) return targets
    const link = data?.routes.find(r => r.target.kind === 'server' && targetKey.server(r.target.panel_id) === row.target)?.link
    return [...targets, { key: row.target, label: serverName(Number(m[1]), link) }]
  }
  const destLists = (lists.data?.items ?? []).filter(l => l.owner_group_id === 0 && l.side === 'dest')

  async function clearAll() {
    const ok = await confirm({
      title: t(`${K}clear_title`), message: t(`${K}clear_message`, { count: data?.routes.length ?? 0 }),
      destructive: true, confirmText: t(`${K}clear`),
    })
    if (ok) await doSave([])
  }

  const header = (
    <div className="flex items-center gap-1">
      <h3 className="text-sm font-semibold">{t(`${K}title`)}</h3>
      <InfoTip text={t(`${K}help.what`)} />
    </div>
  )

  if (!setup && q.isPending) return <div data-testid="routes-section" className="grid gap-2">{header}<LoadingState /></div>
  if ((!setup && q.isError) || !data) {
    return (
      <div data-testid="routes-section" className="grid gap-2">
        {header}
        <ErrorState compact title={t(`${K}load_failed`)} reason={q.error} retry={() => q.refetch()} />
      </div>
    )
  }

  const dirtyView = dirty && check && check.routes.length === rows.length ? check : null
  const infoOf = (row: DraftRoute, i: number): RouteView | undefined => (dirtyView ? dirtyView.routes[i] : data.routes.find(r => r.id === row.id))
  const warnings = dirty ? (check?.warnings ?? []) : (saved?.warnings ?? data.warnings)
  const budget = dirty ? (check?.budget ?? data.budget) : (saved?.budget ?? data.budget)
  const defaultName = data.default_exit.kind === 'local' ? t(`${K}exit.local`) as string : serverName(data.default_exit.panel_id, data.default_link)
  const savedExit = data.default_exit.kind === 'server' ? data.default_exit.panel_id : 0
  // One tunnel block per distinct exit server a saved route uses (an exit that no longer exists has no tunnel to edit).
  const exitIds = [...new Set(data.routes.flatMap(r => (r.target.kind === 'server' && !r.link?.gone ? [r.target.panel_id] : [])))]
  const blocked = !supported
  const canSave = supported && dirty && issues.length === 0 && !saving
  const appliedState = !data.sent ? 'not_sent' : data.running ? 'running' : 'waiting'
  const at = (iso?: string) => (iso ? formatDualTz(iso, panelTz) : '')

  return (
    <div data-testid="routes-section" className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        {header}
        <div className="flex items-center gap-2">
          {dirty && <span data-testid="routes-unsaved"><StatusBadge tone="warning" label={t(`${K}unsaved`)} /></span>}
          <Button type="button" variant="outline" size="sm" disabled={blocked || saving} onClick={() => setDraft(d => [...(d ?? []), emptyRoute()])}><IconAdd />{t(`${K}add`)}</Button>
        </div>
      </div>
      <p className="text-xs text-muted-foreground">{t(`${K}hint`)}</p>

      {blocked && (
        <Alert data-testid="routes-unsupported">
          <AlertDescription className="flex items-start gap-1">
            <span>{supportText(t, data.reason)}</span>
            <InfoTip text={t(`${K}help.unsupported`)} />
          </AlertDescription>
        </Alert>
      )}

      {!setup && (
        <div className="grid gap-1.5 rounded-lg border p-3">
          <div className="flex flex-wrap items-center gap-1">
            <span data-testid="routes-default-exit" className="text-sm font-medium">{t(`${K}default_exit`, { name: defaultName })}</span>
            <InfoTip text={t(`${K}help.default_exit`)} />
          </div>
          {data.default_link && <RouteLinkStatus link={data.default_link} testId="routes-default-link" />}
          {pendingExit !== savedExit && <p data-testid="routes-exit-pending" className="text-xs text-warning-ink">{t(`${K}default_exit_pending`)}</p>}
          <p data-testid="routes-applied" data-state={appliedState} className="text-xs text-muted-foreground">
            {t(`${K}applied.${appliedState}`)}
            {appliedState === 'waiting' && data.sent_at ? ` · ${t(`${K}applied.sent_at`, { time: at(data.sent_at) })}` : ''}
            {appliedState === 'running' && data.applied_at ? ` · ${t(`${K}applied.applied_at`, { time: at(data.applied_at) })}` : ''}
          </p>
        </div>
      )}

      {rows.length === 0 && <p className="text-xs text-muted-foreground italic">{t(`${K}empty`)}</p>}
      {rows.map((row, i) => (
        <RouteRow key={row.key} index={i} count={rows.length} row={row} info={infoOf(row, i)} issues={issues.filter(x => x.route === i + 1)}
          blocked={blocked} dirty={dirty} lists={destLists} targets={targetsFor(row)} serverName={id => serverName(id)} labelOf={labelOf}
          onPatch={p => setDraft(d => (d ?? []).map(r => (r.key === row.key ? { ...r, ...p } : r)))}
          onMove={delta => setDraft(d => moveRoute(d ?? [], i, i + delta))}
          onRemove={() => setDraft(d => (d ?? []).filter(r => r.key !== row.key))} />
      ))}

      {exitIds.length > 0 && (
        <div data-testid="route-tunnels" className="grid gap-2">
          {exitIds.map(id => (
            <RouteTunnelSettings key={id} entry={entryPanelId} exit={id} name={serverName(id, data.routes.find(r => r.link?.panel_id === id)?.link)}
              defaultExitNodes={defaultExitNodesOf?.(id) ?? []} />
          ))}
        </div>
      )}

      {budget && <RouteBudget budget={budget} />}

      {checkFailed && <Alert variant="destructive" data-testid="routes-check-error"><AlertDescription>{checkFailed}</AlertDescription></Alert>}
      {warnings.map((w, i) => (
        <Alert key={`${w.code}:${i}`} data-testid="route-warning" data-code={w.code}><AlertDescription>{warningText(t, w, labelOf)}</AlertDescription></Alert>
      ))}
      {saveError && <Alert variant="destructive" data-testid="routes-save-error"><AlertDescription>{saveError}</AlertDescription></Alert>}

      {!setup && (
        <div className="flex flex-wrap justify-end gap-2">
          <Button type="button" variant="destructive" data-testid="routes-clear" disabled={saving || (data.routes.length === 0 && rows.length === 0)} onClick={() => void clearAll()}>{t(`${K}clear`)}</Button>
          <Button type="button" variant="outline" disabled={!dirty || saving} onClick={() => setDraft(draftOf(data.routes))}>{t(`${K}revert`)}</Button>
          <Button type="button" data-testid="routes-save" disabled={!canSave} onClick={() => void doSave(rows)}>{t(`${K}save`)}</Button>
        </div>
      )}
    </div>
  )
}
