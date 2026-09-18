import { client } from './client'

/**
 * Administrator-requested remote diagnostics.
 *
 * THE STATUS DESCRIBES THE TASK, NOT THE REQUEST. The panel allows one active
 * collection per server and returns the running one rather than starting a
 * second, so `sections` here is what is actually being collected — which can
 * differ from what the caller asked for. Rendering the request instead would
 * show an operator something that is not happening.
 *
 * THE RESULT IS ADMINISTRATOR-ONLY on the server side; these endpoints live in
 * the admin route group, which is what carries that.
 */

/** Section 13.5's lifecycle. Every value is something an operator can act on. */
export type NodeDiagnosticStatus =
  | 'queued'
  | 'offered'
  | 'succeeded'
  | 'failed'
  | 'indeterminate'

/** A task that will not change again. `indeterminate` is a terminal state too: the outcome is unknowable. */
export const TERMINAL_NODE_DIAGNOSTIC_STATUSES: readonly NodeDiagnosticStatus[] = [
  'succeeded', 'failed', 'indeterminate',
]

export function isTerminalNodeDiagnostic(status: NodeDiagnosticStatus): boolean {
  return TERMINAL_NODE_DIAGNOSTIC_STATUSES.includes(status)
}

/** The sections a caller may ask for. Section 13.1's allowlist, in the order the node lists them. */
export const NODE_DIAGNOSTIC_SECTIONS = ['host', 'runtime', 'state', 'events'] as const
export type NodeDiagnosticSection = (typeof NODE_DIAGNOSTIC_SECTIONS)[number]

/** A check's verdict, matching the doctor an operator runs by hand. */
export type NodeDiagnosticCheckStatus = 'ok' | 'warning' | 'failed' | 'unavailable'

export interface NodeDiagnosticCheck {
  code: string
  status: NodeDiagnosticCheckStatus
  summary: string
}

export interface NodeDiagnosticEvent {
  code: string
  at_ms: number
  severity: 'info' | 'warning' | 'error'
  summary: string
}

export interface NodeDiagnosticRuntime {
  core_state: string
  core_config_digest: string
}

export interface NodeDiagnosticState {
  sqlite_quick_check: string
  outbox_pending: number
  tasks_queued: number
}

/**
 * What the node produced.
 *
 * A SECTION IS ABSENT, NOT EMPTY, WHEN IT WAS NOT REQUESTED — the same rule the
 * host observation follows. "Not asked" and "asked and found nothing" are
 * different statements, and a component that renders an absent section as an
 * empty one is claiming the panel looked.
 */
export interface NodeDiagnosticResult {
  schema_version: number
  collected_at_ms: number
  recovered: boolean
  truncated: boolean
  host?: Record<string, unknown>
  runtime?: NodeDiagnosticRuntime
  state?: NodeDiagnosticState
  events?: NodeDiagnosticEvent[]
  checks: NodeDiagnosticCheck[]
}

export interface NodeDiagnostic {
  task_id: string
  agent_id: string
  sections: string[]
  max_events: number
  status: NodeDiagnosticStatus
  not_after_ms: number
  dispatch_closed: boolean
  dispatch_closed_reason?: string
  collected_at_ms?: number
  recovered?: boolean
  truncated?: boolean
  /** Present only for a task that succeeded. */
  result?: NodeDiagnosticResult
  completed_at?: string
}

/** Records the intent to collect, or returns the collection already running. */
export async function requestNodeDiagnostic(
  serverId: number,
  body: { sections: string[]; max_events: number },
): Promise<NodeDiagnostic> {
  const { data } = await client.post(`/admin/servers/${serverId}/node-diagnostics`, body)
  return data
}

export async function getNodeDiagnostic(
  serverId: number,
  taskId: string,
  signal?: AbortSignal,
): Promise<NodeDiagnostic> {
  const { data } = await client.get(`/admin/servers/${serverId}/node-diagnostics/${taskId}`, { signal })
  return data
}
