import { withFrontConfirm } from '@/utils/frontConfirm'
import { withTunnelConfirm, type TunnelConfirmation, type TunnelPlanPresenter } from '@/utils/tunnelConfirm'
import { client } from './client'
import type { RouteInput } from './routes'
import type { ListResponse, Node, RelayLine, UnmanagedInbound } from './types'

export interface AdminInbound {
  id: number
  remark: string
  enable: boolean
  listen: string
  port: number
  protocol: string
  settings: string
  stream_settings: string
  sniffing: string
  allocate: string
  expiry_time: number
}

export interface ImportNodeRequest {
  /** The admin's go-ahead for joining a shared port this inbound lands on. */
  front_confirm?: { loopback_port: number }
  panel_id: number
  inbound_id: number
  display_name: string
  server_address: string
  flow?: string
  /** Source inbound protocol (lowercased), cached on the node for
   *  protocol-specific UI gating. */
  protocol?: string
  region: string
  tags?: string[]
  sort_order?: number
  /** Direct line multiplier in thousandths; omit for 1000. */
  traffic_factor_milli?: number
}

export interface UpdateNodeMetadataRequest {
  display_name?: string
  server_address?: string
  flow?: string
  region?: string
  tags?: string[]
  sort_order?: number
  /** Full-replace the transit lines. Omit to leave them (and hide_direct)
   *  untouched; send [] to clear. */
  relays?: RelayLine[]
  hide_direct?: boolean
  /** Direct line multiplier in thousandths (0..100000). Omit to keep the saved value. */
  traffic_factor_milli?: number
  /** Exit server (0: the node's own). Omit to leave it unchanged. */
  egress_panel_id?: number
}

export type TransitLinkProtocol = 'ss2022' | 'vless_reality'

/** The tunnel between two Passwall Node servers. Credentials never leave PSP. */
export interface TransitLink {
  /** False for a pair no node uses yet: the fields are the defaults. */
  exists: boolean
  protocol: TransitLinkProtocol
  /** Address the entry dials; empty uses server_address. */
  exit_address: string
  reality_server_name: string
  reality_target: string
  port?: number
  /** Where PSP last saw the exit server sync from. */
  server_address: string
  up_bytes: number
  down_bytes: number
  /** True when REALITY cannot be used: the entry runs sing-box and the exit Xray. */
  reality_refused?: boolean
}

export interface UpdateTransitLinkRequest {
  protocol: TransitLinkProtocol
  exit_address: string
  reality_server_name?: string
  reality_target?: string
}

export async function getTransitLink(entry: number, exit: number) {
  const { data } = await client.get<TransitLink>(`/admin/transit/links/${entry}/${exit}`)
  return data
}

export async function updateTransitLink(entry: number, exit: number, req: UpdateTransitLinkRequest) {
  const { data } = await client.put<TransitLink>(`/admin/transit/links/${entry}/${exit}`, req)
  return data
}

export interface InboundSpec {
  remark: string
  enable: boolean
  listen: string
  port: number
  protocol: string
  settings: string
  stream_settings: string
  sniffing: string
  allocate: string
  expiry_time?: number
  /** Update-only hint: inject this managed certificate before the adapter
   * writes a TLS inbound. Kept on the same DTO for API compatibility. */
  cert_source?: 'psp_managed'
  cert_id?: number
  /** The admin's go-ahead for joining a shared port this inbound lands on. */
  front_confirm?: { loopback_port: number }
}

export interface CreateInboundRequest {
  panel_id: number
  display_name: string
  server_address: string
  flow?: string
  region: string
  tags?: string[]
  sort_order?: number
  cert_source?: 'psp_managed'
  cert_id?: number
  /** Direct line multiplier in thousandths; omit for 1000. */
  traffic_factor_milli?: number
  relays?: RelayLine[]
  hide_direct?: boolean
  egress_panel_id?: number
  setup?: NodeSetupOptions
  inbound: InboundSpec
}

export interface NodeSetupEntry {
  listen: string
  sni: string
  target: string
}

export interface NodeSetupOptions {
  loopbacks: Record<number, number>
  tunnels: Record<number, NodeSetupEntry>
  link_ports?: Record<string, number>
  routes?: RouteInput[]
  confirm?: string
}

export interface NodeSetupBinding {
  panel_id: number
  peer_id?: number
  kind: 'landing' | 'forward' | 'tunnel' | 'link'
  name: string
  address?: string
  listen?: string
  port: number
  loopback?: number
  front_id?: number
  sni?: string[]
  target?: string
  reused?: boolean
}

export interface NodeSetupPlan {
  bindings: NodeSetupBinding[]
  token: string
  warnings?: string[]
}

export async function previewNodeSetup(req: CreateInboundRequest) {
  const { data } = await client.post<NodeSetupPlan>('/admin/nodes/setup-preview', req, { _skipErrorToast: true })
  return data
}

export interface RealityKeypair {
  private_key: string
  public_key: string
  short_id: string
}

export interface RealityScanResult {
  target: string
  host: string
  ip: string
  port: number
  feasible: boolean
  tls13: boolean
  tlsVersion: string
  h2: boolean
  alpn: string
  x25519: boolean
  curveID: string
  certValid: boolean
  certSubject: string
  certIssuer: string
  notAfter: string
  serverNames: string[]
  latencyMs: number
  reason: string
}

export interface RealityScanResponse {
  source_panel_id: number
  source_panel_name: string
  items: RealityScanResult[]
}

export interface NodeListParams {
  page?: number
  page_size?: number
  keyword?: string
  sort_by?: string
  sort_dir?: 'asc' | 'desc'
}

export async function listNodes(params: NodeListParams = {}, signal?: AbortSignal): Promise<ListResponse<Node>> {
  const { data } = await client.get<ListResponse<Node>>('/admin/nodes', { params, signal })
  return data
}

export async function getNode(id: number, opts: { signal?: AbortSignal; quiet?: boolean } = {}) {
  const { data } = await client.get<{
    node: Node
    inbound?: AdminInbound
    clients: unknown[]
    inbound_error?: string
    clients_error?: string
  }>(`/admin/nodes/${id}`, { signal: opts.signal, _skipErrorToast: opts.quiet })
  return data
}

export async function importNode(req: ImportNodeRequest) {
  return withFrontConfirm(async c => importNodeOnce(c ? { ...req, front_confirm: { loopback_port: c.loopback_port ?? 0 } } : req))
}

async function importNodeOnce(req: ImportNodeRequest) {
  const { data } = await client.post<Node>('/admin/nodes/import', req)
  return data
}

/** Separator: a layout-only divider rendered as a DIRECT proxy in the
 *  subscription. Lives in the dedicated nodes_separator table.
 *  Visibility model (rc.4):
 *   - mode='global'     : visible in every group
 *   - mode='node_bound' : visible only when the group contains at least
 *                         one node from node_ids
 *  Position is always SortOrder (admin drags in NodesView). */
export type SeparatorMode = 'global' | 'node_bound'

export interface Separator {
  id: number
  display_name: string
  sort_order: number
  enabled: boolean
  mode: SeparatorMode
  node_ids: number[]
  created_at?: string
}

export interface SeparatorRequest {
  display_name: string
  sort_order?: number
  enabled?: boolean
  mode?: SeparatorMode
  node_ids?: number[]
}

export async function listSeparators(): Promise<Separator[]> {
  // Always loaded in full — typical fleets have a handful of separator
  // entries that interleave into the node list by sort_order. Request a
  // page_size large enough to cover any realistic count; the backend
  // clamps to 200.
  const { data } = await client.get<ListResponse<Separator>>(
    '/admin/nodes/separator', { params: { page: 1, page_size: 200 } },
  )
  return data.items
}

export async function createSeparator(req: SeparatorRequest) {
  const { data } = await client.post<Separator>('/admin/nodes/separator', req)
  return data
}

export async function updateSeparator(id: number, req: SeparatorRequest) {
  const { data } = await client.put<Separator>(`/admin/nodes/separator/${id}`, req)
  return data
}

export async function deleteSeparator(id: number) {
  await client.delete(`/admin/nodes/separator/${id}`)
}

export interface SeparatorReorderItem {
  id: number
  sort_order: number
}

export async function reorderSeparators(items: SeparatorReorderItem[]) {
  await client.put('/admin/nodes/separator/reorder', { items })
}

export async function createInbound(req: CreateInboundRequest) {
  return withFrontConfirm(c => createInboundOnce(c ? { ...req, inbound: { ...req.inbound, front_confirm: { loopback_port: c.loopback_port ?? 0 } } } : req))
}

async function createInboundOnce(req: CreateInboundRequest) {
  // Quiet: the guided setup shows the error inline, so a toast would say it twice.
  const { data } = await client.post<Node | { queued: true }>('/admin/nodes', req, { _skipErrorToast: true })
  return data
}

export async function generateRealityKeypair() {
  const { data } = await client.post<RealityKeypair>('/admin/nodes/generate-reality-keypair')
  return data
}

/** Run REALITY discovery from the selected 3X-UI/Xray node. PSP only
 * proxies this request; it never probes the target from its own host. */
export async function scanRealityTargets(panelId: number, targets?: string) {
  const { data } = await client.post<RealityScanResponse>('/admin/nodes/reality-targets/scan', {
    panel_id: panelId,
    targets: targets?.trim() || '',
  }, {
    // The selected 3X-UI host may process up to 512 domain probes in 32
    // concurrent waves (10s each). Keep this override local to scanning; the
    // shared client retains its 30s default for every other API call.
    timeout: 185_000,
  })
  return data
}

/** Query flags of a node change that the admin has confirmed. */
function confirmParams(tunnels?: TunnelConfirmation, front?: { confirm_front?: boolean }): Record<string, string> | undefined {
  const out: Record<string, string> = {}
  if (tunnels) out.confirm_tunnels = 'true'
  if (tunnels?.plan) out.confirm_plan = tunnels.plan
  if (front?.confirm_front) out.confirm_front = 'true'
  return Object.keys(out).length > 0 ? out : undefined
}

/** `present`, here and on the other tunnel-confirming calls below, renders the server's tunnel plan in the caller's own dialog (see withTunnelConfirm). */
export async function updateNodeMetadata(id: number, req: UpdateNodeMetadataRequest, present?: TunnelPlanPresenter) {
  // Removing a tunnel line removes its listener: the server asks first.
  return withTunnelConfirm(tunnels => withFrontConfirm(async front => {
    const params = confirmParams(tunnels, front)
    const { data } = params
      ? await client.put<Node>(`/admin/nodes/${id}/metadata`, req, { params })
      : await client.put<Node>(`/admin/nodes/${id}/metadata`, req)
    return data
  }), undefined, present)
}

/** The public key that belongs to a REALITY private key; a malformed key answers 400. */
export async function deriveRealityPublicKey(privateKey: string) {
  const { data } = await client.post<{ public_key: string }>('/admin/nodes/reality-public-key', { private_key: privateKey }, { _skipErrorToast: true })
  return data.public_key
}

export async function updateInboundConfig(id: number, req: InboundSpec) {
  await withFrontConfirm(c => client.put(`/admin/nodes/${id}/inbound`,
    c ? { ...req, front_confirm: { loopback_port: c.loopback_port ?? 0 } } : req))
}

export async function setNodeEnabled(id: number, enabled: boolean) {
  await withFrontConfirm(c => client.post(`/admin/nodes/${id}/set-enabled`, c?.confirm_front ? { enabled, confirm_front: true } : { enabled }))
}

export async function deleteNode(id: number, present?: TunnelPlanPresenter) {
  // A listener behind a shared port leaves it when deleted, and a landing's tunnel listeners go with it: the server asks first.
  await withTunnelConfirm(tunnels => withFrontConfirm(c => {
    const params = confirmParams(tunnels, c)
    return client.delete(`/admin/nodes/${id}`, params ? { params } : undefined)
  }), undefined, present)
}

// recreateNodeInbound rebuilds the node's inbound on its (repointed/empty) server
// from PSP's captured config and relinks the node to the new inbound id. Use after
// moving a node's Server to a fresh 3X-UI that shows "Connected (0)".
export async function recreateNodeInbound(id: number) {
  await client.post(`/admin/nodes/${id}/recreate-inbound`)
}

// detachNode drops the node record + ownership whitelist locally and does
// NOT contact 3X-UI. Use when the upstream server is offline so PSP doesn't
// burn retries against a dead panel; any clients PSP previously created
// stay on 3X-UI for the admin to clean up there.
export async function detachNode(id: number, present?: TunnelPlanPresenter) {
  await withTunnelConfirm(tunnels => withFrontConfirm(c => {
    const params = confirmParams(tunnels, c)
    return client.post(`/admin/nodes/${id}/detach`, undefined, params ? { params } : undefined)
  }), undefined, present)
}

const quiet = { _skipErrorToast: true } as const

/**
 * Switches a saved line between a direct forward and an encrypted tunnel. This
 * acts on the stored line, never on unsaved editor changes. Creating or
 * removing a listener is confirmed first (see withTunnelConfirm). Switching to
 * a forward takes the entry port (0: the server picks a free one).
 */
export async function switchLineMode(nodeId: number, lineId: string, mode: 'tunnel' | 'forward', port = 0, present?: TunnelPlanPresenter): Promise<Node> {
  // A listener joining or leaving a shared port is asked about too, in the server's words.
  return withTunnelConfirm(confirmed => withFrontConfirm(async front => {
    const body = { mode, ...(port > 0 ? { port } : {}), ...(confirmed ? { confirm: true, ...(confirmed.plan ? { confirm_plan: confirmed.plan } : {}) } : {}), ...(front?.confirm_front ? { confirm_front: true } : {}) }
    const { data } = await client.post<Node>(`/admin/nodes/${nodeId}/lines/${lineId}/mode`, body, quiet)
    return data
  }), undefined, present)
}

/** What the admin edits on a tunnel line's listener; empty or 0 keeps the saved value. */
export interface TunnelListenerInput {
  port: number
  server_name: string
  target: string
  short_id: string
  private_key: string
  public_key: string
  fingerprint: string
}

export async function updateTunnelListener(nodeId: number, lineId: string, input: TunnelListenerInput): Promise<RelayLine[]> {
  // A port on a shared port joins it only once the admin agreed, in the server's words.
  return withFrontConfirm(async front => {
    const { data } = await client.put<{ relays: RelayLine[] }>(`/admin/nodes/${nodeId}/lines/${lineId}/tunnel`, input, {
      ...quiet, ...(front?.confirm_front ? { params: { confirm_front: 'true' } } : {}),
    })
    return data.relays ?? []
  })
}

export interface TunnelDiffEntry { field: string; from: string; to: string }

/** What copying the landing's protocol settings onto the listener would change. A landing that cannot be copied answers 400. */
export async function previewTunnelSync(nodeId: number, lineId: string): Promise<TunnelDiffEntry[]> {
  const { data } = await client.get<{ diff: TunnelDiffEntry[] | null }>(`/admin/nodes/${nodeId}/lines/${lineId}/tunnel/sync`, quiet)
  return data.diff ?? []
}

/** Copies the landing's protocol settings onto the listener once the admin confirmed the diff (asked here, `note` is added to the question); its own port, names and keys stay. */
export async function syncTunnelFromLanding(nodeId: number, lineId: string, note?: string, present?: TunnelPlanPresenter): Promise<RelayLine[]> {
  // The first request is refused with the diff; it is shown, and the plan it carries is echoed on the confirmed one.
  return withTunnelConfirm(confirmed => withFrontConfirm(async front => {
    const body = {
      ...(confirmed ? { confirm: true, ...(confirmed.plan ? { confirm_plan: confirmed.plan } : {}) } : {}),
      ...(front?.confirm_front ? { confirm_front: true } : {}),
    }
    const { data } = await client.post<{ diff: TunnelDiffEntry[]; relays: RelayLine[] }>(`/admin/nodes/${nodeId}/lines/${lineId}/tunnel/sync`, body, quiet)
    return data.relays ?? []
  }), note, present)
}

export interface NodeReorderItem {
  id: number
  sort_order: number
}

export async function reorderNodes(items: NodeReorderItem[]) {
  await client.put('/admin/nodes/reorder', { items })
}

// listUnmanagedInbounds is scoped to one panel — the caller picks a server
// and only that panel is queried (one 3X-UI round trip; a dead panel can't
// stall the others). The backend returns an empty list when panel_id is absent.
export async function listUnmanagedInbounds(panelId: number, opts: { signal?: AbortSignal } = {}) {
  const { data } = await client.get<ListResponse<UnmanagedInbound>>('/admin/nodes/unmanaged', {
    params: { panel_id: panelId },
    signal: opts.signal,
  })
  return data
}

export async function claimClient(req: {
  user_id: number
  panel_id: number
  inbound_id: number
  client_email: string
  client_uuid?: string
}) {
  await client.post('/admin/nodes/-/claim', req)
}

/** The node editor's unsaved labels, in editor row order (ADR 0041). */
export interface LineVisibilityDraft {
  server_address: string
  region: string
  tags: string[]
  relays: { id?: string; region: string; tags: string[] }[]
}

/** Which groups would see each draft line; empty means none, never unknown. */
export interface LineVisibilityView {
  groups: { id: number; name: string }[]
  node_groups: number[]
  /** Groups the stored node passes; with node_groups, those a save removes. */
  saved_node_groups: number[]
  relays: number[][]
}

/** Read-only and unaudited: called as the editor's typing pauses. A reply
 *  without one list per draft row is treated as unreadable, never as "none". */
export async function previewLineVisibility(nodeId: number, draft: LineVisibilityDraft, signal?: AbortSignal): Promise<LineVisibilityView> {
  const { data } = await client.post<LineVisibilityView>(`/admin/nodes/${nodeId}/line-visibility`, draft, { signal, _skipErrorToast: true })
  const rows = (value: unknown, count: number) => {
    if (!Array.isArray(value) || value.length !== count || !value.every(Array.isArray)) throw new Error('malformed line visibility')
    return value as number[][]
  }
  if (!Array.isArray(data?.node_groups) || !Array.isArray(data?.saved_node_groups)) throw new Error('malformed line visibility')
  return {
    groups: Array.isArray(data?.groups) ? data.groups : [],
    node_groups: data.node_groups,
    saved_node_groups: data.saved_node_groups,
    relays: rows(data?.relays, draft.relays.length),
  }
}

/** Which groups would show a separator with this rule (ADR 0042 §4). */
export interface SeparatorGroupsPreview {
  groups: { id: number; name: string }[]
  visible: number[]
}

/** Read-only and unaudited; evaluated as if the separator were enabled. */
export async function previewSeparatorGroups(mode: 'global' | 'node_bound', nodeIds: number[], signal?: AbortSignal): Promise<SeparatorGroupsPreview> {
  const { data } = await client.post<SeparatorGroupsPreview>('/admin/nodes/separator/group-preview', { mode, node_ids: nodeIds }, { signal, _skipErrorToast: true })
  if (!data || !Array.isArray(data.groups) || !Array.isArray(data.visible)) throw new Error('malformed separator preview')
  return data
}
