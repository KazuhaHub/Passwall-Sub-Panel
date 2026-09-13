import { client } from './client'

// CompatStatus mirrors internal/version.CompatStatus.String() — keep in
// sync if either side changes.
export type CompatStatus = 'supported' | 'too_old' | 'untested' | 'unknown'

export type XUIAuthMethod = '' | 'token' | 'password'
export type PanelType = '3xui' | 'sui' | 'psp'
export type NativeCoreEngine = 'xray' | 'sing-box'
export type PanelCapability =
  | 'inbound.read' | 'inbound.write'
  | 'inbound.create' | 'inbound.update' | 'inbound.delete' | 'inbound.enable'
  | 'client.read' | 'client.write' | 'traffic.read' | 'status.read'
  | 'panel.upgrade' | 'core.upgrade' | 'webcert.read' | 'reality.scan'
  | 'client.iplimit' | 'client.devicelimit'

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
	/** Native nodes: actual engine/version last reported by the running daemon. */
	core_engine?: NativeCoreEngine
	core_version?: string
	/** Native nodes: exact catalog selection PSP will continue delivering. */
	desired_core_engine?: NativeCoreEngine
	desired_core_version?: string
  version_checked_at?: string
  compat_status?: CompatStatus
  compat_message?: string
  // Upstream-update snapshot (v3.6.0-beta.8). 3X-UI itself queries GitHub
  // and returns these via /getPanelUpdateInfo — Passwall Panel doesn't
  // touch GitHub for this. Drives the ⋮ kebab "new version" badge.
  latest_xui_version?: string
  update_available?: boolean
  /**
   * What the node's fail2ban probe concluded about the concurrent-IP cap.
   * 3X-UI panels only; absent on S-UI, which has no such cap at all.
   *
   * Not a capability. 3X-UI stores `limitIp` on every supported version and
   * says nothing about acting on it — enforcement needs fail2ban on the box
   * and `XUI_ENABLE_FAIL2BAN` unset or literally "true".
   *
   * - `enforced`        the client is disconnected and its IP banned
   * - `disconnect_only` Windows: disconnected, but nothing keeps the IP out
   * - `disabled`        the env var is set to something other than "true"
   * - `not_installed`   fail2ban is missing, so the job gives up
   * - `unknown`         no answer yet from a panel that should be able to give one
   * - `unsupported`     panel predates 3.7.0, which added the route
   */
  ip_limit_enforcement?: IPLimitEnforcement
  /** When the current state was established — NOT when a probe was last attempted. */
  ip_limit_probed_at?: string
}

export type IPLimitEnforcement =
  | 'enforced'
  | 'disconnect_only'
  | 'disabled'
  | 'not_installed'
  | 'unknown'
  | 'unsupported'

export interface CreateServerRequest {
	panel_type?: PanelType
	name: string
	url?: string
  api_token?: string
  username?: string
  password?: string
  remark?: string
  auth_method?: XUIAuthMethod
  insecure_https?: boolean
}

export interface NativeServerProvisioning {
	server: Server
	agent_id: string
	/** Long-lived secret, returned only by administrator provisioning routes. Never part of Server lists. */
	credential: string
	endpoint: string
}

export interface NativeAgentStatus {
  state: 'waiting' | 'offline' | 'unconfigured' | 'applying' | 'running' | 'error'
  last_seen?: string
  core_state?: string
  configured_nodes: number
}

export type NativeInstallMethod = 'linux' | 'docker' | 'manual'
export type NativeInstallOS = 'linux' | 'darwin' | 'windows'
export type NativeInstallArch = 'amd64' | 'arm64'

export interface NativeInstallationSelection {
  method: NativeInstallMethod
  os: NativeInstallOS
  arch: NativeInstallArch
}

export interface NativeInstallationFiles {
  method: 'docker' | 'manual'
  os: NativeInstallOS
  arch?: NativeInstallArch
  files: { name: string; content: string; sensitive?: boolean }[]
  steps: { id?: string; title: string; commands?: string[]; description?: string }[]
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
	core_engine?: NativeCoreEngine
	core_version?: string
  compat_status?: CompatStatus
  compat_message?: string
  version_checked_at?: string
  // v3.6.0-beta.8: upstream update info piggybacked on the same Test
  // click. Absent on GetPanelUpdateInfo failure (3X-UI can't reach
  // GitHub, etc.) — UI keeps the previously-cached values in items.
  latest_xui_version?: string
  update_available?: boolean
  /**
   * Refreshed on the same click, so an admin who just installed fail2ban does
   * not wait out the next traffic-poll probe. Absent when the probe itself failed,
   * in which case the stored state is left alone rather than reset — a blip
   * must not turn "enforced" into "unknown".
   */
  ip_limit_enforcement?: IPLimitEnforcement
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
	const { data } = await client.post<Server | NativeServerProvisioning>('/admin/servers', req)
	return data
}

export async function rotateNativeCredential(id: number) {
	const { data } = await client.post<NativeServerProvisioning>(`/admin/servers/${id}/rotate-node-credential`)
	return data
}

export async function getNativeInstallation(id: number, signal?: AbortSignal) {
  const { data } = await client.get<NativeServerProvisioning>(`/admin/servers/${id}/node-installation`, {
    signal, _skipErrorToast: true,
  })
  return data
}

export async function importNativeCredential(id: number, credential: string, signal?: AbortSignal) {
  const { data } = await client.post<{ ok: boolean }>(`/admin/servers/${id}/node-credential`,
    { credential }, { signal, _skipErrorToast: true },
  )
  return data
}

export async function createNativeInstallScript(id: number, version: string, signal?: AbortSignal) {
  const { data } = await client.post<string>(`/admin/servers/${id}/node-install-script`,
    { version }, { responseType: 'text', signal, _skipErrorToast: true },
  )
  return data
}

export interface NodeInstallCommand {
  server_id: number
  command: string
  expires_at: string
}

export async function createNodeInstallCommand(id: number, input: { version: string }, signal?: AbortSignal) {
  const { data } = await client.post<NodeInstallCommand>(`/admin/servers/${id}/node-install-command`, input,
    { signal, _skipErrorToast: true })
  return data
}

export async function createNodeMigrationCommand(id: number, input: {
  version: string
  fingerprint: string
  core_version: string
  allow_restricted_reality: boolean
  managed_only: true
  confirm_single_instance: true
}, signal?: AbortSignal) {
  const { data } = await client.post<NodeInstallCommand>(`/admin/servers/${id}/node-migration-command`, input,
    { signal, _skipErrorToast: true })
  return data
}

export async function createNativeInstallationFiles(id: number, version: string, selection: NativeInstallationSelection, signal?: AbortSignal) {
  const { data } = await client.post<NativeInstallationFiles>(`/admin/servers/${id}/node-installation-files`,
    { version, method: selection.method, ...(selection.method === 'manual' ? { os: selection.os, arch: selection.arch } : {}) },
    { signal, _skipErrorToast: true },
  )
  return data
}

export async function getNativeAgentStatus(id: number, signal?: AbortSignal) {
  const { data } = await client.get<NativeAgentStatus>(`/admin/servers/${id}/node-agent-status`, {
    signal, _skipErrorToast: true,
  })
  return data
}

export interface NodeMigrationIssue {
  code: string
  node_id?: number
  client_id?: number
}

/** Read-only inspection of PSP-managed data; never includes credentials. */
export interface NodeMigrationPreview {
  server_id: number
  server_name: string
  core_version: string
  recommended_core_version: string
  core_requires_ack: boolean
  allow_restricted_reality: boolean
  fingerprint: string
  node_count: number
  client_count: number
  blockers: NodeMigrationIssue[]
  warnings: NodeMigrationIssue[]
  can_migrate: boolean
}

export interface NodeMigrationPreviewOptions {
  core_version?: string
  allow_restricted_reality?: boolean
}

export async function getNodeMigrationPreview(id: number, signal?: AbortSignal, options: NodeMigrationPreviewOptions = {}) {
  const { data } = await client.get<NodeMigrationPreview>(`/admin/servers/${id}/node-migration-preview`, {
    params: options, signal, _skipErrorToast: true,
  })
  return data
}

export interface NativeAgentUpgrade {
  task_id: string
  agent_id: string
  version: string
  expected_version: string
  status: 'queued' | 'offered' | 'succeeded' | 'failed' | 'indeterminate'
  upgrade_state: 'queued' | 'offered' | 'dispatch_closed' | 'failed' | 'manual_attention' | 'awaiting_observation' | 'verified'
  not_after_ms: number
  dispatch_closed: boolean
  dispatch_closed_reason?: string
  binary_sha256?: string
  observed_version?: string
  last_seen?: string
  completed_at?: string
}

export async function requestNativeAgentUpgrade(id: number, version: string, expectedVersion: string, key: string, signal?: AbortSignal) {
  const { data } = await client.post<NativeAgentUpgrade>(`/admin/servers/${id}/upgrade-node-agent`,
    { version, expected_version: expectedVersion }, { headers: { 'Idempotency-Key': key }, signal, _skipErrorToast: true })
  return data
}

export async function getNativeAgentUpgrade(id: number, taskID: string, signal?: AbortSignal) {
  const { data } = await client.get<NativeAgentUpgrade>(`/admin/servers/${id}/node-agent-upgrades/${encodeURIComponent(taskID)}`,
    { signal, _skipErrorToast: true })
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
	engine?: NativeCoreEngine
  version?: string
  message?: string
  error?: string
}

export interface CoreRelease {
	engine: NativeCoreEngine
	version: string
	tier: 'recommended' | 'verified' | 'config_verified' | 'restricted'
	prerelease: boolean
	selectable: boolean
	requires_confirmation: boolean
	published_at: string
	source_url: string
	summary: { en: string; zh_cn: string }
	reality: {
		xray: string
		mihomo: string
		sing_box: string
		uri_list: string
		server_min_client_ver?: string
		mihomo_fingerprint?: string
		mihomo_x25519mlkem768?: boolean
	}
	evidence: {
		source_audited: boolean
		config_tested: boolean
		handshake_tested: boolean
		verified_at?: string
		handshakes?: Array<{
			client: 'xray' | 'mihomo' | 'sing_box'
			version: string
			platform: string
			profile: string
			result: 'pass' | 'expected_failure'
		}>
	}
}

// Legacy 3X-UI treats an omitted version as "latest". A native PSP node treats
// it as the catalog's recommended exact release and rejects latest/unlisted
// values. The caller normally sends the explicit selector value for native.
export async function upgradeXray(id: number, version?: string, options?: { confirmRestricted?: boolean }) {
	const body = {
		...(version ? { version } : {}),
		...(options?.confirmRestricted ? { confirm_restricted: true } : {}),
	}
  // _skipErrorToast: submitXrayUpgrade and batchUpgradeXray own their own
  // toasts (a single-row catch + an aggregate summary); the global interceptor
  // would otherwise double-toast each failure.
  const { data } = await client.post<UpgradeXrayResult>(`/admin/servers/${id}/upgrade-xray`, body, { _skipErrorToast: true })
  return data
}

// listXrayVersions returns the versions a backend can install. Native nodes
// include the shared audited release metadata; legacy 3X-UI returns its own
// tag list. A legacy failure can still fall back to latest, while native
// selection fails closed when its catalog cannot be loaded.
export async function listXrayVersions(id: number) {
	const { data } = await client.get<{ versions: string[]; releases?: CoreRelease[] }>(`/admin/servers/${id}/xray-versions`)
	return { versions: data.versions ?? [], releases: data.releases ?? [] }
}

export async function listCoreReleases(id: number) {
	const { data } = await client.get<{ releases?: CoreRelease[] }>(`/admin/servers/${id}/core-releases`)
	return data.releases ?? []
}

export async function selectCore(
	id: number,
	engine: NativeCoreEngine,
	version: string,
	options?: { confirmRestricted?: boolean },
) {
	const body = {
		engine,
		version,
		...(options?.confirmRestricted ? { confirm_restricted: true } : {}),
	}
	const { data } = await client.post<UpgradeXrayResult>(`/admin/servers/${id}/select-core`, body, { _skipErrorToast: true })
	return data
}
