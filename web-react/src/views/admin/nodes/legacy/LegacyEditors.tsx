// The host of the legacy MUI editors (node metadata and lines, inbound, create wizard). Until N2 and N3 replace
// them, every "edit" entry of the new list and the registered detail and create routes open one of these, so the
// old behaviour is reachable from the new page without being rewritten. One editor is open at a time.
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { getNode } from '@/api/nodes'
import type { Node } from '@/api/types'
import { useAllNodes, useAllServers } from '@/query/nodes'
import { nodeKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { registerServerNames } from '@/utils/tunnelConfirm'
import CreateNodeDialog from './CreateNodeDialog'
import InboundEditDialog from './InboundEditDialog'
import MetadataEditDialog from './MetadataEditDialog'

type Request =
  | { kind: 'meta'; node: Node; lineId?: string | null; addLine?: boolean; seq: number }
  | { kind: 'inbound'; node: Node; seq: number }
  | { kind: 'create'; seq: number }

export interface LegacyEditorsApi {
  /** The metadata and lines editor; `lineId` scrolls to a line, `addLine` appends a blank one. */
  openEdit: (node: Node | number, opts?: { lineId?: string; addLine?: boolean }) => void
  openInbound: (node: Node | number) => void
  /** False (and nothing opens) while no server can create inbounds; the caller says why. */
  openCreate: () => boolean
  /** Whether the create wizard can open at all: a server that can create and delete inbounds exists. */
  canCreate: boolean
}

const Ctx = createContext<LegacyEditorsApi | null>(null)

export function useLegacyEditors(): LegacyEditorsApi {
  const api = useContext(Ctx)
  if (!api) throw new Error('useLegacyEditors needs a LegacyEditorsProvider')
  return api
}

const NONE: never[] = []

export function LegacyEditorsProvider({ children, onClosed }: { children: ReactNode; onClosed?: () => void }) {
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const nodesQuery = useAllNodes(scope)
  const serversQuery = useAllServers(scope)
  const nodes = nodesQuery.data ?? NONE
  const servers = serversQuery.data ?? NONE
  const [request, setRequest] = useState<Request | null>(null)
  const seq = useRef(0)

  // Tunnel confirmations name the servers whose ports open and close.
  useEffect(() => { registerServerNames(servers) }, [servers])

  const allTags = useMemo(() => {
    const set = new Set<string>()
    for (const n of nodes) for (const tg of n.tags ?? []) if (tg) set.add(tg)
    return Array.from(set).sort((a, b) => a.localeCompare(b))
  }, [nodes])
  const writable = useMemo(
    () => servers.filter(s => s.capabilities?.includes('inbound.create') && s.capabilities?.includes('inbound.delete')),
    [servers],
  )

  // The list's copy when it has the node, otherwise one read: the join may have failed or not arrived yet.
  const resolve = useCallback(async (node: Node | number): Promise<Node | null> => {
    if (typeof node !== 'number') return node
    const known = nodes.find(n => n.id === node)
    if (known) return known
    try { return (await getNode(node)).node } catch { return null /* the request layer already surfaced it */ }
  }, [nodes])

  const mutateManaged = useCallback((updater: (prev: Node[]) => Node[]) => {
    queryClient.setQueryData<Node[]>(nodeKeys.allNodes(scope), prev => (prev ? updater(prev) : prev))
  }, [queryClient, scope])
  // A write changes the node and, through it, the order's rows and their publication state.
  const changed = useCallback(() => { void queryClient.invalidateQueries({ queryKey: nodeKeys.all(scope) }) }, [queryClient, scope])
  const close = useCallback(() => { setRequest(null); onClosed?.() }, [onClosed])

  const api = useMemo<LegacyEditorsApi>(() => ({
    openEdit: (node, opts) => { void resolve(node).then(n => { if (n) setRequest({ kind: 'meta', node: n, lineId: opts?.lineId, addLine: opts?.addLine, seq: ++seq.current }) }) },
    openInbound: node => { void resolve(node).then(n => { if (n) setRequest({ kind: 'inbound', node: n, seq: ++seq.current }) }) },
    openCreate: () => {
      if (writable.length === 0) return false
      setRequest({ kind: 'create', seq: ++seq.current })
      return true
    },
    canCreate: writable.length > 0,
  }), [resolve, writable.length])

  return (
    <Ctx.Provider value={api}>
      {children}
      {request?.kind === 'meta' && (
        <MetadataEditDialog key={request.seq} node={request.node} lineId={request.lineId} addLine={request.addLine}
          servers={servers} nodes={nodes} allTags={allTags} onClose={close} mutateManaged={mutateManaged} onSaved={changed}
          onSwitchNode={n => setRequest({ kind: 'meta', node: n, seq: ++seq.current })}
          onOpenInbound={n => setRequest({ kind: 'inbound', node: n, seq: ++seq.current })} />
      )}
      {request?.kind === 'inbound' && (
        <InboundEditDialog key={request.seq} node={request.node} servers={servers} onClose={close} onSaved={changed} />
      )}
      {request?.kind === 'create' && (
        <CreateNodeDialog key={request.seq} servers={servers} allTags={allTags} onClose={close} onCreated={changed} />
      )}
    </Ctx.Provider>
  )
}
