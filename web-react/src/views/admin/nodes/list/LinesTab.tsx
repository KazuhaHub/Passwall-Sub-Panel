import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { Link, useNavigate } from 'react-router'
import type { OrderRow } from '@/api/nodes'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { HelpTip } from '@/components/common/HelpTip'
import { KpiGrid, KpiTile } from '@/components/common/KpiTile'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { DataTable } from '@/components/data/DataTable'
import type { DataTableHandle } from '@/components/data/dataTableTypes'
import { FacetFilter } from '@/components/data/FacetFilter'
import { FilterBar } from '@/components/data/FilterBar'
import { useRecordNav } from '@/components/data/useRecordNav'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { TableSkeleton } from '@/components/feedback/LoadingState'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { useTableViewState } from '@/hooks/useTableViewState'
import { urlCodecs, useUrlStates } from '@/hooks/useUrlState'
import { useUrlSelection } from '@/hooks/useUrlSelection'
import { useSeparators } from '@/query/nodes'
import { useSiteStore } from '@/stores/site'
import { useCan } from '@/utils/permissions'
import { LineEditor } from '../lines/LineEditor'
import { useGuardedEditor } from '../lines/useGuardedEditor'
import { useLineActions } from '../lines/useLineActions'
import { useNodeDeletion } from '../delete/useNodeDeletion'
import { TopologyView } from '../graph/TopologyView'
import type { PathLabels } from '../shared/pathOf'
import { useNodesT } from '../shared/useNodesT'
import { BulkNodeActions } from './BulkNodeActions'
import { LineCard } from './LineCard'
import { LineDialog } from './LineDialog'
import { MoveToDialog } from './MoveToDialog'
import { SeparatorDialog } from './SeparatorDialog'
import { StaleOrderDialog } from './StaleOrderDialog'
import { SeparatorRowCell } from './cells'
import { orderColumns, rowStateOf, treeColumns } from './columns'
import { buildTree, facetOptions, filterOrder, isFiltered, kpisOf, landingColorsOf, relatedIds, type ListFilters } from './listModel'
import { rowId } from './orderMoves'
import { rowMenuItems, type RowActionCaps } from './rowActions'
import type { ListRow, TreeItem } from './types'
import { useNodeList } from './useNodeList'
import { useOrderMoves } from './useOrderMoves'
import { useRowHandlers } from './useRowHandlers'

const VIEWS = ['order', 'node', 'graph'] as const
const MAX_ORDER_ROWS = 10_000
const FACETS = ['type', 'status', 'entry', 'landing'] as const
const NONE: string[] = []
const WITHHELD_REASONS = ['hidden_direct', 'landing_disabled', 'disabled', 'egress', 'probe', 'source_guard', 'waiting_entry', 'cdn_unfit']

/** The nodes list (design section 2): the subscription order and the by-node view over one data set, with reordering. */
export default function LinesTab() {
  const t = useNodesT()
  const admin = useCan('config.write')
  const tz = useSiteStore(s => s.timezone)
  const actions = useLineActions()
  const guard = useGuardedEditor()
  const navigate = useNavigate()
  const { scope, order, nodes, serverMap, rows, items, nodeMap } = useNodeList()
  const separators = useSeparators(scope)
  const moves = useOrderMoves()
  const [url, setUrl] = useUrlStates({ view: urlCodecs.oneOf(VIEWS, 'order'), q: urlCodecs.string(), edit: urlCodecs.bool() })
  const lineSel = useUrlSelection('line')
  const sepSel = useUrlSelection('sep')
  const view = useTableViewState('admin.nodes.lines')
  const [checked, setChecked] = useState<Record<string, boolean>>({})
  const [hover, setHover] = useState<string | null>(null)
  const tableRef = useRef<DataTableHandle>(null)
  const [moveTarget, setMoveTarget] = useState<OrderRow | null>(null)
  const [added, setAdded] = useState<string[]>([])

  const filters: ListFilters = {
    q: url.q, type: view.state.filters.type ?? NONE, status: view.state.filters.status ?? NONE,
    entry: view.state.filters.entry ?? NONE, landing: view.state.filters.landing ?? NONE,
  }
  const filtered = isFiltered(filters)
  const setFacet = (id: (typeof FACETS)[number]) => (next: string[]) => view.setFilters({ ...view.state.filters, [id]: next })
  const clearAll = () => { view.setFilters({}); setUrl({ q: '' }) }

  const colors = useMemo(() => landingColorsOf(rows), [rows])
  const labels = useMemo<PathLabels>(() => ({ external: t('nodes.type.external'), link: t('nodes.type.link'), deleted: t('nodes.withheld.entry_missing') }), [t])
  const kpis = useMemo(() => kpisOf(rows), [rows])
  const facetData = useMemo(() => facetOptions(rows), [rows])
  const shown = useMemo(() => {
    const keep = new Set(filterOrder(rows, nodeMap, filters).map(rowId))
    return items.filter(i => keep.has(i.id))
  // `filters` is rebuilt every render from stable pieces; these are the pieces.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, rows, nodeMap, url.q, view.state.filters])
  const separatorById = useMemo(() => new Map((separators.data ?? []).map(s => [s.id, s])), [separators.data])

  const capsOf = useCallback((row: OrderRow): RowActionCaps => {
    const caps = serverMap.get(row.landing?.panel_id ?? 0)?.capabilities ?? []
    return {
      admin, enable: caps.includes('inbound.enable'), updateInbound: caps.includes('inbound.update'),
      recreate: caps.includes('inbound.create') && caps.includes('inbound.update') && caps.includes('inbound.delete'),
      remove: caps.includes('inbound.delete'),
    }
  }, [admin, serverMap])

  // The line editor's entries (design 2.6): the sheet opens in edit mode straight from the row (`?line=…&edit=1`).
  const itemOf = (row: OrderRow) => items.find(i => i.id === rowId(row))
  const lineHandlers = {
    edit: (row: OrderRow) => { lineSel.select(rowId(row)); setUrl({ edit: true }) },
    add: (row: OrderRow) => { lineSel.select(`${row.node_id}.new`); setUrl({ edit: true }) },
    toggle: (row: OrderRow, enable: boolean) => { void actions.toggle(row.node_id ?? 0, [row.line_id ?? ''], enable) },
    remove: (row: OrderRow) => actions.askDelete({ row, node: nodeMap.get(row.node_id ?? 0), exitName: itemOf(row)?.exitName }),
  }
  // N4: the deletion dialogs read what a delete takes with it before asking; the row menu and the bulk bar open them.
  const deletion = useNodeDeletion({ rows, serverMap })
  const handlers = useRowHandlers({ lines: lineHandlers, moveTo: setMoveTarget, editSeparator: id => sepSel.select(String(id)), deletion })
  const menuFor = useCallback((item: ListRow, opts?: { parent?: boolean }): MoreMenuItem[] =>
    rowMenuItems({ row: item.row, caps: capsOf(item.row), handlers, t, canMove: !moves.saving, withAddLine: opts?.parent }), [capsOf, handlers, t, moves.saving])
  const nodeHref = admin ? (id: number) => `/admin/nodes/${id}` : undefined
  const menuOf = useCallback((item: ListRow, opts?: { parent?: boolean }) => menuFor(item, opts), [menuFor])

  const hasFactor = items.some(i => i.row.traffic_factor_milli !== undefined && i.row.traffic_factor_milli !== 1000)
  const cols = useMemo(() => orderColumns({ t, colors, labels, tz, nodeHref, menuOf, hasFactor }), [t, colors, labels, tz, admin, menuOf, hasFactor]) // eslint-disable-line react-hooks/exhaustive-deps
  const treeCols = useMemo(() => treeColumns({ t, colors, labels, tz, nodeHref, menuOf }), [t, colors, labels, tz, admin, menuOf]) // eslint-disable-line react-hooks/exhaustive-deps

  const tree = useMemo<TreeItem[]>(() => {
    const byId = new Map(items.map(i => [i.id, i]))
    return buildTree(shown.map(s => s.row), rows).map(g => ({
      id: g.id, group: g,
      list: g.direct ? byId.get(rowId(g.direct)) : undefined,
      children: g.children.map(c => ({ id: rowId(c), group: g, list: byId.get(rowId(c)), child: true })),
    }))
  }, [shown, items, rows])

  // ---- topology (N4) ----
  // Where a landing leaves through another server (its own egress), for the exit column; null = its own server.
  const exitOf = useCallback((nodeId: number) => {
    const id = nodeMap.get(nodeId)?.egress_panel_id
    return id ? { id, name: serverMap.get(id)?.name ?? `#${id}` } : null
  }, [nodeMap, serverMap])
  const graphRows = useMemo(() => shown.map(s => s.row).filter(r => r.kind === 'line'), [shown])
  // "View in list": the order list narrowed to what the picked box stands for, over whatever else is filtered.
  const showInList = (facet: 'entry' | 'type', value: string) => {
    view.setFilters({ ...view.state.filters, [facet]: [value] })
    setUrl({ view: 'order' })
  }

  // ---- reorder rules ----
  const tooMany = rows.length > MAX_ORDER_ROWS
  const blockedReason = filtered ? t('nodes.order.blocked_filter') : moves.saving ? t('nodes.order.saving') : tooMany ? t('nodes.order.too_many') : undefined

  // ---- the open line ----
  // `?line=<node>.new` is a line being added to that node; any other value is a row of the order.
  const addingTo = lineSel.selected?.endsWith('.new') ? Number(lineSel.selected.slice(0, -4)) : null
  const open = lineSel.selected && addingTo === null ? items.find(i => i.id === lineSel.selected) ?? null : null
  const editing = admin && url.edit && (open !== null || addingTo !== null)
  const siblings = useMemo(() => (open ? items.filter(i => i.row.kind === 'line' && i.row.node_id === open.row.node_id && i.id !== open.id) : []), [items, open])
  // Previous / next walk the lines the list shows, in the order of the view that is on (the tree reads group by group).
  const navIds = useMemo(() => (url.view === 'node'
    ? tree.flatMap(g => [g.list, ...(g.children ?? []).map(c => c.list)]).flatMap(l => (l ? [l.id] : []))
    : shown.filter(s => s.row.kind === 'line').map(s => s.id)), [url.view, tree, shown])
  const nav = useRecordNav(navIds, open ? open.id : null, id => lineSel.select(id, { replace: true }))
  // A link to a line that no longer exists closes itself once the list has loaded.
  useEffect(() => { if (lineSel.selected && addingTo === null && order.data && !open) lineSel.clear() }, [lineSel, order.data, open, addingTo])
  const closeSheet = () => guard.close(() => { lineSel.clear(); setUrl({ edit: false }) })
  // A saved line returns to its view, which now shows the new state; a new line closes and is shown in the list.
  const onEditorSaved = ({ addedId, isNew }: { addedId?: string; isNew: boolean }) => guard.leave(() => {
    if (!isNew) { setUrl({ edit: false }); return }
    const id = addedId && addingTo !== null ? `${addingTo}.${addedId}` : null
    lineSel.clear()
    setUrl({ edit: false })
    if (id) { setAdded([id]); setTimeout(() => setAdded(cur => (cur[0] === id ? [] : cur)), 2500) }
  })

  // Once the new order has rendered, bring the moved row into view (the table needs the new data first).
  const firstMoved = moves.highlighted[0]
  useEffect(() => { if (firstMoved) tableRef.current?.scrollToRow(firstMoved) }, [firstMoved])
  // A line just added appears once the order has been read again; bring it into view then.
  const firstAdded = added[0] && items.some(i => i.id === added[0]) ? added[0] : null
  useEffect(() => { if (firstAdded) tableRef.current?.scrollToRow(firstAdded) }, [firstAdded])

  // Two different signals. A line just moved or added FLASHES (a tint, timed). Lines sharing the hovered row's landing
  // or entry are only a quiet hint (their colour bar widens): the row under the pointer must stay the most prominent.
  const highlight = useMemo(() => [...moves.highlighted, ...added], [moves.highlighted, added])
  const related = useMemo(() => relatedIds(rows, hover), [rows, hover])
  const openItem = (item: ListRow) => { if (item.row.kind === 'separator') { if (admin) sepSel.select(String(item.row.id)) } else lineSel.select(item.id) }

  // `E` on a focused row opens the line editor; Enter keeps opening the sheet.
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (!admin || e.key.toLowerCase() !== 'e' || e.metaKey || e.ctrlKey || e.altKey) return
    const el = e.target as HTMLElement
    if (!el.matches('[data-row]')) return
    const item = items.find(i => i.id === el.dataset.id) ?? tree.find(g => g.id === el.dataset.id)?.list
    if (item && item.row.kind === 'line') { e.preventDefault(); lineHandlers.edit(item.row) }
  }

  const statusOptions = useMemo(() => {
    const opt = (value: string, label: string) => ({ value, label, count: facetData.statusCount(value) })
    return [
      opt('published', t('nodes.status.published')), opt('unpublished', t('nodes.kpi.withheld')),
      ...WITHHELD_REASONS.map(r => opt(r, t(`nodes.withheld.${r}`))),
      opt('unreachable', t('nodes.status.unreachable')), opt('entry_missing', t('nodes.withheld.entry_missing')),
    ].filter(o => o.count > 0 || filters.status.includes(o.value))
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [facetData, t, view.state.filters])
  const typeOptions = (['direct', 'forward', 'tunnel', 'cdn', 'external'] as const).map(v => ({ value: v, label: t(`nodes.type.${v}`), count: facetData.type.find(o => o.value === v)?.count ?? 0 }))

  const kpi = (id: string, label: string, value: number | undefined, tone?: 'success' | 'warning' | 'danger') => {
    const on = filters.status.length === 1 && filters.status[0] === id
    return <KpiTile label={label} value={value} tone={tone} pressed={on} onToggle={() => setFacet('status')(on ? [] : [id])} />
  }

  if (order.isError && !order.data) {
    return <ErrorState title={t('nodes.error.load')} reason={order.error} retry={() => order.refetch()} />
  }

  const loading = order.isPending
  const noServers = !loading && serverMap.size === 0 && !!nodes.data
  const empty = filtered
    ? <EmptyState compact title={t('nodes.empty.filtered')} action={<Button variant="outline" size="sm" onClick={clearAll}>{t('nodes.empty.clear')}</Button>} />
    : (
      <EmptyState compact title={t('nodes.empty.title')} description={t(noServers && admin ? 'nodes.empty.no_servers_body' : 'nodes.empty.body')}
        action={admin ? (noServers ? <Button variant="outline" size="sm" render={<Link to="/admin/servers" />}>{t('nodes.empty.no_servers')}</Button> : <Button size="sm" onClick={() => navigate('/admin/nodes/new')}>{t('nodes.create')}</Button>) : undefined} />
    )

  const separatorRow = (item: ListRow) => {
    const s = separatorById.get(item.row.id ?? 0)
    return { fullWidth: <SeparatorRowCell name={item.row.name} enabled={item.row.enabled} mode={item.row.mode} nodeCount={s?.node_ids.length ?? 0} menu={menuFor(item)} />, selectable: false, openable: admin, state: item.row.enabled ? undefined : 'dimmed' as const }
  }
  const nodeIdsOf = (ids: string[]) => [...new Set(ids.flatMap(id => (id.startsWith('landing:') ? [Number(id.slice(8))] : (items.find(i => i.id === id)?.row.node_id ?? []))))].filter(Number.isFinite) as number[]
  // The relay lines among the selection (a direct row's enable is its whole node, which the node actions cover).
  const relayRefsOf = (ids: string[]) => ids.flatMap(id => {
    const r = items.find(i => i.id === id)?.row
    return r && r.kind === 'line' && r.line_id !== 'direct' ? [{ nodeId: r.node_id ?? 0, lineId: r.line_id ?? '' }] : []
  })
  const bulk = (ids: string[], clear: () => void) => (
    <BulkNodeActions nodeIds={nodeIdsOf(ids)} lineRefs={relayRefsOf(ids)} toggleLines={actions.toggle} admin={admin} serverOf={id => serverMap.get(nodeMap.get(id)?.panel_id ?? 0)} clear={clear} onDelete={deletion.deleteMany} />
  )

  const common = {
    columnMenu: true, selectable: true, selection: checked, onSelectionChange: setChecked, pagination: false as const,
    columnVisibility: view.state.columnVisibility, onColumnVisibilityChange: view.setColumnVisibility,
    loading, onRowHover: setHover, highlightedIds: highlight, relatedIds: related, empty, maxHeight: null,
    bulkActions: (ctx: { ids: string[]; clear: () => void }) => bulk(ctx.ids, ctx.clear),
  }

  return (
    <div className="flex flex-col" onKeyDown={onKeyDown}>
      <KpiGrid>
        <KpiTile label={t('nodes.kpi.lines')} value={loading ? undefined : kpis.lines} onToggle={() => setFacet('status')([])} />
        {kpi('published', t('nodes.kpi.published'), loading ? undefined : kpis.published, 'success')}
        {kpi('unpublished', t('nodes.kpi.withheld'), loading ? undefined : kpis.unpublished, 'warning')}
        {kpi('unreachable', t('nodes.kpi.unreachable'), loading ? undefined : kpis.unreachable, 'danger')}
      </KpiGrid>
      {nodes.isError && (
        <Alert className="mb-4"><AlertDescription className="flex flex-wrap items-center gap-2">{t('nodes.error.partial')}<Button variant="outline" size="xs" onClick={() => void nodes.refetch()}>{t('nodes.retry')}</Button></AlertDescription></Alert>
      )}
      <FilterBar
        className="mb-4"
        search={{ value: url.q, onChange: q => setUrl({ q }), placeholder: t('nodes.search.placeholder') }}
        facets={[
          { id: 'type', label: t('nodes.facet.type'), selected: filters.type, onChange: setFacet('type'), options: typeOptions },
          { id: 'status', label: t('nodes.facet.status'), selected: filters.status, onChange: setFacet('status'), options: statusOptions },
          { id: 'entry', label: t('nodes.facet.entry'), selected: filters.entry, onChange: setFacet('entry'), options: facetData.entry },
          { id: 'landing', label: t('nodes.facet.landing_server'), selected: filters.landing, onChange: setFacet('landing'), options: facetData.landing },
        ]}
        onClearAll={clearAll}
      >
        <SegmentedControl aria-label={t('nodes.view.label')} size="sm" value={url.view} onChange={v => setUrl({ view: v })}
          options={[{ value: 'order', label: t('nodes.view.order') }, { value: 'node', label: t('nodes.view.node') }, { value: 'graph', label: t('nodes.view.graph') }]} />
        {moves.undoable && <Button variant="outline" size="sm" onClick={moves.undo} data-testid="undo-move">{t('nodes.toast.undo')}</Button>}
        {admin && url.view === 'order' && filtered && <Button variant="outline" size="sm" onClick={clearAll}>{t('nodes.order.clear_to_drag')}</Button>}
      </FilterBar>
      <div className="mb-4 flex items-center gap-1 text-xs text-muted-foreground">
        <span>{t('nodes.list.scope_note')}</span>
        <HelpTip text={t('nodes.list.scope_help')} label={t('nodes.list.scope_help_label')} />
        {filtered && !loading && <span className="ml-2 tabular-nums">{t('nodes.list.count_filtered', { shown: shown.filter(s => s.row.kind === 'line').length, total: kpis.lines })}</span>}
        {!admin && url.view === 'order' && <span className="ml-2">{t('nodes.order.read_only')}</span>}
      </div>

      {url.view === 'graph' ? (
        <TopologyView
          rows={graphRows} colors={colors} exitOf={exitOf} admin={admin} empty={loading ? <TableSkeleton rows={4} cols={3} /> : empty}
          filters={(
            <>
              <FacetFilter label={t('nodes.facet.entry')} options={facetData.entry} selected={filters.entry} onChange={setFacet('entry')} />
              <FacetFilter label={t('nodes.facet.landing_server')} options={facetData.landing} selected={filters.landing} onChange={setFacet('landing')} />
            </>
          )}
          onShowInList={showInList} onAsList={() => setUrl({ view: 'order' })}
        />
      ) : url.view === 'order' ? (
        <DataTable<ListRow>
          tableId="admin.nodes.lines"
          {...common}
          aria-label={t('nodes.tabs.lines')}
          handleRef={tableRef}
          data={shown}
          columns={cols}
          getRowId={r => r.id}
          rowLabel={r => r.row.name}
          onRowOpen={openItem}
          activeRowId={lineSel.selected}
          rowOptions={r => (r.row.kind === 'separator' ? separatorRow(r) : { state: rowStateOf(r) })}
          reorderBlockedReason={blockedReason}
          {...(admin ? { onReorder: moves.drag } : {})}
          renderCard={(r, ctx) => (r.row.kind === 'separator'
            ? <span className="text-xs font-medium text-foreground/70">— {r.row.name} —</span>
            : <LineCard item={r} colors={colors} labels={labels} menu={menuFor(r)} open={ctx.open} />)}
        />
      ) : (
        <DataTable<TreeItem>
          tableId="admin.nodes.linesTree"
          {...common}
          aria-label={t('nodes.view.node')}
          data={tree}
          columns={treeCols}
          getRowId={r => r.id}
          rowLabel={r => r.group.name}
          getSubRows={r => r.children}
          defaultExpanded
          onRowOpen={r => { const l = r.list; if (l) lineSel.select(l.id) }}
          activeRowId={lineSel.selected && open?.row.line_id === 'direct' ? `landing:${open.row.node_id}` : lineSel.selected}
          rowOptions={r => ({ openable: !!r.list, state: r.list ? rowStateOf(r.list) : undefined })}
          renderCard={(r, ctx) => (r.list
            ? <LineCard item={r.list} colors={colors} labels={labels} menu={menuFor(r.list, { parent: true })} open={ctx.open} />
            : <span className="text-sm font-medium">{r.group.name}</span>)}
        />
      )}

      <LineDialog
        item={open} newLine={editing && addingTo !== null ? { nodeName: nodeMap.get(addingTo)?.display_name ?? '' } : null}
        nav={nav}
        editor={editing ? <LineEditor nodeId={addingTo ?? open?.row.node_id ?? 0} lineId={addingTo !== null ? null : open?.row.line_id ?? null} onDirtyChange={guard.setDirty} onSaved={onEditorSaved} /> : undefined}
        siblings={siblings} colors={colors} labels={labels} tz={tz}
        menu={open ? menuFor(open) : []} canEdit={admin} nodeHref={open && nodeHref ? nodeHref(open.row.node_id ?? 0) : undefined}
        onEdit={() => setUrl({ edit: true })} onBack={() => guard.close(() => setUrl({ edit: false }))}
        onSelect={id => lineSel.select(id, { replace: true })} onClose={closeSheet}
      />
      {actions.dialogs}
      <SeparatorDialog
        separatorId={!admin || !sepSel.selected ? null : sepSel.selected === 'new' ? 'new' : Number(sepSel.selected)}
        separators={separators.data ?? []}
        nodes={items.filter(i => i.row.kind === 'line' && i.row.line_id === 'direct').map(i => ({ id: i.row.node_id ?? 0, name: i.row.landing?.name ?? i.row.name }))}
        onClose={sepSel.clear}
        onSaved={() => { sepSel.clear(); void order.refetch(); void separators.refetch() }}
      />
      {moveTarget && admin && <MoveToDialog rows={rows} target={moveTarget} onClose={() => setMoveTarget(null)} onMove={moves.move} />}
      {moves.stale && <StaleOrderDialog stale={moves.stale} onAnswer={moves.resolveStale} />}
      {deletion.element}
    </div>
  )
}
