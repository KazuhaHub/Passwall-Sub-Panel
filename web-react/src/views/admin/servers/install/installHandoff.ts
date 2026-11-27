import type { NativeInstallationSelection, NativeServerProvisioning, Server } from '@/api/servers'

/**
 * What the page that created or rotated a Passwall Node's credential hands to its installation page: the record, the
 * one-time provisioning (identity and credential) and, for a node just created, the install method chosen in the form.
 * It lives in memory only, on purpose: router state is written into the browser's history entry and would outlive the
 * page, and a credential must not. A reload before the installation page reads it loses it, and the page then asks the
 * panel for the stored installation like any other visit.
 *
 * `peek` does not forget, because a page reads it while rendering (twice in development); the page calls `drop` once it
 * has kept what it needs.
 */
export interface InstallHandoff {
  server: Server
  provisioning: NativeServerProvisioning
  /** The method chosen while adding the server; absent for a rotation. */
  selection?: NativeInstallationSelection
  /** The server was just created: the page title says so and the "existing record" note is left out. */
  creating: boolean
}

const pending = new Map<number, InstallHandoff>()

export function leaveInstallation(handoff: InstallHandoff) { pending.set(handoff.server.id, handoff) }
export function peekInstallation(serverId: number): InstallHandoff | undefined { return pending.get(serverId) }
export function dropInstallation(serverId: number) { pending.delete(serverId) }
