import { useMemo } from 'react'
import { useAllNodes, useAllServers, useNodeOrder } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'
import type { Node } from '@/api/types'
import type { Server } from '@/api/servers'
import { rowId } from './orderMoves'
import type { ListRow } from './types'

const NONE: never[] = []

/**
 * The three reads behind the list and what they make together: the order (every line and separator with its
 * publication state) joined to the nodes (tags, protocol, config push, exit) by node id. The order is the list;
 * the join only decorates it, so a failed or slow join leaves the list working with those columns empty
 * (design 2.7), and a failed server read leaves entry names, which come from the order itself.
 *
 * The node join reads EVERY page of GET /nodes (the server caps a page at 200): reading one page silently
 * dropped nodes above that, so search, tags and the exit were missing for them (design gap G1).
 */
export function useNodeList({ enabled = true }: { enabled?: boolean } = {}) {
  const scope = useQueryScope()
  const order = useNodeOrder(scope, { enabled })
  const nodes = useAllNodes(scope, { enabled })
  const servers = useAllServers(scope, { enabled })

  const nodeMap = useMemo(() => new Map<number, Node>((nodes.data ?? NONE).map(n => [n.id, n])), [nodes.data])
  const serverMap = useMemo(() => new Map<number, Server>((servers.data ?? NONE).map(s => [s.id, s])), [servers.data])
  const rows = order.data?.rows ?? NONE

  const items = useMemo<ListRow[]>(() => rows.map(row => {
    const node = row.kind === 'line' ? nodeMap.get(row.node_id ?? 0) : undefined
    const exit = node?.egress_panel_id ? serverMap.get(node.egress_panel_id)?.name : undefined
    return { id: rowId(row), row, node, exitName: exit }
  }), [rows, nodeMap, serverMap])

  return { scope, order, nodes, servers, nodeMap, serverMap, rows, items }
}
