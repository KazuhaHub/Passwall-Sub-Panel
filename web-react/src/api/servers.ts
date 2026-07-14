import { client } from './client'

// CompatStatus mirrors internal/version.CompatStatus.String() — keep in
// sync if either side changes.
export type CompatStatus = 'supported' | 'too_old' | 'untested' | 'unknown'

export type XUIAuthMethod = 'token' | 'password'
export type PanelType = '3xui' | 'sui'
export type PanelCapability =
  | 'inbound.read' | 'inbound.write'
  | 'inbound.create' | 'inbound.update' | 'inbound.delete' | 'inbound.enable'
  | 'client.read' | 'client.write' | 'traffic.read' | 'status.read'
  | 'panel.upgrade' | 'core.upgrade' | 'webcert.read' | 'reality.scan'

export interface Server {
  id: number
  panel_type: PanelType
  capabilities: PanelCapability[]
  name: string
  url: string
  username?: string
  remark?: string
  has_api_token: boolean
  has_password: boolean
  /** Effective auth mode (server resolves "" → token/password by inference). */
  auth_method: XUIAuthMethod
  /** Skip TLS cert verification when connecting to this panel (self-signed). */
  insecure_https: boolean
  // Version-identity snapshot from the last successful probe (boot probe
  // + traffic-poll piggyback + admin "test connection" trigger). Empty
  // strings + missing version_checked_at == "never probed".
  panel_version?: string
  xray_version?: string
  version_checked_at?: string
  compat_status?: CompatStatus
  compat_message?: string
  // Upstream-update snapshot (v3.6.0-beta.8). 3X-UI itself queries GitHub
  // and returns these via /getPanelUpdateInfo — Passwall Panel doesn't
  // touch GitHub for this. Drives the ⋮ kebab "new version" badge.
  latest_xui_version?: string
  update_available?: boolean
}

export interface CreateServerRequest {
  panel_type?: PanelType
  name: string
  url: string
  api_token?: string
  username?: string
  password?: string
  remark?: string
  auth_method?: XUIAuthMethod
  insecure_https?: boolean
}

export interface UpdateServerRequest {
  panel_type?: PanelType
  name?: string
  url?: string
  api_token?: string
  username?: string
  password?: string
  remark?: string
  auth_method?: XUIAuthMethod
  insecure_https?: boolean
}

export interface TestResult {
  ok: boolean
  error?: string
  inbound_count?: number
  // Version probe piggybacks on a successful test (admin "test connection"
  // doubles as a manual refresh). Absent on a probe failure or pre-v3.6
  // backend.
  panel_version?: string
  xray_version?: string
  xray_state?: string
  compat_status?: CompatStatus
  compat_message?: string
  version_checked_at?: string
  // v3.6.0-beta.8: upstream update info piggybacked on the same Test
  // click. Absent on GetPanelUpdateInfo failure (3X-UI can't reach
  // GitHub, etc.) — UI keeps the previously-cached values in items.
  latest_xui_version?: string
  update_available?: boolean
}

export interface ServerListParams {
  page?: number
  page_size?: number
  keyword?: string
  sort_by?: string
  sort_dir?: 'asc' | 'desc'
}

export async function listServers(params: ServerListParams = {}, signal?: AbortSignal) {
  const { data } = await client.get<{ items: Server[]; total: number; page?: number; page_size?: number }>(
    '/admin/servers', { params, signal },
  )
  return data
}

export async function createServer(req: CreateServerRequest) {
  const { data } = await client.post<Server>('/admin/servers', req)
  return data
}

export async function updateServer(id: number, req: UpdateServerRequest) {
  const { data } = await client.put<Server>(`/admin/servers/${id}`, req)
  return data
}

export async function deleteServer(id: number) {
  await client.delete(`/admin/servers/${id}`)
}

export async function testServer(id: number) {
  const { data } = await client.post<TestResult>('/admin/servers/probe', { id })
  return data
}

// UpgradePanelResult mirrors the backend's gin.H response from
// AdminServersHandler.UpgradePanel:
//   - 202 success: { ok: true, started: true, target_version, message }
//   - 409 conflict (target untested): { ok: false, reason: "untested_target",
//       latest_version, psp_min_xui, psp_max_xui, message }
//   - 502 bad gateway (call failed mid-fire): { ok: false, error }
// Axios surfaces non-2xx via the throw path so we read the same shape from
// AxiosError.response.data in the catch site.
export interface UpgradePanelResult {
  ok: boolean
  started?: boolean
  // already_latest: the panel was already on the newest release, so nothing
  // was fired (200, not a failure). Lets the UI report "already latest"
  // instead of counting it as initiated/failed.
  already_latest?: boolean
  current_version?: string
  target_version?: string
  message?: string
  reason?: string
  latest_version?: string
  compat_status?: CompatStatus
  psp_min_xui?: string
  psp_max_xui?: string
  /** When true, the rejection is recoverable via a second call with
   *  {force: true}. False / absent means the rejection is structural
   *  (e.g. panel unreachable) and force won't help. */
  can_force?: boolean
  error?: string
}

// upgradePanel optionally takes a force flag. When the first (non-force)
// call is rejected with 409 reason:"untested_target", the frontend can
// re-send with force=true after admin acknowledges the risk. The backend
// audits forced upgrades under a distinct action (panel_upgrade_forced)
// so the trail makes it obvious which upgrades bypassed the gate.
export async function upgradePanel(id: number, opts?: { force?: boolean }) {
  const body = opts?.force ? { force: true } : {}
  // _skipErrorToast: the callers (runUpgradePanel's two-step gate +
  // batchUpgradePanel's aggregate summary) own all messaging. Without this the
  // global interceptor fires a raw "Request failed with status code 409" toast
  // on the EXPECTED untested_target gate — clashing with the intentional
  // confirm dialog / the batch's "blocked" summary.
  const { data } = await client.post<UpgradePanelResult>(`/admin/servers/${id}/upgrade-panel`, body, { _skipErrorToast: true })
  return data
}

// XUIAdvisory is the admin-facing pre-upgrade heads-up for a specific 3X-UI
// target version, sourced from the remotely-updatable compat JSON. Mirrors
// internal/version.XUIAdvisory.
export interface XUIAdvisory {
  severity: 'info' | 'warning'
  affects_xray: boolean
  text: string
}

// UpgradePreviewResult mirrors AdminServersHandler.UpgradePreview — the
// READ-ONLY pre-flight the upgrade confirm dialog reads BEFORE firing anything:
// what version the panel would jump to (3X-UI /updatePanel only pulls latest),
// whether it's inside PSP's tested range, and any breaking-change advisory.
export interface UpgradePreviewResult {
  update_available: boolean
  current_version?: string
  target_version?: string
  // already_latest: nothing to upgrade — the UI shows "already latest" and
  // skips the confirm entirely.
  already_latest?: boolean
  compat_status?: CompatStatus
  compat_message?: string
  psp_min_xui?: string
  psp_max_xui?: string
  can_force?: boolean
  advisory?: XUIAdvisory
}

// upgradePreview fetches the target version + tested-range check + advisory for
// a panel WITHOUT firing anything, so the confirm dialog can warn about breaking
// changes (especially ones that also restart/upgrade the bundled Xray) before
// the admin commits. Read-only GET.
export async function upgradePreview(id: number) {
  // _skipErrorToast: runUpgradePanel catches a preview failure and silently
  // falls back to a generic confirm (the UpgradePanel gate still protects the
  // fire), so the global interceptor must NOT also fire a raw error toast.
  const { data } = await client.get<UpgradePreviewResult>(`/admin/servers/${id}/upgrade-preview`, { _skipErrorToast: true })
  return data
}

export interface UpgradeXrayResult {
  ok: boolean
  version?: string
  message?: string
  error?: string
}

// upgradeXray defaults version to "latest" on the backend when the field is
// empty / missing — pass undefined for the common "give me latest" case,
// or a specific tag like "v25.10.31" to pin a release.
export async function upgradeXray(id: number, version?: string) {
  const body = version ? { version } : {}
  // _skipErrorToast: submitXrayUpgrade and batchUpgradeXray own their own
  // toasts (a single-row catch + an aggregate summary); the global interceptor
  // would otherwise double-toast each failure.
  const { data } = await client.post<UpgradeXrayResult>(`/admin/servers/${id}/upgrade-xray`, body, { _skipErrorToast: true })
  return data
}

// listXrayVersions returns the xray-core tags 3X-UI knows it can install
// on this panel. Used by the Upgrade Xray dialog to populate its version
// dropdown. Failure is recoverable — UI falls back to a single "latest"
// option so admin can still upgrade without browsing tags.
export async function listXrayVersions(id: number) {
  const { data } = await client.get<{ versions: string[] }>(`/admin/servers/${id}/xray-versions`)
  return data.versions ?? []
}
