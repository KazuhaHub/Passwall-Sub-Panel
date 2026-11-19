import { useEffect, useMemo, useState } from 'react'
import type { ColumnDef, SortingState } from '@tanstack/react-table'
import { EllipsisIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Label } from '@/components/ui/label'
import { LandingColorBar } from '@/components/diagram/LandingColorBar'
import { PathDiagram } from '@/components/diagram/PathDiagram'
import { DataTable } from '@/components/data/DataTable'
import { DetailSheet } from '@/components/data/DetailSheet'
import { FilterBar } from '@/components/data/FilterBar'
import { StatFilterCard, StatFilterGrid } from '@/components/data/StatFilterCard'
import { TipButton } from '@/components/data/TipButton'
import { applyReorder, filterRows } from '@/components/data/dataTableLogic'
import { useTableViewState } from '@/hooks/useTableViewState'
import { urlCodecs, useUrlStates } from '@/hooks/useUrlState'
import { useUrlSelection } from '@/hooks/useUrlSelection'
import { cn } from '@/lib/utils'
import {
  LANDING_TREE, LINES, NODES, fetchUsers, linePath, type LandingGroupRow, type LineRow, type LineStatus, type NodeRow, type UserRow,
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
const STATUS_TEXT = { up: 'bg-success', down: 'bg-destructive', pending: 'bg-warning' } as const

/** A quiet dot for the normal state and a coloured badge only when something is off (plan 3.2). */
function StatusCell({ status }: { status: 'up' | 'down' | 'pending' }) {
  if (status === 'up') return <span className="inline-flex items-center gap-1.5 text-muted-foreground"><span className="size-2 rounded-full bg-success" />{STATUS_LABEL.up}</span>
  return (
    <Badge variant={status === 'down' ? 'destructive' : 'secondary'} className="gap-1.5">
      <span className={cn('size-1.5 rounded-full', STATUS_TEXT[status])} />{STATUS_LABEL[status]}
    </Badge>
  )
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
    cell: () => <TipButton label="更多操作" size="icon-xs" onClick={e => e.stopPropagation()}><EllipsisIcon /></TipButton>,
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
      <StatFilterGrid columns={4}>
        <StatFilterCard label="全部节点" value={NODES.length} />
        <StatFilterCard label="在线" value={sc.get('up') ?? 0} tone="success" pressed={status.length === 1 && status[0] === 'up'} onToggle={() => setFacet('status')(status[0] === 'up' && status.length === 1 ? [] : ['up'])} />
        <StatFilterCard label="离线" value={sc.get('down') ?? 0} tone="danger" pressed={status.length === 1 && status[0] === 'down'} onToggle={() => setFacet('status')(status[0] === 'down' && status.length === 1 ? [] : ['down'])} />
        <StatFilterCard label="等待生效" value={sc.get('pending') ?? 0} tone="warning" pressed={status.length === 1 && status[0] === 'pending'} onToggle={() => setFacet('status')(status[0] === 'pending' && status.length === 1 ? [] : ['pending'])} />
      </StatFilterGrid>
      <DataTable
        aria-label="节点"
        data={filtered}
        columns={nodeColumns}
        getRowId={n => n.id}
        rowLabel={n => n.name}
        selectable
        page={url.page}
        onPageChange={p => setUrl({ page: p }, { history: 'push' })}
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
        bulkActions={({ selectedIds }) => (
          <>
            <Button variant="outline" size="sm" onClick={() => setBulkLog(`禁用 ${selectedIds.length} 个节点`)}>禁用</Button>
            <Button variant="outline" size="sm" onClick={() => setBulkLog(`导出 ${selectedIds.join(', ')}`)}>导出</Button>
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

// ---------------------------------------------------------------- lines: reorder + path cell

const KIND_LABEL: Record<LineRow['lineKind'], string> = { forward: '端口转发', tunnel: '加密隧道', cdn: 'CDN', external: '外部入口', direct: '直连' }

const lineColumns: ColumnDef<LineRow>[] = [
  {
    id: 'path', header: '线路', enableSorting: false, meta: { hideable: false, className: 'w-full' },
    cell: c => {
      const row = c.row.original
      const { nodes, edges } = linePath(row, false)
      return (
        <div className="flex items-center gap-2">
          <LandingColorBar id={row.landingId} className="h-5 self-center" />
          <PathDiagram nodes={nodes} edges={edges} size="sm" />
        </div>
      )
    },
  },
  { id: 'kind', header: '类型', enableSorting: false, cell: c => KIND_LABEL[c.row.original.lineKind], meta: { label: '类型' } },
  { accessorKey: 'multiplier', header: '倍率', enableSorting: false, meta: { align: 'right', label: '倍率' }, cell: c => `×${c.getValue<number>()}` },
]

export function LinesReorderSection() {
  const [rows, setRows] = useState(LINES)
  const [log, setLog] = useState('尚未移动')
  return (
    <Section id="lines" title="线路列表（拖动排序，路径单元格）" note="手柄可拖动，也可聚焦后按空格拿起、方向键移动、空格放下。每次移动只上报 (movedId, beforeId)。">
      <DataTable
        aria-label="线路"
        data={rows}
        columns={lineColumns}
        getRowId={r => r.id}
        rowLabel={r => `${r.landing} · ${r.entry}`}
        pagination={false}
        columnMenu={false}
        onReorder={call => { setRows(prev => applyReorder(prev, r => r.id, call)); setLog(`移动 ${call.movedId}，放到 ${call.beforeId ?? '末尾'} 之前`) }}
      />
      <p className="text-xs text-muted-foreground" role="status">{log}</p>
    </Section>
  )
}

// ---------------------------------------------------------------- tree: by landing

interface TreeRow { id: string; name: string; sub: string; status: LineStatus; kind: string; children?: TreeRow[] }
function treeOf(groups: LandingGroupRow[]): TreeRow[] {
  return groups.map(g => ({
    id: g.id, name: g.name, sub: g.entry, status: g.status, kind: '落地节点',
    children: (g.lines ?? []).map(l => ({ id: l.id, name: `${l.entry}`, sub: `:${l.port}`, status: l.status, kind: KIND_LABEL[l.lineKind] })),
  }))
}
const treeColumns: ColumnDef<TreeRow>[] = [
  { accessorKey: 'name', header: '名称', meta: { hideable: false, className: 'font-medium' }, enableSorting: false },
  { accessorKey: 'kind', header: '类型', enableSorting: false },
  { accessorKey: 'sub', header: '入口 / 端口', enableSorting: false },
  { accessorKey: 'status', header: '状态', enableSorting: false, cell: c => { const v = c.getValue<LineStatus>(); return <StatusCell status={v === 'ok' ? 'up' : v} /> } },
]

export function TreeSection() {
  const data = useMemo(() => treeOf(LANDING_TREE), [])
  return (
    <Section id="tree" title="按落地节点分组（树形行）" note="父行下缩进显示子行。聚焦父行后按右箭头展开、左箭头收起。">
      <DataTable aria-label="按落地节点" data={data} columns={treeColumns} getRowId={r => r.id} rowLabel={r => r.name} getSubRows={r => r.children} defaultExpanded pagination={false} columnMenu={false} />
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
