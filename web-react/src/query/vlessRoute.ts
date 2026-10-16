import { useEffect, useState } from 'react'
import { isAxiosError } from 'axios'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { getRouteLines, getRouteOutbounds, saveRouteLines, type RouteLinesView, type RouteOutbound, type VlessRouteLine } from '@/api/vlessRoute'
import { nodeKeys } from './keys'
import type { QueryScope } from './session'
import { freshness, policies } from './policies'

const inputs = (lines: VlessRouteLine[]) => lines.map(({ id, name, outbound_tag, enabled }) => ({ id, name: name.trim(), outbound_tag, enabled }))

/** One draft per editor opening. Focus/poll reads never overwrite unsaved edits. */
export function useVlessRouteDraft(scope: QueryScope, nodeId: number, session: number, active: boolean) {
  const client = useQueryClient()
  const key = [...nodeKeys.all(scope), 'route-lines', nodeId, session] as const
  const [baseline, setBaseline] = useState<RouteLinesView | null>(null)
  const [lines, setLines] = useState<VlessRouteLine[]>([])
  const [outbounds, setOutbounds] = useState<RouteOutbound[]>([])
  const [catalogError, setCatalogError] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  useEffect(() => { setBaseline(null); setLines([]); setOutbounds([]); setCatalogError(false) }, [nodeId, session, scope.apiBase, scope.userId, scope.role, scope.authEpoch])
  const query = useQuery({
    queryKey: key,
    enabled: active && nodeId > 0,
    ...freshness(policies.nodeRouteLines),
    refetchOnWindowFocus: false, refetchOnReconnect: false,
    queryFn: async ({ signal }) => {
      const view = await getRouteLines(nodeId, signal)
      if (!view.supported) return { view, catalog: [] as RouteOutbound[], catalogError: false }
      try {
        const catalog = await getRouteOutbounds(nodeId, signal)
        return { view: { ...view, supported: true }, catalog, catalogError: false }
      } catch (error) {
        if (signal.aborted) throw error
        const unsupported = isAxiosError(error) && error.response?.status === 400
        return { view: unsupported ? { ...view, supported: false } : view, catalog: [] as RouteOutbound[], catalogError: true }
      }
    },
  })
  useEffect(() => {
    if (!baseline && query.data && !query.isFetching) {
      setBaseline(query.data.view)
      setLines(query.data.view.state.lines.map(line => ({ ...line })))
      setOutbounds(query.data.catalog)
      setCatalogError(query.data.catalogError)
    }
  }, [baseline, query.data, query.isFetching])
  const dirty = baseline !== null && JSON.stringify(inputs(lines)) !== JSON.stringify(inputs(baseline.state.lines))
  const needsSave = !!baseline?.supported && (dirty || ['unknown', 'applying', 'drift'].includes(baseline.state.status))
  async function refreshOutbounds() {
    setRefreshing(true)
    try { setOutbounds(await getRouteOutbounds(nodeId)); setCatalogError(false); setBaseline(value => value ? { ...value, supported: true } : value) }
    catch { setCatalogError(true) }
    finally { setRefreshing(false) }
  }
  async function save() {
    if (!baseline || !needsSave) return null
    try {
      const saved = await saveRouteLines(nodeId, baseline.state.revision, lines)
      setBaseline(saved); setLines(saved.state.lines.map(line => ({ ...line })))
      client.setQueryData(key, { view: saved, catalog: outbounds, catalogError })
      return saved
    } catch (error) {
      // A timeout may follow a committed remote write. Re-read only; do not
      // automatically issue another save using a stale revision or blank IDs.
      if (!isAxiosError(error) || error.response && error.response.status < 500) throw error
      const latest = await getRouteLines(nodeId).catch(() => null)
      if (latest && latest.state.revision !== baseline.state.revision) { setBaseline(latest); setLines(latest.state.lines.map(line => ({ ...line }))) }
      throw error
    }
  }
  return { baseline, lines, setLines, outbounds, catalogError, refreshing, refreshOutbounds, needsSave, save,
    loading: active && (query.isFetching || !baseline && !query.isError), readError: query.isError, reload: query.refetch }
}

export type VlessRouteDraft = ReturnType<typeof useVlessRouteDraft>
