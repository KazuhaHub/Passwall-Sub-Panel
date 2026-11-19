import { useEffect, useMemo, useState } from 'react'
import type { ColumnDef, SortingState } from '@tanstack/react-table'
import { EllipsisIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Label } from '@/components/ui/label'
import { LandingColorBar } from '@/components/diagram/LandingColorBar'
import { PathDiagram } from '@/components/diagram/PathDiagram'
import { RelationGraph } from '@/components/diagram/RelationGraph'
import type { LandingColor } from '@/components/diagram/landingColor'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { DataTable } from '@/components/data/DataTable'
import { DetailSheet } from '@/components/data/DetailSheet'
import { FilterBar } from '@/components/data/FilterBar'
import { KpiGrid, KpiTile } from '@/components/common/KpiTile'
import { StatusBadge } from '@/components/common/StatusBadge'
import { IconButton } from '@/components/common/IconButton'
import { applyReorder, filterRows } from '@/components/data/dataTableLogic'
import { useTableViewState } from '@/hooks/useTableViewState'
import { urlCodecs, useUrlStates } from '@/hooks/useUrlState'
import { useUrlSelection } from '@/hooks/useUrlSelection'
import {
  LINES, NODES, fetchUsers, landingColorsFor, linePath, type LineRow, type LineStatus, type NodeRow, type UserRow,
} from './sampleData'

export function Section({ id, title, note, children }: { id?: string; title: string; note?: string; children: React.ReactNode }) {
  return (
    <section id={id} className="flex flex-col gap-3">
      <div>
        <h2 className="text-lg font-semibold">{title}</h2>
        {note && <p className="text-sm text-muted-foreground">{note}</p>}
      </div>
      {children}
    </section>
  )
}

const STATUS_LABEL = { up: '在线', down: '离线', pending: '等待生效' } as const
const STATUS_TONE = { up: 'success', down: 'danger', pending: 'warning' } as const

/** A quiet dot for the normal state and a coloured badge only when something is off (plan 3.2). */
function StatusCell({ status }: { status: 'up' | 'down' | 'pending' }) {
  return <StatusBadge tone={STATUS_TONE[status]} label={STATUS_LABEL[status]} />
}

// ---------------------------------------------------------------- nodes: client mode

const nodeColumns: ColumnDef<NodeRow>[] = [
  { accessorKey: 'name', header: '名称', meta: { hideable: false, label: '名称', className: 'font-medium' } },
  { accessorKey: 'status', header: '状态', meta: { label: '状态' }, cell: c => <StatusCell status={c.getValue<NodeRow['status']>()} /> },
  { accessorKey: 'region', header: '区域', meta: { label: '区域' } },
  { accessorKey: 'protocol', header: '协议', meta: { label: '协议' } },
  { accessorKey: 'group', header: '可见分组', meta: { label: '可见分组', defaultHidden: true } },
  { accessorKey: 'lines', header: '线路', meta: { label: '线路', align: 'right', defaultHidden: true } },
  { accessorKey: 'trafficGb', header: '本周期流量', meta: { label: '本周期流量', align: 'right' }, cell: c => `${c.getValue<number>().toFixed(1)} GB` },
  {
    id: 'actions', header: '', enableSorting: false, meta: { hideable: false, align: 'right' },
    cell: () => <IconButton label="更多操作" size="icon-xs" onClick={e => e.stopPropagation()}><EllipsisIcon /></IconButton>,
  },
]

// One shared empty list: a fresh [] each render would defeat the useMemo below.
const NONE: string[] = []

export function NodesTableSection() {
  const view = useTableViewState('ui-kit.nodes', { pageSize: 10 })
  const [url, setUrl] = useUrlStates({ q: urlCodecs.string(), page: urlCodecs.int(1, { min: 1 }) })
  const selection = useUrlSelection('row')
  const [bulkLog, setBulkLog] = useState('')

  const status = view.state.filters.status ?? NONE
  const group = view.state.filters.group ?? NONE
  const setFacet = (id: string) => (next: string[]) => {
    view.setFilters({ ...view.state.filters, [id]: next })
    setUrl({ page: 1 })
  }

  const filtered = useMemo(() => {
    let rows = filterRows(NODES, n => `${n.name} ${n.region} ${n.protocol}`, url.q)
    if (status.length) rows = rows.filter(n => status.includes(n.status))
    if (group.length) rows = rows.filter(n => group.includes(n.group))
    return rows
  }, [url.q, status, group])

  const counts = (key: 'status' | 'group') => {
    const m = new Map<string, number>()
    for (const n of NODES) m.set(n[key], (m.get(n[key]) ?? 0) + 1)
    return m
  }
  const sc = counts('status')
  const gc = counts('group')
  const selected = NODES.find(n => n.id === selection.selected)
  const sorting: SortingState = view.state.sort ? [view.state.sort] : []

  return (
    <Section id="nodes" title="节点列表（客户端分页）" note="筛选、排序、每页条数和列显隐记在 localStorage；搜索词、页码、选中行在 URL 里。点一行，或用方向键加回车，打开右侧详情。">
      <KpiGrid>
        <KpiTile label="全部节点" value={NODES.length} />
        <KpiTile label="在线" value={sc.get('up') ?? 0} tone="success" pressed={status.length === 1 && status[0] === 'up'} onToggle={() => setFacet('status')(status[0] === 'up' && status.length === 1 ? [] : ['up'])} />
        <KpiTile label="离线" value={sc.get('down') ?? 0} tone="danger" pressed={status.length === 1 && status[0] === 'down'} onToggle={() => setFacet('status')(status[0] === 'down' && status.length === 1 ? [] : ['down'])} />
        <KpiTile label="等待生效" value={sc.get('pending') ?? 0} tone="warning" pressed={status.length === 1 && status[0] === 'pending'} onToggle={() => setFacet('status')(status[0] === 'pending' && status.length === 1 ? [] : ['pending'])} />
      </KpiGrid>
      <DataTable
        aria-label="节点"
        data={filtered}
        columns={nodeColumns}
        getRowId={n => n.id}
        rowLabel={n => n.name}
        selectable
        page={url.page}
        // A person paging pushes (Back returns to the previous page); a correction of an
        // out-of-range page REPLACES, or Back would return to the bad page and be corrected again.
        onPageChange={(p, reason) => setUrl({ page: p }, { history: reason === 'correction' ? 'replace' : 'push' })}
        pageSize={view.state.pageSize}
        onPageSizeChange={view.setPageSize}
        sorting={sorting}
        onSortingChange={s => view.setSort(s[0] ?? null)}
        columnVisibility={view.state.columnVisibility}
        onColumnVisibilityChange={view.setColumnVisibility}
        onRowOpen={n => selection.select(n.id)}
        activeRowId={selection.selected}
        toolbar={
          <FilterBar
            search={{ value: url.q, onChange: q => setUrl({ q, page: 1 }), placeholder: '搜索名称、区域、协议' }}
            facets={[
              { id: 'status', label: '状态', selected: status, onChange: setFacet('status'), options: (['up', 'down', 'pending'] as const).map(v => ({ value: v, label: STATUS_LABEL[v], count: sc.get(v) ?? 0 })) },
              { id: 'group', label: '可见分组', selected: group, onChange: setFacet('group'), options: [...gc.keys()].map(v => ({ value: v, label: v, count: gc.get(v) ?? 0 })) },
            ]}
            onClearAll={() => { view.setFilters({}); setUrl({ q: '', page: 1 }) }}
          />
        }
        empty={<span>没有符合条件的节点。清除筛选后再试。</span>}
        bulkActions={({ ids, hiddenCount, clear }) => (
          <>
            <Button variant="outline" size="sm" onClick={() => setBulkLog(`导出 ${ids.join(', ')}`)}>导出</Button>
            {/* A destructive action acts on every selected id, so it confirms with the TOTAL
                and says how many of them are not on screen. */}
            <Button
              variant="destructive"
              size="sm"
              onClick={() => {
                const text = `删除 ${ids.length} 个节点${hiddenCount > 0 ? `，其中 ${hiddenCount} 个不在当前列表中` : ''}？`
                if (window.confirm(text)) { setBulkLog(`已确认：${text}`); clear() }
              }}
            >
              删除
            </Button>
          </>
        )}
      />
      {bulkLog && <p className="text-xs text-muted-foreground">最近的批量操作：{bulkLog}</p>}
      <DetailSheet
        open={selection.isOpen}
        onClose={selection.clear}
        title={selected?.name ?? `未找到 ${selection.selected}`}
        status={selected && <StatusCell status={selected.status} />}
        description={selected && `${selected.region} · ${selected.protocol}`}
        actions={selected && <><Button size="sm">打开详情页</Button><Button size="sm" variant="outline">编辑</Button></>}
        footer={<Button variant="outline" onClick={selection.clear}>关闭</Button>}
      >
        {selected && (
          <dl className="grid grid-cols-[7rem_1fr] gap-x-3 gap-y-2 text-sm">
            <dt className="text-muted-foreground">可见分组</dt><dd>{selected.group}</dd>
            <dt className="text-muted-foreground">线路</dt><dd>{selected.lines} 条</dd>
            <dt className="text-muted-foreground">本周期流量</dt><dd>{selected.trafficGb.toFixed(1)} GB</dd>
            <dt className="text-muted-foreground">编号</dt><dd className="font-mono">{selected.id}</dd>
          </dl>
        )}
      </DetailSheet>
      <div className="flex items-center gap-2">
        <Button variant="ghost" size="sm" onClick={() => view.reset()}>恢复此表的默认视图</Button>
      </div>
    </Section>
  )
}

// ---------------------------------------------------------------- lines: three views, one data set

const KIND_LABEL: Record<LineRow['lineKind'], string> = { forward: '端口转发', tunnel: '加密隧道', cdn: 'CDN', external: '外部入口', direct: '直连' }

function PathCell({ row, colors }: { row: LineRow; colors: Map<string, LandingColor> }) {
  const { nodes, edges } = linePath(row, false, colors)
  return (
    <div className="flex items-center gap-2">
      <LandingColorBar color={colors.get(String(row.landingId))} className="h-5 self-center" />
      <PathDiagram nodes={nodes} edges={edges} size="sm" />
      {/* A dimmed row also says why, in words: a softened row alone would be the only cue. */}
      {row.hidden && <Badge variant="outline">已隐藏</Badge>}
    </div>
  )
}

interface TreeRow { id: string; name: string; sub: string; status: LineStatus; kind: string; line?: LineRow; children?: TreeRow[] }

function treeOf(lines: LineRow[]): TreeRow[] {
  const landings = [...new Set(lines.filter(l => l.kind === 'line').map(l => l.landing))]
  return landings.map(name => {
    const mine = lines.filter(l => l.kind === 'line' && l.landing === name)
    return {
      id: `landing:${name}`, name, kind: '落地节点', sub: `${mine.length} 条线路`,
      status: mine.some(l => l.status === 'down') ? 'down' : mine.some(l => l.status === 'pending') ? 'pending' : 'ok',
      children: mine.map(l => ({ id: l.id, name: l.entry, sub: `:${l.port}`, status: l.status, kind: KIND_LABEL[l.lineKind], line: l })),
    } satisfies TreeRow
  })
}

const treeColumns: ColumnDef<TreeRow>[] = [
  { accessorKey: 'name', header: '名称', meta: { hideable: false, className: 'font-medium' }, enableSorting: false },
  { accessorKey: 'kind', header: '类型', enableSorting: false },
  { accessorKey: 'sub', header: '入口 / 端口', enableSorting: false },
  { accessorKey: 'status', header: '状态', enableSorting: false, cell: c => { const v = c.getValue<LineStatus>(); return <StatusCell status={v === 'ok' ? 'up' : v} /> } },
]

const LINE_VIEWS = ['flat', 'tree', 'graph'] as const

// One colour map for the page, from the complete set of landings (see landingColorsFor).
const LANDING_COLORS = landingColorsFor(LINES)

/**
 * The line list as the nodes page will build it, and the template to copy:
 * - three views over ONE data set: flat (reorderable, with separators), tree (by landing),
 *   graph (entries to landings);
 * - ONE opened line, kept in the URL (useUrlSelection), so a link opens it and Back closes it;
 * - ONE checkbox selection, lifted here, so it survives switching views;
 * - the graph's own box selection kept apart from the opened line;
 * - a search that switches reordering off, with the reason shown on the handles.
 */
export function LinesViewsSection() {
  const [url, setUrl] = useUrlStates({ view: urlCodecs.oneOf(LINE_VIEWS, 'flat'), lq: urlCodecs.string() })
  const open = useUrlSelection('line')
  const [checked, setChecked] = useState<Record<string, boolean>>({})
  const [boxSel, setBoxSel] = useState<string | null>(null)
  const [rows, setRows] = useState(LINES)
  const [hover, setHover] = useState<string | null>(null)
  const [by, setBy] = useState<'landing' | 'entry'>('landing')
  const [log, setLog] = useState('尚未移动')
  const colors = LANDING_COLORS

  // Lines that share the hovered line's landing (or entry): lit in every view.
  const hovered = rows.find(r => r.id === hover && r.kind === 'line')
  const highlighted = useMemo(
    () => (hovered ? rows.filter(r => r.kind === 'line' && r.id !== hovered.id && (by === 'landing' ? r.landingId === hovered.landingId : r.entry === hovered.entry)).map(r => r.id) : []),
    [rows, hovered, by],
  )
  const openLine = rows.find(r => r.id === open.selected && r.kind === 'line')
  const toggleOpen = (id: string) => { if (open.selected === id) open.clear(); else open.select(id) }

  const visible = useMemo(() => (url.lq ? filterRows(rows.filter(r => r.kind === 'line'), r => `${r.landing} ${r.entry} ${KIND_LABEL[r.lineKind]}`, url.lq) : rows), [rows, url.lq])

  const flatColumns = useMemo<ColumnDef<LineRow>[]>(() => [
    { id: 'path', header: '线路', enableSorting: false, meta: { hideable: false, className: 'w-full' }, cell: c => <PathCell row={c.row.original} colors={colors} /> },
    { id: 'kind', header: '类型', enableSorting: false, cell: c => KIND_LABEL[c.row.original.lineKind], meta: { label: '类型' } },
    { id: 'multiplier', header: '倍率', enableSorting: false, meta: { align: 'right', label: '倍率' }, cell: c => `×${c.row.original.multiplier}` },
  ], [colors])

  const tree = useMemo(() => treeOf(rows), [rows])

  // The graph: entries to landings. Boxes select boxes; the line that is open is a different thing.
  const graph = useMemo(() => {
    const lines = rows.filter(l => l.kind === 'line' && l.lineKind !== 'direct')
    const entries = [...new Set(lines.map(l => l.entry))]
    const landings = [...new Set(lines.map(l => l.landing))]
    const landingId = (n: string) => lines.find(l => l.landing === n)!.landingId
    return {
      lines,
      columns: [
        { id: 'entries', title: '入口', boxes: entries.map(e => ({ id: `entry:${e}`, label: e })) },
        { id: 'landings', title: '落地节点', boxes: landings.map(n => ({ id: `landing:${n}`, label: n, landingColor: colors.get(String(landingId(n))) })) },
      ],
      edges: lines.map(l => ({ id: l.id, label: KIND_LABEL[l.lineKind], from: `entry:${l.entry}`, to: `landing:${l.landing}`, style: l.lineKind === 'tunnel' ? 'dashed' as const : 'solid' as const, status: l.status === 'ok' ? 'success' as const : l.status === 'pending' ? 'pending' as const : 'danger' as const })),
    }
  }, [rows, colors])
  const throughBox = boxSel ? graph.lines.filter(l => `entry:${l.entry}` === boxSel || `landing:${l.landing}` === boxSel) : []

  const selectedCount = Object.values(checked).filter(Boolean).length

  return (
    <Section id="lines" title="线路列表（三种视图，共用一份数据）" note="平铺视图可拖动排序，含分隔行、等待生效和已隐藏的线路；悬停一行，同落地节点或同入口的线路会一起亮起。打开的线路写在地址栏里，勾选的行切换视图后仍在。搜索时排序关闭，手柄上写明原因。窄屏下每行变成一张卡片，路径竖排。">
      <div className="flex flex-wrap items-center gap-3">
        <SegmentedControl
          aria-label="视图"
          value={url.view}
          onChange={view => setUrl({ view })}
          options={[{ value: 'flat', label: '平铺' }, { value: 'tree', label: '按落地节点' }, { value: 'graph', label: '关系图' }]}
        />
        <SegmentedControl
          aria-label="悬停时高亮"
          size="sm"
          value={by}
          onChange={setBy}
          options={[{ value: 'landing', label: '同落地节点' }, { value: 'entry', label: '同入口' }]}
        />
        <span className="text-xs text-muted-foreground tabular-nums">已勾选 {selectedCount} 行</span>
      </div>

      {url.view === 'flat' && (
        <DataTable
          aria-label="线路"
          data={visible}
          columns={flatColumns}
          getRowId={r => r.id}
          rowLabel={r => (r.kind === 'separator' ? r.title ?? '' : `${r.landing} · ${r.entry}`)}
          pagination={false}
          columnMenu={false}
          selectable
          selection={checked}
          onSelectionChange={setChecked}
          onRowOpen={r => toggleOpen(r.id)}
          activeRowId={open.selected}
          onRowHover={setHover}
          highlightedIds={highlighted}
          toolbar={<FilterBar aria-label="筛选线路" search={{ value: url.lq, onChange: lq => setUrl({ lq }), placeholder: '搜索落地节点、入口' }} onClearAll={() => setUrl({ lq: '' })} />}
          reorderBlockedReason={url.lq ? '搜索时无法调整顺序，清除搜索后再试' : undefined}
          rowOptions={r => r.kind === 'separator'
            ? { fullWidth: <span className="text-xs font-medium tracking-wide text-foreground/70">— {r.title} —</span>, selectable: false, openable: false }
            : { state: r.status === 'pending' ? 'pending' : r.hidden ? 'dimmed' : undefined }}
          onReorder={call => { setRows(prev => applyReorder(prev, r => r.id, call)); setLog(`移动 ${call.movedId}，放到 ${call.beforeId ?? '末尾'} 之前`) }}
          renderCard={(r, { open: openRow }) => (r.kind === 'separator'
            ? <span className="text-xs font-medium text-foreground/70">— {r.title} —</span>
            : (
              <button type="button" onClick={openRow} className="flex w-full min-w-0 flex-col gap-2 text-left">
                <div className="flex items-center gap-2">
                  <LandingColorBar color={colors.get(String(r.landingId))} className="h-4" />
                  <span className="min-w-0 truncate text-sm font-medium">{r.landing}</span>
                  <span className="text-xs text-foreground/70">{KIND_LABEL[r.lineKind]} · ×{r.multiplier}</span>
                  {r.hidden && <Badge variant="outline">已隐藏</Badge>}
                </div>
                <PathDiagram {...linePath(r, false, colors)} size="md" orientation="vertical" />
              </button>
            ))}
        />
      )}
      {url.view === 'tree' && (
        <DataTable
          aria-label="按落地节点"
          data={tree}
          columns={treeColumns}
          getRowId={r => r.id}
          rowLabel={r => r.name}
          getSubRows={r => r.children}
          defaultExpanded
          pagination={false}
          columnMenu={false}
          selectable
          selection={checked}
          onSelectionChange={setChecked}
          onRowOpen={r => { if (r.line) toggleOpen(r.line.id) }}
          activeRowId={open.selected}
          onRowHover={setHover}
          highlightedIds={highlighted}
          rowOptions={r => ({ openable: !!r.line, selectable: !!r.line, state: r.status === 'pending' && r.line ? 'pending' : undefined })}
        />
      )}
      {url.view === 'graph' && (
        <>
          <RelationGraph columns={graph.columns} edges={graph.edges} selectedId={boxSel} onSelect={setBoxSel} maxHeight="22rem" />
          {boxSel && (
            <div className="flex flex-col gap-1 rounded-xl border bg-card p-3 text-sm">
              <span className="text-xs font-medium text-muted-foreground">经过所选的线路：点一条打开它</span>
              {throughBox.map(l => (
                <Button key={l.id} variant={open.selected === l.id ? 'secondary' : 'ghost'} size="sm" className="justify-start" onClick={() => toggleOpen(l.id)}>
                  {l.entry} → {l.landing}
                </Button>
              ))}
            </div>
          )}
        </>
      )}

      <div className="flex min-w-0 flex-col gap-2 rounded-xl border bg-card p-4">
        <span className="text-xs font-medium text-muted-foreground">打开的线路（三个视图共用，写在地址栏里）</span>
        {openLine ? (
          <>
            <PathDiagram {...linePath(openLine, true, colors)} />
            <div><Button variant="outline" size="sm" onClick={open.clear}>关闭</Button></div>
          </>
        ) : <span className="text-sm text-muted-foreground">没有打开的线路</span>}
      </div>
      <p className="text-xs text-muted-foreground" role="status">{log}</p>
    </Section>
  )
}

// ---------------------------------------------------------------- users: server mode + cards

const userColumns: ColumnDef<UserRow>[] = [
  { accessorKey: 'id', header: 'ID', meta: { label: 'ID', className: 'font-mono text-muted-foreground', defaultHidden: true } },
  { accessorKey: 'upn', header: '登录名', meta: { hideable: false, className: 'font-medium' } },
  { accessorKey: 'name', header: '姓名', meta: { label: '姓名' } },
  { accessorKey: 'group', header: '分组', meta: { label: '分组' } },
  { accessorKey: 'usedGb', header: '已用', meta: { label: '已用', align: 'right' }, cell: c => `${c.getValue<number>().toFixed(1)} GB` },
  { accessorKey: 'limitGb', header: '额度', meta: { label: '额度', align: 'right' }, cell: c => (c.getValue<number>() === 0 ? '不限量' : `${c.getValue<number>()} GB`) },
  { accessorKey: 'expires', header: '到期', meta: { label: '到期' } },
]

export function UsersServerSection() {
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(10)
  const [sorting, setSorting] = useState<SortingState>([])
  const [keyword, setKeyword] = useState('')
  const [fail, setFail] = useState(false)
  const [empty, setEmpty] = useState(false)
  const [tick, setTick] = useState(0)
  const [state, setState] = useState<{ items: UserRow[]; total: number; loading: boolean; error: Error | null }>({ items: [], total: 0, loading: true, error: null })

  useEffect(() => {
    let live = true
    setState(s => ({ ...s, loading: true, error: null }))
    fetchUsers({ page, pageSize, sortBy: sorting[0]?.id ?? '', sortDesc: sorting[0]?.desc ?? false, keyword }, { fail, empty })
      .then(r => { if (live) setState({ ...r, loading: false, error: null }) })
      .catch((e: Error) => { if (live) setState(s => ({ ...s, loading: false, error: e })) })
    return () => { live = false }
  }, [page, pageSize, sorting, keyword, fail, empty, tick])

  return (
    <Section id="users" title="用户列表（服务端分页）" note="页码、每页条数、排序、关键字都由页面持有，表格只报告变化。窄屏下每行变成一张卡片。">
      <div className="flex flex-wrap items-center gap-4 text-sm">
        <span className="flex items-center gap-2"><Switch checked={fail} onCheckedChange={setFail} id="sim-fail" /><Label htmlFor="sim-fail">模拟出错</Label></span>
        <span className="flex items-center gap-2"><Switch checked={empty} onCheckedChange={setEmpty} id="sim-empty" /><Label htmlFor="sim-empty">模拟空列表</Label></span>
      </div>
      <DataTable
        mode="server"
        aria-label="用户"
        data={state.items}
        total={state.total}
        columns={userColumns}
        getRowId={u => String(u.id)}
        rowLabel={u => u.upn}
        page={page}
        onPageChange={setPage}
        pageSize={pageSize}
        onPageSizeChange={n => { setPageSize(n); setPage(1) }}
        sorting={sorting}
        onSortingChange={s => { setSorting(s); setPage(1) }}
        loading={state.loading}
        error={state.error}
        onRetry={() => setTick(t => t + 1)}
        toolbar={<FilterBar search={{ value: keyword, onChange: k => { setKeyword(k); setPage(1) }, placeholder: '搜索登录名、姓名' }} />}
        empty={keyword ? undefined : <span>还没有用户。新建用户后会显示在这里。</span>}
        renderCard={u => (
          <div className="flex flex-col gap-1">
            <div className="flex items-center justify-between gap-2"><span className="truncate font-medium">{u.upn}</span><Badge variant="secondary">{u.group}</Badge></div>
            <span className="text-xs text-muted-foreground">{u.name} · 到期 {u.expires}</span>
            <span className="text-xs tabular-nums">{u.usedGb.toFixed(1)} GB / {u.limitGb === 0 ? '不限量' : `${u.limitGb} GB`}</span>
          </div>
        )}
      />
    </Section>
  )
}
