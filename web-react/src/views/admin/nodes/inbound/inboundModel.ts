// AUTOIMPORT-BEGIN
import type { Server } from '@/api/servers'
import type { RelayLine } from '@/api/types'
// AUTOIMPORT-END
export type CreateProtocol = 'vless' | 'vmess' | 'trojan' | 'ss2022' | 'hysteria2' | 'anytls' | 'tuic' | 'naive'
export type VlessNetwork = 'tcp' | 'ws' | 'grpc' | 'httpupgrade' | 'http' | 'xhttp'
export type VlessSecurity = 'none' | 'tls' | 'reality'
export type SS2022Method = '2022-blake3-aes-128-gcm' | '2022-blake3-aes-256-gcm' | '2022-blake3-chacha20-poly1305'

// usesVlessStream returns true for protocols whose stream settings reuse
// the VLESS-shaped fields (network + security + transport opts). VMess and
// Trojan ride on the same Xray transport layer; only the inner protocol
// settings differ. Used by both the form UI and the buildStreamSettings
// dispatcher to gate REALITY/flow/encryption knobs.
export function usesVlessStream(p: CreateProtocol): boolean {
  return p === 'vless' || p === 'vmess' || p === 'trojan'
}

// ALPN default is protocol-specific: Hysteria2 runs over QUIC and MUST advertise
// h3 (the QUIC ALPN) — and this value is written into the 3X-UI inbound's
// tlsSettings, so a wrong default breaks the server too, not just the rendered
// client. TCP-based TLS (VLESS/VMess/Trojan) uses h2,http/1.1 (h3 can't be
// negotiated over TCP). Used to prefill / retarget the ALPN field on protocol
// change.
export const ALPN_HY2 = 'h3'
export const ALPN_TLS = 'h2,http/1.1'

export function defaultAlpn(p: CreateProtocol): string {
  return p === 'hysteria2' || p === 'tuic' ? ALPN_HY2 : ALPN_TLS
}

/** A relay row in the editor: the line plus client-only edit state. */
export type RelayDraft = RelayLine & {
  /** Stable React key (the line id, or a client key for new rows). */
  _key?: string
  /** Multiplier as typed; absent until edited. */
  _factor_text?: string
  /** The admin changed this line's multiplier in this edit session. */
  _factor_touched?: boolean
  /** Source addresses as typed; absent until edited. */
  _source_text?: string
}
/** The entry select's value for a CDN line (a server id is never negative). */
export const CDN_ENTRY = -1
let relayDraftSeq = 0
export const newRelayKey = () => `new-${++relayDraftSeq}`

export interface MetaForm {
  display_name: string
  server_address: string
  flow: string
  region: string
  tags_text: string
  sort_order: number
  relays: RelayDraft[]
  hide_direct: boolean
  /** Direct line multiplier in thousandths. */
  traffic_factor_milli?: number
  /** Direct multiplier as typed; absent until edited. */
  traffic_factor_text?: string
  /** Exit server of the node's traffic; 0 is its own. Edit dialog only. */
  egress_panel_id?: number
}

export interface ImportForm extends MetaForm {
  panel_id: number
  panel_name: string
  inbound_id: number
  // Source inbound protocol (lowercased, e.g. "vless"/"shadowsocks").
  // Drives whether the Flow picker is shown — flow is a VLESS-only knob.
  protocol: string
}

export interface InboundFormState {
  panel_id: number
  display_name: string
  server_address: string
  region: string
  tags_text: string
  sort_order: number
  listen: string
  port: number
  enable: boolean
  protocol: CreateProtocol
  // VLESS
  vless_flow: string
  vless_encryption: string
  vless_network: VlessNetwork
  vless_security: VlessSecurity
  // TCP
  tcp_accept_proxy_protocol: boolean
  tcp_header_type: string
  // WS
  ws_accept_proxy_protocol: boolean
  ws_path: string
  ws_host: string
  // gRPC
  grpc_service_name: string
  grpc_authority: string
  grpc_multi_mode: boolean
  // HTTPUpgrade — same path/host shape as WS, plus the proxy-protocol
  // toggle. Newer Xray transport that pierces some CDNs cleaner than WS.
  httpupgrade_path: string
  httpupgrade_host: string
  httpupgrade_accept_proxy_protocol: boolean
  // XHTTP — Xray 1.8.20+ universal HTTP transport. Modes:
  //   "packet-up"  classic bidi HTTP/1.1 + upgrade
  //   "stream-up"  HTTP/2 server-streaming
  //   "stream-one" HTTP/3 / single-stream
  // Empty mode = use Xray default.
  xhttp_path: string
  xhttp_host: string
  xhttp_mode: string
  // TLS
  tls_server_name: string
  tls_alpn_text: string
  tls_min_version: string
  tls_max_version: string
  // Xray expects one colon-delimited string, not a JSON array.
  tls_cipher_suites: string
  // utls browser fingerprint — anti-detection knob; "chrome" / "firefox" /
  // "safari" / "ios" / "android" / "edge" / "360" / "qq" / "random" /
  // "randomized". Empty = no fingerprint set. VLESS+Vision deployments
  // almost always want "chrome" or "randomized".
  tls_fingerprint: string
  // Skip cert chain verification when the client connects. Dev/debug only;
  // production should always be false.
  tls_allow_insecure: boolean
  // Reject TLS handshakes whose SNI isn't in serverName. Tightens an
  // inbound that hosts multiple SNIs by name.
  tls_reject_unknown_sni: boolean
  // Cert source: 'file' uses paths on the 3X-UI host's filesystem (production
  // pattern with acme/certbot), 'inline' pastes PEM directly into the config
  // (one-off / testing), 'psp_managed' binds a PSP-issued ACME cert that the
  // backend deploys + auto-renews. Empty / 'none' = no certificates emitted
  // (REALITY and plain mode don't need them). This single selector replaces
  // the older split between a tls_cert_mode dropdown and a separate managed-
  // cert picker, which let an admin pick contradictory sources at once.
  tls_cert_mode: '' | 'file' | 'inline' | 'psp_managed'
  tls_cert_file: string
  tls_key_file: string
  tls_cert_pem: string
  tls_key_pem: string
  // cert_id: the bound PSP-managed certificate (tls_certificates row), used
  // only when tls_cert_mode === 'psp_managed'. 0 = none picked.
  cert_id: number
  // Reality
  reality_dest: string
  reality_server_names_text: string
  private_key: string
  public_key: string
  short_ids_text: string
  reality_fingerprint: string
  reality_spider_x: string
  reality_xver: number
  reality_max_timediff: number
  reality_min_client: string
  reality_max_client: string
  // SS-2022
  ss_method: SS2022Method
  ss_password: string
  ss_network: string
  // ivCheck rejects replayed initialization vectors at the server. Off
  // in 3X-UI's default; admins can flip it on for stricter replay
  // protection.
  ss_iv_check: boolean
  // Hysteria 2 in 3X-UI is an Xray transport (network: "hysteria"). Per-
  // user auth lives in clients[].auth; obfs lives in
  // streamSettings.finalmask.udp[] as a salamander mask, NOT under
  // settings.obfs. SNI/ALPN reuse tls_server_name / tls_alpn_text. The
  // upstream-Hysteria bandwidth/ignoreClientBandwidth fields are NOT
  // accepted by Xray's hysteria2 transport — don't add them.
  hy2_obfs_password: string // empty = no salamander obfs
  // Inbound-level UDP idle timeout (seconds). Default 60 in 3X-UI.
  hy2_udp_idle_timeout: number
  // 伪装 (masquerade): how the server responds to plain HTTPS probes.
  // Lives in streamSettings.hysteriaSettings.masquerade. Type 'proxy'
  // reverse-proxies to a URL; 'file' serves a directory; 'string' returns
  // a literal body. Empty = no masquerade.
  hy2_masquerade_type: '' | 'proxy' | 'file' | 'string'
  // Content semantics depend on hy2_masquerade_type:
  //   proxy  -> upstream URL
  //   file   -> filesystem path (dir)
  //   string -> literal response body
  //   '' / unset -> masquerade block omitted entirely
  hy2_masquerade_content: string
  // S-UI-native modern protocols. These fields live in the canonical
  // settings JSON and are translated by the S-UI adapter into sing-box
  // inbound fields; they are never offered for a 3X-UI server.
  anytls_padding_scheme_text: string
  tuic_congestion_control: 'cubic' | 'new_reno' | 'bbr'
  tuic_auth_timeout: number
  tuic_zero_rtt_handshake: boolean
  tuic_heartbeat: number
  naive_network: '' | 'tcp' | 'udp'
  naive_quic_congestion_control: '' | 'bbr' | 'bbr_standard' | 'bbr2' | 'bbr2_variant' | 'cubic' | 'reno'
  // Sockopt — socket-level tuning at the listener layer. All fields are
  // optional; empty/zero defaults emit nothing. mark is the SO_MARK
  // applied to outgoing sockets (transparent proxy / iptables tagging).
  // tcpFastOpen accepts boolean OR a queue length; we model as bool +
  // queue (queue==0 means just enable). tproxy is the Linux TPROXY
  // mode ("redirect" / "tproxy" / "off"). domainStrategy applies to
  // dialed connections, not listeners — irrelevant for inbound so
  // omitted here.
  sockopt_enabled: boolean
  sockopt_mark: number
  // Xray inbound listener support for the HAProxy PROXY protocol. This is
  // independent from the transport-level acceptProxyProtocol switches.
  sockopt_accept_proxy_protocol: boolean
  sockopt_tcp_fast_open: boolean
  sockopt_tcp_keep_alive_interval: number
  sockopt_tcp_keep_alive_idle: number
  sockopt_tcp_user_timeout: number
  sockopt_tproxy: '' | 'off' | 'redirect' | 'tproxy'
  // Sniffing
  sniffing_enabled: boolean
  sniffing_dest_override_text: string
  sniffing_metadata_only: boolean
  sniffing_route_only: boolean
  // Carry-over for edit (round-trip preserve unknown fields)
  raw_settings?: string
  raw_stream_settings?: string
  raw_sniffing?: string
}

export const EMPTY_META: MetaForm = {
  display_name: '', server_address: '', flow: '', region: '', tags_text: '', sort_order: 0,
  relays: [], hide_direct: false,
}

export const EMPTY_IMPORT: ImportForm = {
  ...EMPTY_META, panel_id: 0, panel_name: '', inbound_id: 0, protocol: '',
}

export const EMPTY_INBOUND: InboundFormState = {
  panel_id: 0,
  display_name: '',
  server_address: '',
  region: '',
  tags_text: '',
  sort_order: 0,
  listen: '',
  port: 443,
  enable: true,
  protocol: 'vless',
  vless_flow: 'xtls-rprx-vision',
  vless_encryption: 'none',
  vless_network: 'tcp',
  vless_security: 'reality',
  tcp_accept_proxy_protocol: false,
  tcp_header_type: 'none',
  ws_accept_proxy_protocol: false,
  ws_path: '/',
  ws_host: '',
  grpc_service_name: '',
  grpc_authority: '',
  grpc_multi_mode: false,
  httpupgrade_path: '/',
  httpupgrade_host: '',
  httpupgrade_accept_proxy_protocol: false,
  xhttp_path: '/',
  xhttp_host: '',
  xhttp_mode: '',
  tls_server_name: '',
  tls_alpn_text: 'h2,http/1.1',
  tls_min_version: '',
  tls_max_version: '',
  tls_cipher_suites: '',
  tls_fingerprint: 'chrome',
  tls_allow_insecure: false,
  tls_reject_unknown_sni: false,
  tls_cert_mode: '',
  tls_cert_file: '',
  tls_key_file: '',
  tls_cert_pem: '',
  tls_key_pem: '',
  cert_id: 0,
  reality_dest: 'www.asus.com:443',
  reality_server_names_text: 'www.asus.com',
  private_key: '',
  public_key: '',
  short_ids_text: '',
  reality_fingerprint: 'chrome',
  reality_spider_x: '/ai',
  reality_xver: 0,
  reality_max_timediff: 0,
  // Keep an explicit 1.0.0 floor for older Xray cores: 26.7.11–26.7.28
  // default an empty minClientVer to 26.3.27, rejecting older client versions.
  // In 26.9.8+ an empty value has no version floor. This version setting does
  // not bypass REALITY's ML-KEM requirements. Existing nodes use the panel's
  // actual value through parseInboundForEdit.
  reality_min_client: '1.0.0',
  reality_max_client: '',
  ss_method: '2022-blake3-aes-256-gcm',
  ss_password: '',
  ss_network: 'tcp,udp',
  ss_iv_check: false,
  hy2_obfs_password: '',
  hy2_udp_idle_timeout: 60,
  hy2_masquerade_type: '',
  hy2_masquerade_content: '',
  anytls_padding_scheme_text: [
    'stop=8', '0=30-30', '1=100-400',
    '2=400-500,c,500-1000,c,500-1000,c,500-1000,c,500-1000',
    '3=9-9,500-1000', '4=500-1000', '5=500-1000', '6=500-1000', '7=500-1000',
  ].join('\n'),
  tuic_congestion_control: 'cubic',
  tuic_auth_timeout: 3,
  tuic_zero_rtt_handshake: false,
  tuic_heartbeat: 10,
  naive_network: '',
  naive_quic_congestion_control: '',
  sockopt_enabled: false,
  sockopt_mark: 0,
  sockopt_accept_proxy_protocol: false,
  sockopt_tcp_fast_open: false,
  sockopt_tcp_keep_alive_interval: 0,
  sockopt_tcp_keep_alive_idle: 0,
  sockopt_tcp_user_timeout: 0,
  sockopt_tproxy: '',
  sniffing_enabled: true,
  sniffing_dest_override_text: 'http,tls,quic,fakedns',
  sniffing_metadata_only: false,
  sniffing_route_only: false,
}

export const PROTOCOL_OPTIONS: { value: CreateProtocol; label: string }[] = [
  { value: 'vless', label: 'VLESS' },
  { value: 'vmess', label: 'VMess' },
  { value: 'trojan', label: 'Trojan' },
  { value: 'ss2022', label: 'Shadowsocks 2022' },
  { value: 'hysteria2', label: 'Hysteria 2' },
  { value: 'anytls', label: 'AnyTLS' },
  { value: 'tuic', label: 'TUIC' },
  { value: 'naive', label: 'Naive' },
]
export const SUI_ONLY_PROTOCOLS = new Set<CreateProtocol>(['anytls', 'tuic', 'naive'])
export const VLESS_NETWORKS: { value: VlessNetwork; label: string }[] = [
  { value: 'tcp', label: 'TCP' },
  { value: 'ws', label: 'WebSocket' },
  { value: 'grpc', label: 'gRPC' },
  { value: 'httpupgrade', label: 'HTTPUpgrade' },
  { value: 'http', label: 'HTTP' },
  { value: 'xhttp', label: 'XHTTP' },
]
export const XHTTP_MODES = ['', 'auto', 'packet-up', 'stream-up', 'stream-one']
export const TPROXY_MODES: InboundFormState['sockopt_tproxy'][] = ['', 'off', 'redirect', 'tproxy']
export const VLESS_SECURITIES: { value: VlessSecurity; label: string }[] = [
  { value: 'none', label: 'None' },
  { value: 'tls', label: 'TLS' },
  { value: 'reality', label: 'Reality' },
]
export const FINGERPRINTS = ['chrome', 'firefox', 'safari', 'ios', 'android', 'edge', '360', 'qq', 'random', 'randomized']
export const VLESS_FLOWS = ['', 'xtls-rprx-vision', 'xtls-rprx-vision-udp443']

// hostFromURL extracts just the hostname from a 3X-UI panel URL so it can
// pre-fill a node's Address. The panel URL is a management endpoint
// (scheme + admin port + path, e.g. https://1.2.3.4:54321/xyz) — we keep
// only the host since the proxy port comes from the inbound and the path /
// admin port are irrelevant to clients. Best-effort: prepends a scheme when
// the stored URL omits one, and returns "" on anything unparseable so the
// caller just leaves the field blank.
export function hostFromURL(raw: string): string {
  if (!raw) return ''
  try {
    const u = new URL(/^[a-z][a-z0-9+.-]*:\/\//i.test(raw) ? raw : `https://${raw}`)
    // PSP URLs identify the agent, not a public proxy host. The operator must
    // supply the actual node address instead of leaking an agent ID into subscriptions.
    return u.protocol === 'psp:' ? '' : u.hostname
  } catch {
    return ''
  }
}
// serverHost is the default connection address of a node on a server: the
// host of a 3X-UI / S-UI panel URL, or, for a Passwall Node server (whose URL
// names the agent, not a host), the address PSP last saw it sync from.
export function serverHost(server?: Pick<Server, 'panel_type' | 'url' | 'address'>): string {
  if (!server) return ''
  if (server.panel_type === 'psp') return server.address ?? ''
  return hostFromURL(server.url)
}

// How often the node editor asks whether a pending tunnel now runs on both
// servers.
export const EGRESS_READY_POLL_MS = 5000
export const TCP_HEADER_TYPES = ['none', 'http']
export const TLS_VERSIONS = ['', '1.0', '1.1', '1.2', '1.3']
export const SS2022_METHODS: { value: SS2022Method; bytes: number }[] = [
  { value: '2022-blake3-aes-128-gcm', bytes: 16 },
  { value: '2022-blake3-aes-256-gcm', bytes: 32 },
  { value: '2022-blake3-chacha20-poly1305', bytes: 32 },
]

export function splitList(value: string): string[] {
  return value.split(/[\n,]/).map(s => s.trim()).filter(Boolean);
}

export function parseJSONSafe(raw: string | undefined): Record<string, unknown> {
  if (!raw?.trim()) return {}
  try { return JSON.parse(raw) as Record<string, unknown> } catch { return {} }
}

export function stringValue(v: unknown, fallback = ''): string {
  return typeof v === 'string' ? v : fallback
}
export function boolValue(v: unknown, fallback = false): boolean {
  return typeof v === 'boolean' ? v : fallback
}
export function numberValue(v: unknown, fallback = 0): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : fallback
}
export function durationSeconds(v: unknown, fallback = 0): number {
  if (typeof v !== 'string') return fallback
  const match = v.trim().match(/^(\d+(?:\.\d+)?)s$/)
  return match ? Number(match[1]) : fallback
}
export function listToText(v: unknown): string {
  return Array.isArray(v) ? v.filter(item => item !== '').join(',') : ''
}

export function randomBase64(byteLength: number): string {
  const bytes = new Uint8Array(byteLength)
  crypto.getRandomValues(bytes)
  let binary = ''
  bytes.forEach(b => { binary += String.fromCharCode(b) })
  return btoa(binary)
}
