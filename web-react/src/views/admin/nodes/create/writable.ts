import type { Server } from '@/api/servers'

/** A server a node can be created on: one that can both create and delete inbounds (a created node must be deletable). */
export const writableServers = (servers: readonly Server[]) =>
  servers.filter(s => s.capabilities?.includes('inbound.create') && s.capabilities?.includes('inbound.delete'))
