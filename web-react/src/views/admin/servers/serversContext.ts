import { createContext, useContext } from 'react'
import type { DestNodeStatus } from '@/api/accessControl'
import type { NodeRelease } from '@/api/nodeReleases'
import type { Server } from '@/api/servers'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import type { ProbeState } from './useServerProbes'

/**
 * What the cells of the list read, which changes while the page lives (a probe answers, a release catalog arrives).
 *
 * IT IS A CONTEXT, NOT A CLOSURE IN THE COLUMNS, because TanStack renders a column's `cell` as a component: a column
 * array rebuilt on every change would give every cell a new component type, remounting the whole table each time a probe
 * answers and closing any More menu that was open. The columns stay one stable array; the cells read this.
 */
export interface ServersCtx {
  probeOf: (s: Server) => ProbeState
  accessOf: (s: Server) => DestNodeStatus | undefined
  countsOf: (s: Server) => { nodes: number; lines: number } | undefined
  nodeReleases: NodeRelease[] | null
  nodeReleaseCheckFailed: boolean
  suiReleaseVersion: string | null
  testing: number | null
  onTest: (s: Server) => Promise<void>
  onOpenAccess: (s: Server, node: DestNodeStatus) => void
  menuOf: (s: Server) => MoreMenuItem[]
}

export const ServersContext = createContext<ServersCtx | null>(null)

export function useServersCtx(): ServersCtx {
  const c = useContext(ServersContext)
  if (!c) throw new Error('ServersContext is missing')
  return c
}
