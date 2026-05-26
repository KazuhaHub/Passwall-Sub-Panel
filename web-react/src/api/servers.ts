import { client } from './client'

// CompatStatus mirrors internal/version.CompatStatus.String() — keep in
// sync if either side changes.
export type CompatStatus = 'supported' | 'too_old' | 'untested' | 'unknown'

export interface Server {
  id: number
  name: string
  url: string
  username?: string
  remark?: string
  has_api_token: boolean
  has_password: boolean
  // Version-identity snapshot from the last successful probe (boot probe
  // + traffic-poll piggyback + admin "test connection" trigger). Empty
  // strings + missing version_checked_at == "never probed".
  panel_version?: string
  xray_version?: string
  version_checked_at?: string
  compat_status?: CompatStatus
  compat_message?: string
}

export interface CreateServerRequest {
  name: string
  url: string
  api_token?: string
  username?: string
  password?: string
  remark?: string
}

export interface UpdateServerRequest {
  name?: string
  url?: string
  api_token?: string
  username?: string
  password?: string
  remark?: string
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
}

export async function listServers() {
  const { data } = await client.get<{ items: Server[] }>('/admin/servers')
  return data.items
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
  target_version?: string
  message?: string
  reason?: string
  latest_version?: string
  psp_min_xui?: string
  psp_max_xui?: string
  error?: string
}

export async function upgradePanel(id: number) {
  const { data } = await client.post<UpgradePanelResult>(`/admin/servers/${id}/upgrade-panel`)
  return data
}

export interface UpgradeXrayResult {
  ok: boolean
  version?: string
  message?: string
  error?: string
}

// upgradeXray defaults version to "latest" on the backend when the field is
// empty / missing — pass undefined for the common "give me latest" case.
export async function upgradeXray(id: number, version?: string) {
  const body = version ? { version } : {}
  const { data } = await client.post<UpgradeXrayResult>(`/admin/servers/${id}/upgrade-xray`, body)
  return data
}
