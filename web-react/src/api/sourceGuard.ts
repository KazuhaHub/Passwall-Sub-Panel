import { client } from './client'
import type { ReadOptions } from './requestOptions'

// Source guard (handler/admin_source_guard.go, adminGroup only): where a relay
// line's traffic reaches its landing from, and what the landing refused.

/** Cloudflare's address ranges, the default source set of a CDN line. */
export interface CloudflareRanges {
  cidrs: string[]
  /** 'fetched' is the last good fetch from Cloudflare; 'builtin' the snapshot shipped with the panel. */
  source: 'fetched' | 'builtin'
  fetched_at?: string
  /** When the built-in snapshot was taken (YYYY-MM-DD). */
  builtin_date: string
  auto_fetch: boolean
}

export interface RefusedSource {
  ip: string
  count: number
  last_seen: string
}

export interface LineSourceGuard {
  line_id: string
  /** False for a tunnel line, which has no source set. */
  guarded: boolean
  /** The set the landing is sent for the line; empty when it is not known yet. */
  effective_cidrs: string[]
  /** Why the guard keeps the line out of subscriptions. */
  withheld?: 'source_guard_factor' | 'source_guard_unknown'
  refused: RefusedSource[]
}

export interface NodeSourceGuard {
  /** Whether the landing's server can check where connections come from. */
  can_guard: boolean
  lines: LineSourceGuard[]
  cloudflare: CloudflareRanges
}

const quiet = { _skipErrorToast: true } as const

export async function getCloudflareRanges(opts: ReadOptions = {}): Promise<CloudflareRanges> {
  const { data } = await client.get<CloudflareRanges>('/admin/source-guard/cloudflare', { signal: opts.signal, ...quiet })
  return data
}

export async function getNodeSourceGuard(nodeId: number, opts: ReadOptions = {}): Promise<NodeSourceGuard> {
  const { data } = await client.get<NodeSourceGuard>(`/admin/nodes/${nodeId}/source-guard`, { signal: opts.signal, ...quiet })
  return data
}

/** Adds a refused address to a line's source addresses through the node's ordinary save. */
export async function addRefusedSource(nodeId: number, lineId: string, ip: string): Promise<string[]> {
  const { data } = await client.post<{ source_cidrs: string[] }>(`/admin/nodes/${nodeId}/lines/${lineId}/source-cidrs`, { ip })
  return data.source_cidrs ?? []
}

/** Source addresses as typed: separated by new lines, spaces or commas. */
export function parseSourceCIDRs(text: string): string[] {
  return text.split(/[\s,，;；]+/).map(s => s.trim()).filter(Boolean)
}
