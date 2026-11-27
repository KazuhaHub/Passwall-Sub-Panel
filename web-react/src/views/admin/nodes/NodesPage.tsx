import { useMemo } from 'react'
import { Navigate, useNavigate, useSearchParams } from 'react-router'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { PageHeader } from '@/components/common/PageHeader'
import { urlCodecs, useUrlState } from '@/hooks/useUrlState'
import { useUrlSelection } from '@/hooks/useUrlSelection'
import { useAllServers, useNodeOrder } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { useQueryClient } from '@tanstack/react-query'
import { nodeKeys } from '@/query/keys'
import { writableServers } from './create/writable'
import LinesTab from './list/LinesTab'
import { useNodesT } from './shared/useNodesT'
import UnmanagedTab from './unmanaged/UnmanagedTab'

const TABS = ['lines', 'unmanaged'] as const

/**
 * Where the old query strings go (design P4, P6). `?tab=lines` was the lines table, now the by-node view;
 * `?tab=managed` was the default; `?inbound=<id>` opened the inbound dialog, now the node's Inbound tab.
 * An operator has no node page, so the parameter only goes away, as it did before.
 */
export function legacyRedirect(search: string, admin: boolean): string | null {
  const p = new URLSearchParams(search)
  const inbound = p.get('inbound')
  if (inbound !== null) {
    if (admin && /^\d+$/.test(inbound)) return `/admin/nodes/${inbound}?tab=inbound`
    p.delete('inbound')
    return `?${p.toString()}`
  }
  const tab = p.get('tab')
  if (tab === 'lines') { p.delete('tab'); p.set('view', 'node'); return `?${p.toString()}` }
  if (tab === 'managed') { p.delete('tab'); return `?${p.toString()}` }
  return null
}

function CreateButton() {
  const t = useNodesT()
  const navigate = useNavigate()
  const scope = useQueryScope()
  const servers = useAllServers(scope)
  const loading = servers.isPending
  const none = !loading && (servers.data?.length ?? 0) === 0
  // Disabled WITH its reason: a button that does nothing, or a toast after the click, says why too late.
  const reason = loading ? undefined : none ? t('nodes.create_dialog.no_servers') : writableServers(servers.data ?? []).length === 0 ? t('nodes.create_dialog.no_writable_servers') : undefined
  const off = loading || reason !== undefined
  const button = (
    <Button aria-disabled={off || undefined} className="aria-disabled:cursor-not-allowed aria-disabled:opacity-50" onClick={() => { if (!off) navigate('/admin/nodes/new') }}>
      {t('nodes.create')}
    </Button>
  )
  return reason ? <Tooltip><TooltipTrigger render={button} /><TooltipContent>{reason}</TooltipContent></Tooltip> : button
}

function Page() {
  const t = useNodesT()
  const admin = useCan('config.write')
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const [tab, setTab] = useUrlState('tab', urlCodecs.oneOf(TABS, 'lines'))
  const sepSel = useUrlSelection('sep')
  const order = useNodeOrder(scope)
  const lineCount = useMemo(() => order.data?.rows.filter(r => r.kind === 'line').length, [order.data])
  // The shell gives the content area no padding so every page pads itself, as the node and create pages do.
  return (
    <div className="p-4 sm:p-6">
      <PageHeader
        title={t('nodes.title')}
        count={lineCount === undefined ? undefined : t('nodes.list.count', { count: lineCount })}
        secondary={admin && <Button variant="outline" onClick={() => sepSel.select('new')}>{t('nodes.action.create_separator')}</Button>}
        primary={admin && <CreateButton />}
        more={[{ id: 'refresh', label: t('nodes.action.refresh'), onSelect: () => void queryClient.invalidateQueries({ queryKey: nodeKeys.all(scope) }) }]}
      />
      <Tabs value={tab} onValueChange={v => setTab(v as (typeof TABS)[number])}>
        <TabsList>
          <TabsTrigger value="lines">{t('nodes.tabs.lines')}</TabsTrigger>
          <TabsTrigger value="unmanaged">{t('nodes.tabs.unmanaged')}</TabsTrigger>
        </TabsList>
      </Tabs>
      <div className="mt-3">
        {tab === 'lines' ? <LinesTab /> : <UnmanagedTab onImported={() => setTab('lines')} />}
      </div>
    </div>
  )
}

export default function NodesPage() {
  const admin = useCan('config.write')
  const [params] = useSearchParams()
  const redirect = legacyRedirect(params.toString(), admin)
  if (redirect !== null) return <Navigate to={redirect} replace />
  return <Page />
}
