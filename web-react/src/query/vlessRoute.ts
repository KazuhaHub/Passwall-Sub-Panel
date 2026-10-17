import { useEffect, useRef, useState } from 'react'
import { isAxiosError } from 'axios'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { getRouteLines, getRouteOutbounds, routeLineInput, saveRouteLines, type RouteLinesView, type RouteOutbound, type VlessRouteLine } from '@/api/vlessRoute'
import { nodeKeys } from './keys'
import type { QueryScope } from './session'
import { freshness, policies } from './policies'

const inputs = (lines: VlessRouteLine[]) => lines.map(routeLineInput)

function reconcileSnapshot(baseline: RouteLinesView | null, lines: VlessRouteLine[], latest: RouteLinesView) {
  const edited = baseline !== null && JSON.stringify(inputs(lines)) !== JSON.stringify(inputs(baseline.state.lines))
  const targetChanged = baseline !== null && JSON.stringify(inputs(latest.state.lines)) !== JSON.stringify(inputs(baseline.state.lines))
  // Newly saved lines receive server IDs. A readback matching the submitted
  // target confirms them without treating those assigned IDs as a conflict.
  const requested = inputs(lines)
  const matchesDraft = JSON.stringify(inputs(latest.state.lines).map((line, index) => ({
    ...line, id: requested[index]?.id ? line.id : undefined,
  }))) === JSON.stringify(requested)
  const conflict = edited && targetChanged && !matchesDraft
  return {
    conflict,
    baseline: conflict && baseline ? { ...latest, state: { ...latest.state, revision: baseline.state.revision, lines: baseline.state.lines } } : latest,
    lines: !edited || matchesDraft ? latest.state.lines.map(line => ({ ...line })) : lines,
  }
}

/** One draft per editor opening. Focus/poll reads never overwrite unsaved edits. */
export function useVlessRouteDraft(scope: QueryScope, nodeId: number, session: number, active: boolean) {
  const client = useQueryClient()
  const key = [...nodeKeys.all(scope), 'route-lines', nodeId, session] as const
  const [baseline, setBaseline] = useState<RouteLinesView | null>(null)
  const [lines, setLines] = useState<VlessRouteLine[]>([])
  const [outbounds, setOutbounds] = useState<RouteOutbound[]>([])
  const [catalogError, setCatalogError] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [conflict, setConflict] = useState(false)
  const appliedRead = useRef<unknown>(null)
  useEffect(() => { setBaseline(null); setLines([]); setOutbounds([]); setCatalogError(false); setConflict(false); setRefreshing(false); appliedRead.current = null }, [nodeId, session, scope.apiBase, scope.userId, scope.role, scope.authEpoch])
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
        return { view, catalog: [] as RouteOutbound[], catalogError: true }
      }
    },
  })
  useEffect(() => {
    if (!query.data || query.isFetching || appliedRead.current === query.data) return
    appliedRead.current = query.data
    const latest = query.data.view
    // A check may advance only the status revision. Adopt it without touching
    // edits. A changed target must not silently upgrade an old draft's CAS.
    const next = reconcileSnapshot(baseline, lines, latest)
    setConflict(next.conflict)
    setBaseline(next.baseline)
    setLines(next.lines)
    setOutbounds(query.data.catalog)
    setCatalogError(query.data.catalogError)
  }, [baseline, lines, query.data, query.isFetching])
  const dirty = baseline !== null && JSON.stringify(inputs(lines)) !== JSON.stringify(inputs(baseline.state.lines))
  const needsSave = !!baseline?.supported && (dirty || ['unknown', 'applying', 'drift'].includes(baseline.state.status))
  async function refresh() {
    setRefreshing(true)
    try { await query.refetch({ cancelRefetch: false }) }
    finally { setRefreshing(false) }
  }
  async function save() {
    if (!baseline || !needsSave) return null
    if (conflict || query.isFetching || refreshing || query.isError) throw new Error('Routing status must be refreshed before saving')
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
      if (latest && latest.state.revision !== baseline.state.revision) {
        const next = reconcileSnapshot(baseline, lines, latest)
        setBaseline(next.baseline); setLines(next.lines); setConflict(next.conflict)
      }
      throw error
    }
  }
  return { baseline, lines, setLines, outbounds, catalogError, refreshing, refresh, conflict, needsSave, save,
    loading: query.isFetching || active && !baseline && !query.isError, readError: query.isError, reload: refresh }
}

export type VlessRouteDraft = ReturnType<typeof useVlessRouteDraft>
