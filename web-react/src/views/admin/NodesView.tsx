import { useEffect, useMemo, useRef, useState, type FormEvent, type Dispatch, type SetStateAction } from 'react'
import {
  Autocomplete,
  createFilterOptions,
  Box,
  Button,
  Card,
  Checkbox,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  Switch,
  Tab,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Tabs,
  TextField,
  Tooltip,
  Typography,
  alpha,
  useTheme,
} from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import DeleteIcon from '@mui/icons-material/DeleteOutlined'
import DragIndicatorIcon from '@mui/icons-material/DragIndicator'
import EditIcon from '@mui/icons-material/EditOutlined'
import LinkOffIcon from '@mui/icons-material/LinkOff'
import CloudSyncIcon from '@mui/icons-material/CloudSync'
import { useTranslation } from 'react-i18next'
import { useCan } from '@/utils/permissions'
import { formatDualTz } from '@/utils/datetime'
import { useSiteStore } from '@/stores/site'

import {
  claimClient,
  createInbound,
  createSeparator,
  deleteNode,
  deleteSeparator,
  detachNode,
  generateRealityKeypair,
  getNode,
  importNode,
  listNodes,
  listSeparators,
  listUnmanagedInbounds,
  recreateNodeInbound,
  reorderNodes,
  reorderSeparators,
  setNodeEnabled,
  type Separator,
  type SeparatorMode,
  updateInboundConfig,
  updateNodeMetadata,
  updateSeparator,
} from '@/api/nodes'
import { listUsers } from '@/api/users'
import { fetchPanelWebCert, listCerts, setNodeCertSource, type Cert } from '@/api/certs'
import { listServers, type Server } from '@/api/servers'
import { MenuItem, Select, FormControlLabel } from '@mui/material'
import KeyIcon from '@mui/icons-material/VpnKey'
import type { Node, RelayLine, UnmanagedInbound, User } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import PageHeader from '@/components/PageHeader'
import { PagedTableFooter } from '@/components/PagedTableFooter'
import { pushSnack } from '@/components/SnackbarHost'
import { useTabParam } from '@/hooks/useTabParam'
import RealityTargetScannerDialog from './RealityTargetScannerDialog'
import {
  type FieldErrors,
  firstError,
  validateEmail,
  validateHost,
  validateName,
} from '@/utils/validators'

type CreateProtocol = 'vless' | 'vmess' | 'trojan' | 'ss2022' | 'hysteria2'
type VlessNetwork = 'tcp' | 'ws' | 'grpc' | 'httpupgrade' | 'xhttp'
type VlessSecurity = 'none' | 'tls' | 'reality'
type SS2022Method = '2022-blake3-aes-128-gcm' | '2022-blake3-aes-256-gcm' | '2022-blake3-chacha20-poly1305'

// usesVlessStream returns true for protocols whose stream settings reuse
// the VLESS-shaped fields (network + security + transport opts). VMess and
// Trojan ride on the same Xray transport layer; only the inner protocol
// settings differ. Used by both the form UI and the buildStreamSettings
// dispatcher to gate REALITY/flow/encryption knobs.
function usesVlessStream(p: CreateProtocol): boolean {
  return p === 'vless' || p === 'vmess' || p === 'trojan'
}

// ALPN default is protocol-specific: Hysteria2 runs over QUIC and MUST advertise
// h3 (the QUIC ALPN) — and this value is written into the 3X-UI inbound's
// tlsSettings, so a wrong default breaks the server too, not just the rendered
// client. TCP-based TLS (VLESS/VMess/Trojan) uses h2,http/1.1 (h3 can't be
// negotiated over TCP). Used to prefill / retarget the ALPN field on protocol
// change.
const ALPN_HY2 = 'h3'
const ALPN_TLS = 'h2,http/1.1'
function defaultAlpn(p: CreateProtocol): string {
  return p === 'hysteria2' ? ALPN_HY2 : ALPN_TLS
}

const tagFilter = createFilterOptions<string>()

// TagsAutocomplete is the shared tag input used by the create/edit/import/
// separator dialogs. Wraps a freeSolo multi-select Autocomplete around the
// form's `tags_text` comma-joined string so admins pick from existing tags
// (suggestion dropdown) AND introduce new ones. freeSolo alone only adds on
// Enter (no affordance, dead on touch) — filterOptions surfaces the typed
// value as a clickable "add this" row so a new tag can be SELECTED too, which
// is what the dropdown-only bug was missing. Form state stays a comma-joined
// string for backend compatibility — the split/join happens here.
function TagsAutocomplete(props: {
  label: string
  value: string
  options: string[]
  onChange: (next: string) => void
  helperText?: string
}) {
  const tags = props.value
    ? props.value.split(',').map(s => s.trim()).filter(Boolean)
    : []
  return (
    <Autocomplete
      multiple
      freeSolo
      size="small"
      selectOnFocus
      clearOnBlur
      handleHomeEndKeys
      options={props.options}
      value={tags}
      filterOptions={(options, params) => {
        const filtered = tagFilter(options, params)
        const input = params.inputValue.trim()
        // Offer the typed value as a selectable row when it isn't already a
        // known option (case-insensitive) — so a brand-new tag can be clicked
        // to add, not only committed via Enter.
        if (input && !options.some(o => o.toLowerCase() === input.toLowerCase())) {
          filtered.push(input)
        }
        return filtered
      }}
      onChange={(_, v) => {
        // de-dup + drop empties so two passes through this onChange can't
        // create "Premium,Premium" / trailing commas. Sort is NOT applied
        // so user-controlled ordering survives.
        const seen = new Set<string>()
        const cleaned: string[] = []
        for (const raw of v as string[]) {
          const s = raw.trim()
          if (!s || seen.has(s)) continue
          seen.add(s)
          cleaned.push(s)
        }
        props.onChange(cleaned.join(', '))
      }}
      renderValue={(value, getItemProps) =>
        value.map((option, index) => {
          const tagProps = getItemProps({ index })
          return <Chip {...tagProps} key={option} label={option} size="small" />
        })
      }
      renderInput={(params) => (
        <TextField {...params} label={props.label} helperText={props.helperText} />
      )}
    />
  );
}

interface MetaForm {
  display_name: string
  server_address: string
  flow: string
  region: string
  tags_text: string
  sort_order: number
  relays: RelayLine[]
  hide_direct: boolean
}

interface ImportForm extends MetaForm {
  panel_id: number
  panel_name: string
  inbound_id: number
  // Source inbound protocol (lowercased, e.g. "vless"/"shadowsocks").
  // Drives whether the Flow picker is shown — flow is a VLESS-only knob.
  protocol: string
}

interface InboundFormState {
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

const EMPTY_META: MetaForm = {
  display_name: '', server_address: '', flow: '', region: '', tags_text: '', sort_order: 0,
  relays: [], hide_direct: false,
}

const EMPTY_IMPORT: ImportForm = {
  ...EMPTY_META, panel_id: 0, panel_name: '', inbound_id: 0, protocol: '',
}

const EMPTY_INBOUND: InboundFormState = {
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
  // Default new REALITY inbounds to minClientVer 1.0.0 (= no version gate).
  // xray-core >= 26.7.11 treats an EMPTY minClientVer as "26.3.27", which
  // rejects mihomo/Clash Verge (hardcoded client version 1.8.2) and older
  // cores — so an empty default silently breaks every new node. Existing
  // nodes are unaffected: parseInboundForEdit overrides this with the panel's
  // real value. See docs/3xui-compat.md 2026-07-13.
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
  sockopt_enabled: false,
  sockopt_mark: 0,
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

const PROTOCOL_OPTIONS: { value: CreateProtocol; label: string }[] = [
  { value: 'vless', label: 'VLESS' },
  { value: 'vmess', label: 'VMess' },
  { value: 'trojan', label: 'Trojan' },
  { value: 'ss2022', label: 'Shadowsocks 2022' },
  { value: 'hysteria2', label: 'Hysteria 2' },
]
const VLESS_NETWORKS: { value: VlessNetwork; label: string }[] = [
  { value: 'tcp', label: 'TCP' },
  { value: 'ws', label: 'WebSocket' },
  { value: 'grpc', label: 'gRPC' },
  { value: 'httpupgrade', label: 'HTTPUpgrade' },
  { value: 'xhttp', label: 'XHTTP' },
]
const XHTTP_MODES = ['', 'auto', 'packet-up', 'stream-up', 'stream-one']
const TPROXY_MODES: InboundFormState['sockopt_tproxy'][] = ['', 'off', 'redirect', 'tproxy']
const VLESS_SECURITIES: { value: VlessSecurity; label: string }[] = [
  { value: 'none', label: 'None' },
  { value: 'tls', label: 'TLS' },
  { value: 'reality', label: 'Reality' },
]
const FINGERPRINTS = ['chrome', 'firefox', 'safari', 'ios', 'android', 'edge', '360', 'qq', 'random', 'randomized']
const VLESS_FLOWS = ['', 'xtls-rprx-vision', 'xtls-rprx-vision-udp443']

// hostFromURL extracts just the hostname from a 3X-UI panel URL so it can
// pre-fill a node's Address. The panel URL is a management endpoint
// (scheme + admin port + path, e.g. https://1.2.3.4:54321/xyz) — we keep
// only the host since the proxy port comes from the inbound and the path /
// admin port are irrelevant to clients. Best-effort: prepends a scheme when
// the stored URL omits one, and returns "" on anything unparseable so the
// caller just leaves the field blank.
function hostFromURL(raw: string): string {
  if (!raw) return ''
  try {
    const u = new URL(/^[a-z][a-z0-9+.-]*:\/\//i.test(raw) ? raw : `https://${raw}`)
    return u.hostname
  } catch {
    return ''
  }
}
const TCP_HEADER_TYPES = ['none', 'http']
const TLS_VERSIONS = ['', '1.0', '1.1', '1.2', '1.3']
const SS2022_METHODS: { value: SS2022Method; bytes: number }[] = [
  { value: '2022-blake3-aes-128-gcm', bytes: 16 },
  { value: '2022-blake3-aes-256-gcm', bytes: 32 },
  { value: '2022-blake3-chacha20-poly1305', bytes: 32 },
]

function splitList(value: string): string[] {
  return value.split(/[\n,]/).map(s => s.trim()).filter(Boolean);
}

function parseJSONSafe(raw: string | undefined): Record<string, unknown> {
  if (!raw?.trim()) return {}
  try { return JSON.parse(raw) as Record<string, unknown> } catch { return {} }
}

function stringValue(v: unknown, fallback = ''): string {
  return typeof v === 'string' ? v : fallback
}
function boolValue(v: unknown, fallback = false): boolean {
  return typeof v === 'boolean' ? v : fallback
}
function numberValue(v: unknown, fallback = 0): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : fallback
}
function listToText(v: unknown): string {
  return Array.isArray(v) ? v.filter(item => item !== '').join(',') : ''
}

function randomBase64(byteLength: number): string {
  const bytes = new Uint8Array(byteLength)
  crypto.getRandomValues(bytes)
  let binary = ''
  bytes.forEach(b => { binary += String.fromCharCode(b) })
  return btoa(binary)
}

function buildVlessSettings(f: InboundFormState): unknown {
  return { clients: [], decryption: f.vless_encryption || 'none', fallbacks: [] }
}

// VMess inbound settings — 3X-UI's VmessSettings.toJson emits ONLY
// clients[]. disableInsecureEncryption and alterId are not present in
// the model (3X-UI dropped AEAD-only legacy support).
function buildVmessSettings(_f: InboundFormState): unknown {
  return { clients: [] }
}

// Trojan inbound settings: clients[] always, fallbacks only when present
// (3X-UI omits the key when empty; emitting [] is tolerated but breaks
// round-trip equality on edit).
function buildTrojanSettings(_f: InboundFormState): unknown {
  return { clients: [] }
}

// SS-2022 inbound settings — ivCheck is a server toggle to reject
// replayed IVs (defaults false in 3X-UI).
function buildSS2022Settings(f: InboundFormState): unknown {
  return {
    method: f.ss_method,
    password: f.ss_password,
    network: f.ss_network,
    clients: [],
    ivCheck: f.ss_iv_check,
  }
}

// Hysteria 2 inbound settings — 3X-UI shape (see frontend/src/models/
// inbound.js HysteriaSettings.toJson). Only version + clients[] live
// here; per-user password is clients[].auth (panel-managed). Obfs and
// masquerade live in streamSettings, not here. Bandwidth /
// ignoreClientBandwidth from upstream Hysteria 2 server are NOT
// supported by Xray's hysteria2 transport — don't emit them.
function buildHysteria2Settings(_f: InboundFormState): unknown {
  return { version: 2, clients: [] }
}

// settingsBuilderFor picks the protocol-specific inbound settings builder
// so submit handlers stay free of switch statements.
function settingsBuilderFor(p: CreateProtocol): (f: InboundFormState) => unknown {
  switch (p) {
    case 'ss2022': return buildSS2022Settings
    case 'vmess': return buildVmessSettings
    case 'trojan': return buildTrojanSettings
    case 'hysteria2': return buildHysteria2Settings
    default: return buildVlessSettings
  }
}

// xuiProtocolName maps the panel's CreateProtocol enum to the protocol
// string 3X-UI expects in the inbound payload.
function xuiProtocolName(p: CreateProtocol): string {
  switch (p) {
    case 'ss2022': return 'shadowsocks'
    case 'vmess': return 'vmess'
    case 'trojan': return 'trojan'
    case 'hysteria2': return 'hysteria2'
    default: return 'vless'
  }
}

function buildStreamSettings(f: InboundFormState): unknown {
  if (f.protocol === 'ss2022') {
    return {
      network: 'tcp',
      security: 'none',
      externalProxy: [],
      tcpSettings: { acceptProxyProtocol: false, header: { type: 'none' } },
    }
  }
  if (f.protocol === 'hysteria2') {
    // 3X-UI emits hy2 as an Xray transport: network: "hysteria" with
    // hysteriaSettings holding version/auth/udpIdleTimeout/masquerade.
    // Salamander obfs lives under finalmask.udp[] (NOT settings.obfs).
    // TLS reuses tlsSettings; cert/key are admin-uploaded in 3X-UI's
    // own UI or pasted via the advanced JSON view.
    const hysteriaSettings: Record<string, unknown> = {
      // 3X-UI's HysteriaStreamSettings.toJson emits `protocol` ("hysteria2")
      // alongside version/auth — omitting it would let 3X-UI's UI re-add
      // it on next edit, which dirty-saves the inbound. Include for parity.
      protocol: 'hysteria2',
      version: 2,
      auth: '',
      udpIdleTimeout: f.hy2_udp_idle_timeout || 60,
    }
    if (f.hy2_masquerade_type) {
      const m: Record<string, unknown> = { type: f.hy2_masquerade_type }
      switch (f.hy2_masquerade_type) {
        case 'proxy': m.url = f.hy2_masquerade_content; break
        case 'file':  m.dir = f.hy2_masquerade_content; break
        case 'string': m.content = f.hy2_masquerade_content; break
      }
      hysteriaSettings.masquerade = m
    }
    const finalmask: Record<string, unknown> = { tcp: [], udp: [] }
    if (f.hy2_obfs_password) {
      (finalmask.udp as unknown[]).push({
        type: 'salamander',
        settings: { password: f.hy2_obfs_password },
      })
    }
    return {
      network: 'hysteria',
      security: 'tls',
      tlsSettings: buildTLSSettings(f),
      hysteriaSettings,
      finalmask,
      externalProxy: [],
      ...buildSockoptWrapper(f),
    }
  }
  // VMess can't use REALITY (clients don't support it). Trojan REQUIRES
  // TLS (the protocol has no plaintext mode). Clamp accordingly so an
  // admin who switches protocols after configuring VLESS doesn't end up
  // with a broken inbound.
  let security: VlessSecurity = f.vless_security
  if (f.protocol === 'vmess' && security === 'reality') security = 'tls'
  if (f.protocol === 'trojan') security = 'tls'
  const stream: Record<string, unknown> = {
    network: f.vless_network,
    security,
    externalProxy: [],
  }
  if (f.vless_network === 'tcp') {
    stream.tcpSettings = {
      acceptProxyProtocol: f.tcp_accept_proxy_protocol,
      header: { type: f.tcp_header_type },
    }
  } else if (f.vless_network === 'ws') {
    stream.wsSettings = {
      acceptProxyProtocol: f.ws_accept_proxy_protocol,
      path: f.ws_path || '/',
      host: f.ws_host,
      headers: f.ws_host ? { Host: f.ws_host } : {},
      heartbeatPeriod: 0,
    }
  } else if (f.vless_network === 'grpc') {
    stream.grpcSettings = {
      serviceName: f.grpc_service_name,
      authority: f.grpc_authority,
      multiMode: f.grpc_multi_mode,
    }
  } else if (f.vless_network === 'httpupgrade') {
    stream.httpupgradeSettings = {
      acceptProxyProtocol: f.httpupgrade_accept_proxy_protocol,
      path: f.httpupgrade_path || '/',
      host: f.httpupgrade_host,
      headers: f.httpupgrade_host ? { Host: f.httpupgrade_host } : {},
    }
  } else if (f.vless_network === 'xhttp') {
    const xhttp: Record<string, unknown> = {
      path: f.xhttp_path || '/',
      host: f.xhttp_host,
    }
    if (f.xhttp_mode) xhttp.mode = f.xhttp_mode
    stream.xhttpSettings = xhttp
  }
  if (security === 'tls') {
    stream.tlsSettings = buildTLSSettings(f)
  } else if (security === 'reality') {
    // REALITY field names follow 3X-UI's frontend model (target /
    // minClientVer / maxClientVer) so the inbound round-trips cleanly
    // when an admin opens it in 3X-UI's own web UI. xray-core itself
    // accepts both these names and the legacy aliases (dest /
    // minClient / maxClient), so existing inbounds keep working —
    // parseInboundForEdit reads both shapes below.
    stream.realitySettings = {
      show: false,
      xver: f.reality_xver,
      target: f.reality_dest,
      serverNames: splitList(f.reality_server_names_text),
      privateKey: f.private_key,
      minClientVer: f.reality_min_client,
      maxClientVer: f.reality_max_client,
      maxTimediff: f.reality_max_timediff,
      shortIds: splitList(f.short_ids_text),
      // mldsa65Seed is the post-quantum REALITY knob; 3X-UI always
      // emits it (default empty) so we match for round-trip parity.
      mldsa65Seed: '',
      settings: {
        publicKey: f.public_key,
        fingerprint: f.reality_fingerprint,
        serverName: '',
        spiderX: f.reality_spider_x || '/ai',
        // mldsa65Verify is the client-side counterpart; same parity
        // reasoning. Admin doesn't fill these via the form yet.
        mldsa65Verify: '',
      },
    }
  }
  Object.assign(stream, buildSockoptWrapper(f))
  return stream
}

// buildTLSSettings centralises tlsSettings emission. Schema lines up
// with 3X-UI's frontend model (verified against frontend/src/models/
// inbound.js) so the inbound round-trips cleanly through their own UI:
//
//   fingerprint lives at tlsSettings.settings.fingerprint, NOT the top
//     level — 3X-UI's fromJson silently drops top-level fingerprint.
//   certificates[] entries carry oneTimeLoading / usage / buildChain
//     to match 3X-UI's always-emitted defaults.
//
// File-paths cert mode produces {certificateFile, keyFile}; inline
// mode wraps the PEM in a single-element string array. Empty mode
// keeps certificates: [] (REALITY / clients that bring their own root).
//
// allowInsecure is a client-side knob in xray-core; server inbound
// ignores it. Keep the toggle so the form state round-trips, but the
// emitted field has no effect at runtime.
function buildTLSSettings(f: InboundFormState): Record<string, unknown> {
  const certs: Record<string, unknown>[] = []
  const certDefaults = {
    ocspStapling: 3600,
    oneTimeLoading: false,
    usage: 'encipherment',
    buildChain: false,
  }
  if (f.tls_cert_mode === 'file' && (f.tls_cert_file || f.tls_key_file)) {
    certs.push({
      certificateFile: f.tls_cert_file,
      keyFile: f.tls_key_file,
      ...certDefaults,
    })
  } else if (f.tls_cert_mode === 'inline' && (f.tls_cert_pem || f.tls_key_pem)) {
    certs.push({
      certificate: f.tls_cert_pem ? [f.tls_cert_pem] : [],
      key: f.tls_key_pem ? [f.tls_key_pem] : [],
      ...certDefaults,
    })
  }
  const out: Record<string, unknown> = {
    serverName: f.tls_server_name,
    minVersion: f.tls_min_version,
    maxVersion: f.tls_max_version,
    cipherSuites: [],
    alpn: splitList(f.tls_alpn_text),
    certificates: certs,
    rejectUnknownSni: f.tls_reject_unknown_sni,
    disableSystemRoot: false,
    enableSessionResumption: false,
    settings: {
      // 3X-UI reads fingerprint from settings.fingerprint, not the
      // top level — value at the outer object gets dropped silently.
      fingerprint: f.tls_fingerprint,
    },
  }
  if (f.tls_allow_insecure) out.allowInsecure = true
  return out
}

// buildSockoptWrapper returns `{ sockopt: {...} }` when the toggle is
// on, or `{}` otherwise. Returning a wrapper object (vs writing
// directly into stream) lets the Hysteria2 path spread it cleanly.
function buildSockoptWrapper(f: InboundFormState): Record<string, unknown> {
  if (!f.sockopt_enabled) return {}
  const so: Record<string, unknown> = {}
  if (f.sockopt_mark > 0) so.mark = f.sockopt_mark
  if (f.sockopt_tcp_fast_open) so.tcpFastOpen = true
  if (f.sockopt_tcp_keep_alive_interval > 0) so.tcpKeepAliveInterval = f.sockopt_tcp_keep_alive_interval
  if (f.sockopt_tcp_keep_alive_idle > 0) so.tcpKeepAliveIdle = f.sockopt_tcp_keep_alive_idle
  if (f.sockopt_tcp_user_timeout > 0) so.tcpUserTimeout = f.sockopt_tcp_user_timeout
  if (f.sockopt_tproxy) so.tproxy = f.sockopt_tproxy
  if (Object.keys(so).length === 0) return {}
  return { sockopt: so }
}

function buildSniffing(f: InboundFormState): unknown {
  return {
    enabled: f.sniffing_enabled,
    destOverride: splitList(f.sniffing_dest_override_text),
    metadataOnly: f.sniffing_metadata_only,
    routeOnly: f.sniffing_route_only,
  }
}

interface InboundDetail {
  protocol: string
  listen?: string
  port?: number
  enable: boolean
  settings: string
  stream_settings: string
  sniffing: string
}

function parseInboundForEdit(node: Node, ib: InboundDetail): InboundFormState {
  const settings = parseJSONSafe(ib.settings)
  const stream = parseJSONSafe(ib.stream_settings)
  const sniffing = parseJSONSafe(ib.sniffing)
  const tcp = (stream.tcpSettings as Record<string, unknown>) ?? {}
  const tcpHeader = (tcp.header as Record<string, unknown>) ?? {}
  const ws = (stream.wsSettings as Record<string, unknown>) ?? {}
  const wsHeaders = (ws.headers as Record<string, unknown>) ?? {}
  const grpc = (stream.grpcSettings as Record<string, unknown>) ?? {}
  const httpupgrade = (stream.httpupgradeSettings as Record<string, unknown>) ?? {}
  const httpupgradeHeaders = (httpupgrade.headers as Record<string, unknown>) ?? {}
  const tls = (stream.tlsSettings as Record<string, unknown>) ?? {}
  // 3X-UI nests fingerprint under tlsSettings.settings — older Passwall
  // builds emitted it at the top level (broken), so accept either spot
  // and prefer settings.fingerprint.
  const tlsInner = (tls.settings as Record<string, unknown>) ?? {}
  const reality = (stream.realitySettings as Record<string, unknown>) ?? {}
  const realityInner = (reality.settings as Record<string, unknown>) ?? {}
  const xhttp = (stream.xhttpSettings as Record<string, unknown>) ?? {}
  // Pre-extract the first certificate entry — Xray's certificates is an
  // array but the panel UI only edits one. cert.certificate / cert.key
  // are either strings or string[]; collapse to a single multi-line
  // string for the textarea binding. If neither file paths nor inline
  // bodies are present, cert_mode stays '' so the form renders "no
  // certificate" (the previous behavior).
  const tlsCert = ((tls.certificates as Array<Record<string, unknown>>) ?? [])[0] ?? {}
  const certFile = stringValue(tlsCert.certificateFile)
  const keyFile = stringValue(tlsCert.keyFile)
  const certPEMRaw = tlsCert.certificate
  const keyPEMRaw = tlsCert.key
  const joinPEM = (v: unknown): string => {
    if (typeof v === 'string') return v
    if (Array.isArray(v)) return v.map(s => String(s ?? '')).join('\n')
    return ''
  }
  const certPEM = joinPEM(certPEMRaw)
  const keyPEM = joinPEM(keyPEMRaw)
  let certMode: InboundFormState['tls_cert_mode'] = ''
  if (certFile || keyFile) certMode = 'file'
  else if (certPEM || keyPEM) certMode = 'inline'
  // A PSP-managed binding wins over whatever cert is currently inlined in the
  // live inbound: the inline PEM is just the last deployed copy, not something
  // the admin hand-edits. Show "PSP-managed" + the bound cert so editing other
  // fields doesn't accidentally turn the source back into a manual inline cert.
  if (node.cert_source === 'psp_managed') certMode = 'psp_managed'
  const boundCertID = node.cert_source === 'psp_managed' ? (node.cert_id ?? 0) : 0
  const sockopt = (stream.sockopt as Record<string, unknown>) ?? {}
  const tproxyVal = stringValue(sockopt.tproxy) as InboundFormState['sockopt_tproxy']

  // Map 3X-UI's wire-level protocol name back onto our CreateProtocol enum.
  // Shadowsocks splits between legacy SS and SS-2022 based on the method
  // prefix; everything else maps 1:1.
  let protocol: CreateProtocol = 'vless'
  switch (ib.protocol) {
    case 'vmess': protocol = 'vmess'; break
    case 'trojan': protocol = 'trojan'; break
    case 'hysteria2': protocol = 'hysteria2'; break
    case 'shadowsocks':
      protocol = stringValue(settings.method).startsWith('2022-') ? 'ss2022' : 'vless'
      break
    case 'vless':
    default:
      protocol = 'vless'
  }
  // Pre-extract protocol-specific structures so the return assignment
  // below stays readable. Empty-defaults are safe — the structured form
  // only renders the matching section, so unused fields stay at their
  // EMPTY_INBOUND defaults.
  // Hysteria 2: salamander obfs lives in finalmask.udp[]; masquerade +
  // udpIdleTimeout in hysteriaSettings. See 3X-UI inbound.js.
  const hysteriaSettings = (stream.hysteriaSettings as Record<string, unknown>) ?? {}
  const masquerade = (hysteriaSettings.masquerade as Record<string, unknown>) ?? {}
  const finalmask = (stream.finalmask as Record<string, unknown>) ?? {}
  const finalmaskUDP = (finalmask.udp as Array<Record<string, unknown>>) ?? []
  const salamander = finalmaskUDP.find(m => m.type === 'salamander')
  const salamanderSettings = (salamander?.settings as Record<string, unknown>) ?? {}
  const masqueradeType = stringValue(masquerade.type) as InboundFormState['hy2_masquerade_type']
  const masqueradeContent = masqueradeType === 'proxy' ? stringValue(masquerade.url)
    : masqueradeType === 'file' ? stringValue(masquerade.dir)
    : masqueradeType === 'string' ? stringValue(masquerade.content)
    : ''

  return {
    ...EMPTY_INBOUND,
    panel_id: node.panel_id,
    display_name: node.display_name,
    server_address: node.server_address,
    region: node.region,
    tags_text: (node.tags ?? []).join(', '),
    sort_order: node.sort_order,
    listen: ib.listen ?? '',
    port: ib.port ?? 443,
    enable: ib.enable,
    protocol,
    vless_flow: node.flow ?? (stringValue(stream.security) === 'reality' ? 'xtls-rprx-vision' : ''),
    vless_encryption: stringValue(settings.decryption, 'none'),
    vless_network: (stringValue(stream.network, 'tcp') as VlessNetwork),
    vless_security: (stringValue(stream.security, 'none') as VlessSecurity),
    tcp_accept_proxy_protocol: boolValue(tcp.acceptProxyProtocol),
    tcp_header_type: stringValue(tcpHeader.type, 'none'),
    ws_accept_proxy_protocol: boolValue(ws.acceptProxyProtocol),
    ws_path: stringValue(ws.path, '/'),
    ws_host: stringValue(ws.host) || stringValue(wsHeaders.Host),
    grpc_service_name: stringValue(grpc.serviceName),
    grpc_authority: stringValue(grpc.authority),
    grpc_multi_mode: boolValue(grpc.multiMode),
    httpupgrade_path: stringValue(httpupgrade.path, '/'),
    httpupgrade_host: stringValue(httpupgrade.host) || stringValue(httpupgradeHeaders.Host),
    httpupgrade_accept_proxy_protocol: boolValue(httpupgrade.acceptProxyProtocol),
    xhttp_path: stringValue(xhttp.path, '/'),
    xhttp_host: stringValue(xhttp.host),
    xhttp_mode: stringValue(xhttp.mode),
    tls_server_name: stringValue(tls.serverName),
    tls_alpn_text: listToText(tls.alpn) || defaultAlpn(protocol),
    tls_min_version: stringValue(tls.minVersion),
    tls_max_version: stringValue(tls.maxVersion),
    tls_fingerprint: stringValue(tlsInner.fingerprint) || stringValue(tls.fingerprint, 'chrome'),
    tls_allow_insecure: boolValue(tls.allowInsecure),
    tls_reject_unknown_sni: boolValue(tls.rejectUnknownSni),
    tls_cert_mode: certMode,
    tls_cert_file: certFile,
    tls_key_file: keyFile,
    tls_cert_pem: certPEM,
    tls_key_pem: keyPEM,
    cert_id: boundCertID,
    // REALITY: read both 3X-UI's canonical names (target /
    // minClientVer / maxClientVer) AND xray-core's legacy aliases
    // (dest / minClient / maxClient). Pre-rc.10 inbounds shipped the
    // aliases — fall back to them when the canonical key is empty so
    // existing rows don't silently lose their REALITY config on the
    // first edit.
    reality_dest: stringValue(reality.target) || stringValue(reality.dest, 'www.asus.com:443'),
    reality_server_names_text: listToText(reality.serverNames) || 'www.asus.com',
    private_key: stringValue(reality.privateKey),
    public_key: stringValue(realityInner.publicKey),
    short_ids_text: listToText(reality.shortIds),
    reality_fingerprint: stringValue(realityInner.fingerprint, 'chrome'),
    reality_spider_x: stringValue(realityInner.spiderX, '/ai'),
    reality_xver: numberValue(reality.xver),
    reality_max_timediff: numberValue(reality.maxTimediff),
    reality_min_client: stringValue(reality.minClientVer) || stringValue(reality.minClient),
    reality_max_client: stringValue(reality.maxClientVer) || stringValue(reality.maxClient),
    ss_method: (stringValue(settings.method, '2022-blake3-aes-256-gcm') as SS2022Method),
    ss_password: stringValue(settings.password),
    ss_network: stringValue(settings.network, 'tcp,udp'),
    ss_iv_check: boolValue(settings.ivCheck),
    hy2_obfs_password: stringValue(salamanderSettings.password),
    hy2_udp_idle_timeout: numberValue(hysteriaSettings.udpIdleTimeout, 60),
    hy2_masquerade_type: masqueradeType,
    hy2_masquerade_content: masqueradeContent,
    sockopt_enabled: Object.keys(sockopt).length > 0,
    sockopt_mark: numberValue(sockopt.mark),
    sockopt_tcp_fast_open: boolValue(sockopt.tcpFastOpen),
    sockopt_tcp_keep_alive_interval: numberValue(sockopt.tcpKeepAliveInterval),
    sockopt_tcp_keep_alive_idle: numberValue(sockopt.tcpKeepAliveIdle),
    sockopt_tcp_user_timeout: numberValue(sockopt.tcpUserTimeout),
    sockopt_tproxy: ['off', 'redirect', 'tproxy'].includes(tproxyVal) ? tproxyVal : '',
    sniffing_enabled: boolValue(sniffing.enabled, true),
    sniffing_dest_override_text: listToText(sniffing.destOverride) || 'http,tls,quic,fakedns',
    sniffing_metadata_only: boolValue(sniffing.metadataOnly),
    sniffing_route_only: boolValue(sniffing.routeOnly),
    raw_settings: ib.settings,
    raw_stream_settings: ib.stream_settings,
    raw_sniffing: ib.sniffing,
  }
}

function validateInboundForm(f: InboundFormState, t: (k: string) => string): string | null {
  if (!f.display_name || !f.server_address || !f.region) {
    return t('admin:nodes.create_dialog.validate_required')
  }
  if (f.protocol === 'vless') {
    if (f.vless_security === 'reality') {
      if (!f.private_key || !f.public_key || splitList(f.short_ids_text).length === 0) {
        return t('admin:nodes.create_dialog.validate_reality_keys')
      }
      if (!f.reality_dest || splitList(f.reality_server_names_text).length === 0) {
        return t('admin:nodes.create_dialog.validate_reality_target')
      }
    }
  } else if (f.protocol === 'ss2022') {
    if (!f.ss_method || !f.ss_password) {
      return t('admin:nodes.create_dialog.validate_ss2022')
    }
  }
  return null
}

interface FieldsProps {
  form: InboundFormState
  setForm: Dispatch<SetStateAction<InboundFormState>>
  showMetadata: boolean
  servers?: Server[]
  onGenKeys: () => void | Promise<void>
  onGenSSPassword: () => void
  genKeysBusy: boolean
  protocolReadonly?: boolean
  // advanced mode hides the structured per-protocol fields and lets the
  // admin paste raw settings/stream/sniffing JSON instead — the escape
  // hatch for any 3X-UI option not modelled in our structured form.
  advanced?: boolean
  onSetAdvanced?: (v: boolean) => void
  // Existing tags across all nodes, surfaced as the Tags autocomplete's
  // suggestion list (same as the edit/import dialogs).
  allTags?: string[]
  // Edit mode doesn't receive the selectable server list, but the scanner
  // still needs to disclose which node is executing the probe.
  scanSourceName?: string
}

function InboundFormFields({ form, setForm, showMetadata, servers, onGenKeys, onGenSSPassword, genKeysBusy, protocolReadonly, advanced, onSetAdvanced, allTags, scanSourceName }: FieldsProps) {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])

  const update = <K extends keyof InboundFormState>(key: K, value: InboundFormState[K]) => {
    setForm(prev => ({ ...prev, [key]: value }))
  }

  // --- Certificate source helpers (v3.6.4) ---
  // The single "Certificate source" selector (form.tls_cert_mode) drives this:
  //   '' → no cert · 'file'/'inline' → manual (emitted into streamSettings) ·
  //   'psp_managed' → bind a PSP-managed cert (form.cert_id); the backend
  //   deploys + auto-renews it. The actual bind/unbind happens on dialog submit
  //   (both create and edit) so the two flows stay symmetric — see submitCreate
  //   / submitEditInbound. 'from_panel' fills the file-mode paths in place.
  const [managedCerts, setManagedCerts] = useState<Cert[]>([])
  const [fetchingPanelCert, setFetchingPanelCert] = useState(false)
  const [realityScannerOpen, setRealityScannerOpen] = useState(false)
  useEffect(() => {
    listCerts().then(setManagedCerts).catch(() => {})
  }, [])

  async function fetchFromPanel() {
    if (!form.panel_id) {
      pushSnack(t('admin:nodes.cert_source.no_panel'), 'warning')
      return
    }
    setFetchingPanelCert(true)
    try {
      const r = await fetchPanelWebCert(form.panel_id)
      if (!r.supported) {
        pushSnack(t('admin:nodes.cert_source.from_panel_unsupported'), 'warning')
        return
      }
      setForm(prev => ({ ...prev, tls_cert_mode: 'file', tls_cert_file: r.cert_file ?? '', tls_key_file: r.key_file ?? '' }))
      pushSnack(t('admin:nodes.cert_source.from_panel_filled'), 'success')
    } catch {
      /* error toast via the axios interceptor */
    } finally {
      setFetchingPanelCert(false)
    }
  }

  // Toggling into advanced mode snapshots the structured form's current
  // values as JSON into raw_*. Toggling back is intentionally lossy — we
  // do NOT re-parse the JSON into the structured fields, because the
  // textareas may contain transports/options our parser doesn't know
  // about and silently dropping them would surprise the admin. They get
  // a confirm dialog instead.
  function toggleAdvanced(next: boolean) {
    if (next) {
      setForm(prev => ({
        ...prev,
        raw_settings: prev.raw_settings || JSON.stringify(settingsBuilderFor(prev.protocol)(prev), null, 2),
        raw_stream_settings: prev.raw_stream_settings || JSON.stringify(buildStreamSettings(prev), null, 2),
        raw_sniffing: prev.raw_sniffing || JSON.stringify(buildSniffing(prev), null, 2),
      }))
    }
    onSetAdvanced?.(next)
  }

  const sectionTitle = (text: string) => (
    <Typography sx={{
      fontWeight: 500, fontSize: 11, mb: 0.75,
      color: md.primary, textTransform: 'uppercase', letterSpacing: '.6px',
    }}>{text}</Typography>
  )
  const fieldLabel = (text: string) => (
    <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.25 }}>{text}</Typography>
  )
  const switchControl = (label: string, checked: boolean, onChange: (c: boolean) => void) => (
    <FormControlLabel
      label={label}
      control={<Switch size="small" checked={checked} onChange={(_, c) => onChange(c)} />}
      sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1, fontSize: 13 } }}
    />
  )

  const protocolLabel = PROTOCOL_OPTIONS.find(o => o.value === form.protocol)?.label ?? form.protocol

  // TLS certificate source picker, shared by the VLESS-stream TLS section and
  // the Hysteria 2 section (both emit tlsSettings.certificates). ONE selector,
  // four mutually-exclusive sources:
  //   '' (none)        — certificates: [] (REALITY / bring-your-own-root)
  //   'file'           — paths on the 3X-UI host (production w/ acme/certbot);
  //                      "fetch from panel" fills them from the panel's web cert
  //   'inline'         — paste PEM directly (one-off / no ACME on host)
  //   'psp_managed'    — bind a PSP-issued cert; the backend deploys + renews it
  // Folding the managed-cert picker into this selector (rather than a separate
  // always-on row) means the admin can't pick a manual cert AND a managed one
  // at the same time. The bind itself is applied on submit (create + edit).
  const activeCerts = managedCerts.filter(c => c.status === 'active')
  const tlsCertFields = () => (
    <>
      <TextField select size="small"
        label={t('admin:nodes.create_dialog.tls_cert_mode')}
        value={form.tls_cert_mode}
        onChange={e => update('tls_cert_mode', e.target.value as InboundFormState['tls_cert_mode'])}
        sx={{ alignSelf: 'flex-start', minWidth: 260 }}>
        <MenuItem value="">{t('admin:nodes.create_dialog.tls_cert_none')}</MenuItem>
        <MenuItem value="file">{t('admin:nodes.create_dialog.tls_cert_file')}</MenuItem>
        <MenuItem value="inline">{t('admin:nodes.create_dialog.tls_cert_inline')}</MenuItem>
        <MenuItem value="psp_managed">{t('admin:nodes.create_dialog.tls_cert_managed')}</MenuItem>
      </TextField>
      {form.tls_cert_mode === 'file' && (
        <>
          <Box>
            <Button size="small" variant="outlined" disabled={!form.panel_id || fetchingPanelCert} onClick={fetchFromPanel}>
              {fetchingPanelCert ? <CircularProgress size={16} /> : t('admin:nodes.cert_source.from_panel')}
            </Button>
          </Box>
          <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
            <TextField size="small" fullWidth
              label={t('admin:nodes.create_dialog.tls_cert_file_path', { defaultValue: 'Certificate file path' })}
              placeholder="/etc/letsencrypt/live/example.com/fullchain.pem"
              value={form.tls_cert_file}
              onChange={e => update('tls_cert_file', e.target.value)}
              sx={{ flex: '1 1 320px' }} />
            <TextField size="small" fullWidth
              label={t('admin:nodes.create_dialog.tls_key_file_path', { defaultValue: 'Private key file path' })}
              placeholder="/etc/letsencrypt/live/example.com/privkey.pem"
              value={form.tls_key_file}
              onChange={e => update('tls_key_file', e.target.value)}
              sx={{ flex: '1 1 320px' }} />
          </Box>
        </>
      )}
      {form.tls_cert_mode === 'inline' && (
        <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
          <TextField size="small" multiline minRows={4} maxRows={10}
            label={t('admin:nodes.create_dialog.tls_cert_pem', { defaultValue: 'Certificate (PEM)' })}
            placeholder={'-----BEGIN CERTIFICATE-----\n…\n-----END CERTIFICATE-----'}
            value={form.tls_cert_pem}
            onChange={e => update('tls_cert_pem', e.target.value)}
            sx={{ flex: '1 1 320px', '& textarea': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 12 } }} />
          <TextField size="small" multiline minRows={4} maxRows={10}
            label={t('admin:nodes.create_dialog.tls_key_pem', { defaultValue: 'Private key (PEM)' })}
            placeholder={'-----BEGIN PRIVATE KEY-----\n…\n-----END PRIVATE KEY-----'}
            value={form.tls_key_pem}
            onChange={e => update('tls_key_pem', e.target.value)}
            sx={{ flex: '1 1 320px', '& textarea': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 12 } }} />
        </Box>
      )}
      {form.tls_cert_mode === 'psp_managed' && (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.75 }}>
          <TextField select size="small"
            label={t('admin:nodes.create_dialog.tls_cert_managed_pick')}
            value={form.cert_id}
            onChange={e => update('cert_id', Number(e.target.value))}
            sx={{ alignSelf: 'flex-start', minWidth: 260 }}>
            <MenuItem value={0}><em>{t('admin:nodes.cert_source.pick_managed')}</em></MenuItem>
            {activeCerts.map(c => (
              <MenuItem key={c.id} value={c.id}>{c.name}</MenuItem>
            ))}
          </TextField>
          <Typography sx={{ fontSize: 11.5, color: activeCerts.length === 0 ? md.error : md.onSurfaceVariant }}>
            {activeCerts.length === 0
              ? t('admin:nodes.cert_source.no_active_certs')
              : t('admin:nodes.cert_source.managed_help')}
          </Typography>
        </Box>
      )}
    </>
  )

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.75 }}>
      {/* Target + protocol + listening (combined header). The advanced
          toggle lives on the right so it's visible regardless of which
          mode we're in. */}
      <Box>
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 0.75 }}>
          <Typography sx={{
            fontWeight: 500, fontSize: 11,
            color: md.primary, textTransform: 'uppercase', letterSpacing: '.6px',
          }}>{t('admin:nodes.create_dialog.section_inbound')}</Typography>
          {onSetAdvanced && (
            <FormControlLabel
              label={t('admin:nodes.create_dialog.advanced', { defaultValue: '高级 (JSON)' })}
              control={<Switch size="small" checked={!!advanced}
                onChange={(_, c) => toggleAdvanced(c)} />}
              sx={{ ml: 0, mr: 0, '& .MuiFormControlLabel-label': { ml: 1, fontSize: 13 } }}
            />
          )}
        </Box>
        <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
          {servers && (
            <Box sx={{ flex: '2 1 240px', minWidth: 200 }}>
              {fieldLabel(t('admin:nodes.create_dialog.panel'))}
              {/* Autocomplete (not Select) so the operator can type to filter
                  when there are many 3X-UI servers. fieldLabel-on-top with a
                  label-less input keeps it aligned with the Protocol Select
                  beside it. */}
              <Autocomplete
                size="small"
                fullWidth
                disableClearable
                options={servers}
                getOptionLabel={o => o.name}
                isOptionEqualToValue={(a, b) => a.id === b.id}
                value={servers.find(s => s.id === form.panel_id) ?? servers[0]}
                onChange={(_, v) => {
                  if (!v) return
                  // Switch panel and re-derive the Address default — but only
                  // when the field is still untouched (empty, or equal to the
                  // previously-selected server's host). A manual edit wins.
                  setForm(prev => {
                    const prevHost = hostFromURL(servers.find(s => s.id === prev.panel_id)?.url ?? '')
                    const next = { ...prev, panel_id: v.id }
                    if (!prev.server_address || prev.server_address === prevHost) {
                      next.server_address = hostFromURL(v.url)
                    }
                    return next
                  })
                }}
                renderInput={(params) => (
                  <TextField {...params} placeholder={t('admin:nodes.create_dialog.panel_search_placeholder', { defaultValue: '搜索 / 选择服务器' })} />
                )}
              />
            </Box>
          )}
          <Box sx={{ flex: '1 1 160px', minWidth: 140 }}>
            {fieldLabel(t('admin:nodes.create_dialog.protocol'))}
            <Select size="small" fullWidth value={form.protocol}
              disabled={protocolReadonly || !!advanced}
              onChange={e => {
                const p = e.target.value as CreateProtocol
                // Normalise security so the per-protocol sections render
                // correctly. The default form security is 'reality', which
                // is VLESS-only — without this, switching to Trojan (must be
                // TLS) or VMess (no REALITY) would leave security='reality'
                // and hide BOTH the TLS and Reality sections, so the inbound
                // gets no security config at all.
                setForm(prev => {
                  let sec = prev.vless_security
                  if (p === 'trojan') sec = 'tls'
                  else if (p === 'vmess' && sec === 'reality') sec = 'tls'
                  // Retarget the ALPN default for the new protocol (Hysteria2 →
                  // h3, TCP-TLS → h2,http/1.1) — but only when the field still
                  // holds a default value, so a custom ALPN the admin typed is
                  // preserved.
                  let alpn = prev.tls_alpn_text
                  if (alpn === '' || alpn === ALPN_HY2 || alpn === ALPN_TLS) {
                    alpn = defaultAlpn(p)
                  }
                  return { ...prev, protocol: p, vless_security: sec, tls_alpn_text: alpn }
                })
              }}>
              {PROTOCOL_OPTIONS.map(o => <MenuItem key={o.value} value={o.value}>{o.label}</MenuItem>)}
            </Select>
          </Box>
        </Box>
        <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', mt: 1.5, alignItems: 'flex-end' }}>
          <TextField size="small" label={t('admin:nodes.create_dialog.listen')}
            value={form.listen}
            onChange={e => update('listen', e.target.value)}
            sx={{ flex: '2 1 240px' }} />
          <TextField size="small" type="number" label={t('admin:nodes.create_dialog.port')}
            value={form.port}
            onChange={e => update('port', Number(e.target.value))}
            sx={{ width: 110 }} />
          <Box sx={{ alignSelf: 'center' }}>
            {switchControl(t('admin:nodes.create_dialog.enable'), form.enable, c => update('enable', c))}
          </Box>
        </Box>
      </Box>

      {/* Advanced JSON view — three raw editors. Replaces the structured
          per-protocol form. Submit handlers detect advanced mode and
          send these strings directly to 3X-UI instead of building from
          form fields. */}
      {advanced && (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
          {sectionTitle(t('admin:nodes.create_dialog.section_advanced', { defaultValue: '高级 - 原始 JSON' }))}
          <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
            {t('admin:nodes.create_dialog.advanced_hint', {
              defaultValue: '直接编辑 3X-UI 的 settings / streamSettings / sniffing JSON。可粘贴任何 3X-UI 支持的字段（mKCP、h2、splithttp、httpupgrade 等），保存时原样下发。关闭高级模式不会回填到上方表单，请谨慎切换。',
            })}
          </Typography>
          <TextField fullWidth multiline minRows={6} maxRows={16}
            label="settings"
            value={form.raw_settings ?? ''}
            onChange={e => update('raw_settings', e.target.value)}
            sx={{ '& textarea': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 12 } }} />
          <TextField fullWidth multiline minRows={6} maxRows={16}
            label="streamSettings"
            value={form.raw_stream_settings ?? ''}
            onChange={e => update('raw_stream_settings', e.target.value)}
            sx={{ '& textarea': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 12 } }} />
          <TextField fullWidth multiline minRows={4} maxRows={12}
            label="sniffing"
            value={form.raw_sniffing ?? ''}
            onChange={e => update('raw_sniffing', e.target.value)}
            sx={{ '& textarea': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 12 } }} />
        </Box>
      )}

      {/* Shared VLESS-style stream settings (VLESS, VMess, Trojan all ride
          on the same Xray transport layer — only the inner protocol
          settings JSON differs). Trojan auto-forces TLS; VMess hides the
          REALITY option; flow control + REALITY keys are VLESS-only. */}
      {!advanced && usesVlessStream(form.protocol) && (
        <>
          <Box>
            {/* Shared transport section — header reflects the actual protocol
                (VLESS / VMess / Trojan) instead of hardcoding "VLESS". */}
            {sectionTitle(protocolLabel)}
            <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
              <Box sx={{ flex: '1 1 180px', minWidth: 140 }}>
                {fieldLabel(t('admin:nodes.create_dialog.vless_network'))}
                <Select size="small" fullWidth value={form.vless_network}
                  onChange={e => update('vless_network', e.target.value as VlessNetwork)}>
                  {VLESS_NETWORKS.map(o => <MenuItem key={o.value} value={o.value}>{o.label}</MenuItem>)}
                </Select>
              </Box>
              <Box sx={{ flex: '1 1 180px', minWidth: 140 }}>
                {fieldLabel(t('admin:nodes.create_dialog.vless_security'))}
                <Select size="small" fullWidth
                  // Trojan must run over TLS; lock the selector for that
                  // case so the admin can't pick a config that won't start.
                  value={form.protocol === 'trojan' ? 'tls' : form.vless_security}
                  disabled={form.protocol === 'trojan'}
                  onChange={e => {
                    const next = e.target.value as VlessSecurity
                    setForm(prev => {
                      let flow = prev.vless_flow
                      if (next === 'reality' && !flow) flow = 'xtls-rprx-vision'
                      else if (next !== 'reality' && flow === 'xtls-rprx-vision') flow = ''
                      return { ...prev, vless_security: next, vless_flow: flow }
                    })
                  }}>
                  {VLESS_SECURITIES
                    // REALITY is VLESS-only in practice — VMess clients never
                    // shipped support, and Trojan is forced to TLS. Show it
                    // for VLESS alone so the dropdown can't offer an invalid
                    // protocol/security combination.
                    .filter(o => !(o.value === 'reality' && form.protocol !== 'vless'))
                    .map(o => <MenuItem key={o.value} value={o.value}>{o.label}</MenuItem>)}
                </Select>
              </Box>
              {form.protocol === 'vless' && (
                <Box sx={{ flex: '1 1 180px', minWidth: 140 }}>
                  {fieldLabel(t('admin:nodes.create_dialog.vless_flow'))}
                  <Select size="small" fullWidth value={form.vless_flow}
                    onChange={e => update('vless_flow', e.target.value)} displayEmpty>
                    {VLESS_FLOWS.map(f => <MenuItem key={f} value={f}>{f || '—'}</MenuItem>)}
                  </Select>
                </Box>
              )}
            </Box>
          </Box>

          {/* Network-specific transports */}
          {form.vless_network === 'tcp' && (
            <Box>
              {sectionTitle(t('admin:nodes.create_dialog.section_tcp'))}
              <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'flex-end' }}>
                <Box sx={{ flex: '1 1 220px' }}>
                  {fieldLabel(t('admin:nodes.create_dialog.tcp_header_type'))}
                  <Select size="small" fullWidth value={form.tcp_header_type}
                    onChange={e => update('tcp_header_type', e.target.value)}>
                    {TCP_HEADER_TYPES.map(h => <MenuItem key={h} value={h}>{h}</MenuItem>)}
                  </Select>
                </Box>
                <Box sx={{ pb: 0.5 }}>
                  {switchControl(t('admin:nodes.create_dialog.accept_proxy_protocol'),
                    form.tcp_accept_proxy_protocol,
                    c => update('tcp_accept_proxy_protocol', c))}
                </Box>
              </Box>
            </Box>
          )}

          {form.vless_network === 'ws' && (
            <Box>
              {sectionTitle(t('admin:nodes.create_dialog.section_ws'))}
              <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'center' }}>
                <TextField size="small" label={t('admin:nodes.create_dialog.ws_path')}
                  value={form.ws_path}
                  onChange={e => update('ws_path', e.target.value)}
                  sx={{ flex: '1 1 200px' }} />
                <TextField size="small" label={t('admin:nodes.create_dialog.ws_host')}
                  value={form.ws_host}
                  onChange={e => update('ws_host', e.target.value)}
                  sx={{ flex: '1 1 200px' }} />
                {switchControl(t('admin:nodes.create_dialog.accept_proxy_protocol'),
                  form.ws_accept_proxy_protocol,
                  c => update('ws_accept_proxy_protocol', c))}
              </Box>
            </Box>
          )}

          {form.vless_network === 'grpc' && (
            <Box>
              {sectionTitle(t('admin:nodes.create_dialog.section_grpc'))}
              <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'center' }}>
                <TextField size="small" label={t('admin:nodes.create_dialog.grpc_service_name')}
                  value={form.grpc_service_name}
                  onChange={e => update('grpc_service_name', e.target.value)}
                  sx={{ flex: '1 1 200px' }} />
                <TextField size="small" label={t('admin:nodes.create_dialog.grpc_authority')}
                  value={form.grpc_authority}
                  onChange={e => update('grpc_authority', e.target.value)}
                  sx={{ flex: '1 1 200px' }} />
                {switchControl(t('admin:nodes.create_dialog.grpc_multi_mode'),
                  form.grpc_multi_mode,
                  c => update('grpc_multi_mode', c))}
              </Box>
            </Box>
          )}

          {form.vless_network === 'httpupgrade' && (
            <Box>
              {sectionTitle(t('admin:nodes.create_dialog.section_httpupgrade', { defaultValue: 'HTTPUpgrade' }))}
              <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'center' }}>
                <TextField size="small" label={t('admin:nodes.create_dialog.ws_path', { defaultValue: 'Path' })}
                  value={form.httpupgrade_path}
                  onChange={e => update('httpupgrade_path', e.target.value)}
                  sx={{ flex: '1 1 200px' }} />
                <TextField size="small" label={t('admin:nodes.create_dialog.ws_host', { defaultValue: 'Host' })}
                  value={form.httpupgrade_host}
                  onChange={e => update('httpupgrade_host', e.target.value)}
                  sx={{ flex: '1 1 200px' }} />
                {switchControl(t('admin:nodes.create_dialog.accept_proxy_protocol'),
                  form.httpupgrade_accept_proxy_protocol,
                  c => update('httpupgrade_accept_proxy_protocol', c))}
              </Box>
            </Box>
          )}

          {form.vless_network === 'xhttp' && (
            <Box>
              {sectionTitle(t('admin:nodes.create_dialog.section_xhttp', { defaultValue: 'XHTTP' }))}
              <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'flex-start' }}>
                <TextField size="small" label={t('admin:nodes.create_dialog.ws_path', { defaultValue: 'Path' })}
                  value={form.xhttp_path}
                  onChange={e => update('xhttp_path', e.target.value)}
                  sx={{ flex: '1 1 180px' }} />
                <TextField size="small" label={t('admin:nodes.create_dialog.ws_host', { defaultValue: 'Host' })}
                  value={form.xhttp_host}
                  onChange={e => update('xhttp_host', e.target.value)}
                  sx={{ flex: '1 1 180px' }} />
                <TextField select size="small"
                  label={t('admin:nodes.create_dialog.xhttp_mode')}
                  value={form.xhttp_mode}
                  onChange={e => update('xhttp_mode', e.target.value)}
                  sx={{ flex: '1 1 150px', minWidth: 130 }}>
                  {XHTTP_MODES.map(m => <MenuItem key={m} value={m}>{m || '—'}</MenuItem>)}
                </TextField>
              </Box>
            </Box>
          )}

          {/* Security-specific */}
          {form.vless_security === 'tls' && (
            <Box>
              {sectionTitle(t('admin:nodes.create_dialog.section_tls'))}
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.25 }}>
                <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'flex-start' }}>
                  <TextField size="small" label={t('admin:nodes.create_dialog.tls_server_name')}
                    value={form.tls_server_name}
                    onChange={e => update('tls_server_name', e.target.value)}
                    sx={{ flex: '2 1 240px' }} />
                  <TextField size="small" label={t('admin:nodes.create_dialog.tls_alpn')}
                    value={form.tls_alpn_text}
                    onChange={e => update('tls_alpn_text', e.target.value)}
                    sx={{ flex: '2 1 200px' }} />
                  <TextField select size="small"
                    label={t('admin:nodes.create_dialog.tls_fingerprint')}
                    value={form.tls_fingerprint}
                    onChange={e => update('tls_fingerprint', e.target.value)}
                    sx={{ flex: '1 1 140px', minWidth: 130 }}>
                    <MenuItem value="">—</MenuItem>
                    {FINGERPRINTS.map(fp => <MenuItem key={fp} value={fp}>{fp}</MenuItem>)}
                  </TextField>
                  <TextField select size="small"
                    label={t('admin:nodes.create_dialog.tls_min_version')}
                    value={form.tls_min_version}
                    onChange={e => update('tls_min_version', e.target.value)}
                    sx={{ flex: '1 1 120px', minWidth: 110 }}>
                    {TLS_VERSIONS.map(v => <MenuItem key={v} value={v}>{v || '—'}</MenuItem>)}
                  </TextField>
                  <TextField select size="small"
                    label={t('admin:nodes.create_dialog.tls_max_version')}
                    value={form.tls_max_version}
                    onChange={e => update('tls_max_version', e.target.value)}
                    sx={{ flex: '1 1 120px', minWidth: 110 }}>
                    {TLS_VERSIONS.map(v => <MenuItem key={v} value={v}>{v || '—'}</MenuItem>)}
                  </TextField>
                </Box>
                <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                  {switchControl(t('admin:nodes.create_dialog.tls_allow_insecure', { defaultValue: 'Allow insecure (dev only)' }),
                    form.tls_allow_insecure,
                    c => update('tls_allow_insecure', c))}
                  {switchControl(t('admin:nodes.create_dialog.tls_reject_unknown_sni', { defaultValue: 'Reject unknown SNI' }),
                    form.tls_reject_unknown_sni,
                    c => update('tls_reject_unknown_sni', c))}
                </Box>
                {tlsCertFields()}
              </Box>
            </Box>
          )}

          {form.protocol === 'vless' && form.vless_security === 'reality' && (
            <Box>
              <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1, mb: 1 }}>
                <Typography sx={{
                  fontWeight: 500, fontSize: 11,
                  color: md.primary, textTransform: 'uppercase', letterSpacing: '.6px',
                }}>{t('admin:nodes.create_dialog.section_reality')}</Typography>
                <Button size="small" variant="outlined" onClick={() => onGenKeys()} disabled={genKeysBusy}
                  startIcon={genKeysBusy ? <CircularProgress size={14} /> : <KeyIcon />}>
                  {t('admin:nodes.create_dialog.gen_keys')}
                </Button>
              </Box>
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.25 }}>
                <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'flex-start' }}>
                  <Box sx={{ display: 'flex', gap: 1, flex: '1 1 320px' }}>
                    <TextField required size="small" fullWidth label={t('admin:nodes.create_dialog.reality_dest')}
                      value={form.reality_dest}
                      onChange={e => update('reality_dest', e.target.value)} />
                    <Button type="button" size="small" variant="outlined"
                      disabled={!form.panel_id}
                      onClick={() => setRealityScannerOpen(true)}
                      sx={{ whiteSpace: 'nowrap', minWidth: 104 }}>
                      {t('admin:nodes.create_dialog.reality_scan_open')}
                    </Button>
                  </Box>
                  <TextField required size="small" label={t('admin:nodes.create_dialog.reality_server_names')}
                    value={form.reality_server_names_text}
                    onChange={e => update('reality_server_names_text', e.target.value)}
                    sx={{ flex: '1 1 240px' }} />
                </Box>
                <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
                  <TextField select size="small" label={t('admin:nodes.create_dialog.reality_fingerprint')}
                    value={form.reality_fingerprint}
                    onChange={e => update('reality_fingerprint', e.target.value)}
                    sx={{ flex: '1 1 180px', minWidth: 140 }}>
                    {FINGERPRINTS.map(fp => <MenuItem key={fp} value={fp}>{fp}</MenuItem>)}
                  </TextField>
                  <TextField size="small" label={t('admin:nodes.create_dialog.reality_spider_x')}
                    value={form.reality_spider_x}
                    onChange={e => update('reality_spider_x', e.target.value)}
                    sx={{ flex: '1 1 180px' }} />
                </Box>
                <TextField required size="small" fullWidth label={t('admin:nodes.create_dialog.private_key')}
                  value={form.private_key}
                  onChange={e => update('private_key', e.target.value)}
                  sx={{ '& input': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 13, py: 1.25 } }} />
                <TextField required size="small" fullWidth label={t('admin:nodes.create_dialog.public_key')}
                  value={form.public_key}
                  onChange={e => update('public_key', e.target.value)}
                  sx={{ '& input': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 13, py: 1.25 } }} />
                <TextField required size="small" fullWidth label={t('admin:nodes.create_dialog.short_ids')}
                  value={form.short_ids_text}
                  onChange={e => update('short_ids_text', e.target.value)}
                  sx={{ '& input': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 13, py: 1.25 } }} />
                {/* minClientVer / maxClientVer gate the REALITY handshake by the
                    client's self-reported xray-core version. Both empty = no gate
                    (pre-3.5 behavior). IMPORTANT: xray-core >= 26.7.11 changed an
                    EMPTY minClientVer to default to "26.3.27" server-side, which
                    rejects mihomo/Clash-Verge (they hardcode client version 1.8.2)
                    and any older core — set minClientVer to "1.0.0" to restore the
                    open behavior. See docs/3xui-compat.md 2026-07-13. */}
                <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
                  <TextField size="small" label={t('admin:nodes.create_dialog.reality_min_client')}
                    placeholder="1.0.0"
                    value={form.reality_min_client}
                    onChange={e => update('reality_min_client', e.target.value)}
                    helperText={t('admin:nodes.create_dialog.reality_min_client_hint')}
                    sx={{ flex: '1 1 180px', minWidth: 140 }} />
                  <TextField size="small" label={t('admin:nodes.create_dialog.reality_max_client')}
                    value={form.reality_max_client}
                    onChange={e => update('reality_max_client', e.target.value)}
                    sx={{ flex: '1 1 180px', minWidth: 140 }} />
                </Box>
              </Box>
            </Box>
          )}
        </>
      )}

      {!advanced && form.protocol === 'hysteria2' && (
        <Box>
          {sectionTitle(t('admin:nodes.create_dialog.section_hysteria2', { defaultValue: 'Hysteria 2' }))}
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.25 }}>
            <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
              <TextField size="small" label={t('admin:nodes.create_dialog.tls_server_name', { defaultValue: 'SNI' })}
                value={form.tls_server_name}
                onChange={e => update('tls_server_name', e.target.value)}
                sx={{ flex: '2 1 240px' }} />
              <TextField size="small" label={t('admin:nodes.create_dialog.tls_alpn', { defaultValue: 'ALPN' })}
                value={form.tls_alpn_text}
                onChange={e => update('tls_alpn_text', e.target.value)}
                placeholder="h3"
                sx={{ flex: '1 1 160px' }} />
              <TextField size="small" type="number"
                label={t('admin:nodes.create_dialog.hy2_udp_idle_timeout', { defaultValue: 'UDP 空闲超时 (秒)' })}
                value={form.hy2_udp_idle_timeout}
                onChange={e => update('hy2_udp_idle_timeout', Number(e.target.value) || 60)}
                sx={{ width: 160 }} />
            </Box>
            <TextField size="small" fullWidth
              label={t('admin:nodes.create_dialog.hy2_obfs_password', { defaultValue: '混淆 (salamander) 密码 — 留空 = 不启用' })}
              value={form.hy2_obfs_password}
              onChange={e => update('hy2_obfs_password', e.target.value)}
              helperText={t('admin:nodes.create_dialog.hy2_obfs_hint', {
                defaultValue: '3X-UI 把 obfs 存在 streamSettings.finalmask.udp[salamander].settings.password',
              })} />
            <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'flex-start' }}>
              <Box sx={{ flex: '1 1 200px', minWidth: 160 }}>
                {fieldLabel(t('admin:nodes.create_dialog.hy2_masquerade_type', { defaultValue: '伪装 (Masquerade)' }))}
                <Select size="small" fullWidth value={form.hy2_masquerade_type} displayEmpty
                  onChange={e => update('hy2_masquerade_type', e.target.value as InboundFormState['hy2_masquerade_type'])}>
                  <MenuItem value="">{t('admin:nodes.create_dialog.hy2_masquerade_none', { defaultValue: '不启用' })}</MenuItem>
                  <MenuItem value="proxy">{t('admin:nodes.create_dialog.hy2_masquerade_proxy', { defaultValue: 'proxy（反代到 URL）' })}</MenuItem>
                  <MenuItem value="file">{t('admin:nodes.create_dialog.hy2_masquerade_file', { defaultValue: 'file（返回静态目录）' })}</MenuItem>
                  <MenuItem value="string">{t('admin:nodes.create_dialog.hy2_masquerade_string', { defaultValue: 'string（返回固定内容）' })}</MenuItem>
                </Select>
              </Box>
              <Box sx={{ flex: '2 1 280px' }}>
                {fieldLabel(
                  form.hy2_masquerade_type === 'proxy' ? t('admin:nodes.create_dialog.hy2_masquerade_content_proxy', { defaultValue: 'Upstream URL' })
                  : form.hy2_masquerade_type === 'file' ? t('admin:nodes.create_dialog.hy2_masquerade_content_file', { defaultValue: 'Directory' })
                  : form.hy2_masquerade_type === 'string' ? t('admin:nodes.create_dialog.hy2_masquerade_content_string', { defaultValue: 'Response body' })
                  : t('admin:nodes.create_dialog.hy2_masquerade_content', { defaultValue: '内容 / URL / 目录' })
                )}
                <TextField size="small" fullWidth
                  value={form.hy2_masquerade_content}
                  onChange={e => update('hy2_masquerade_content', e.target.value)}
                  disabled={!form.hy2_masquerade_type} />
              </Box>
            </Box>
            {/* TLS certificate — Hysteria 2 emits tlsSettings.certificates the
                same way the VLESS stream does (see buildStreamSettings), so the
                shared cert picker wires up directly instead of punting to the
                3X-UI side. */}
            {tlsCertFields()}
          </Box>
        </Box>
      )}

      {!advanced && form.protocol === 'ss2022' && (
        <Box>
          {sectionTitle(t('admin:nodes.create_dialog.section_ss2022'))}
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.25 }}>
            <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
              <TextField select size="small" label={t('admin:nodes.create_dialog.ss_method')}
                value={form.ss_method}
                onChange={e => update('ss_method', e.target.value as SS2022Method)}
                sx={{ flex: '2 1 280px' }}>
                {SS2022_METHODS.map(m => <MenuItem key={m.value} value={m.value}>{m.value}</MenuItem>)}
              </TextField>
              <TextField size="small" label={t('admin:nodes.create_dialog.ss_network')}
                value={form.ss_network}
                onChange={e => update('ss_network', e.target.value)}
                sx={{ flex: '1 1 140px' }} />
            </Box>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
              <TextField required size="small" fullWidth label={t('admin:nodes.create_dialog.ss_password')}
                value={form.ss_password}
                onChange={e => update('ss_password', e.target.value)}
                sx={{ '& input': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 13, py: 1.25 } }} />
              <Button size="small" variant="outlined" onClick={onGenSSPassword} startIcon={<KeyIcon />}
                sx={{ whiteSpace: 'nowrap' }}>
                {t('admin:nodes.create_dialog.gen_ss_password')}
              </Button>
            </Box>
            {switchControl(
              t('admin:nodes.create_dialog.ss_iv_check', { defaultValue: 'ivCheck (拒绝重放的 IV)' }),
              form.ss_iv_check,
              c => update('ss_iv_check', c),
            )}
          </Box>
        </Box>
      )}

      {/* Socket-level tuning. Hidden behind the toggle so the form
          stays compact for the common case; admins running a transparent
          proxy / iptables-tagged egress flip it on. tcpFastOpen,
          tcpKeepAlive, tproxy ("tproxy" / "redirect" Linux modes) are
          the realistic knobs — sockopt has more (mark, domainStrategy
          on dial side) but those don't apply to inbound. */}
      {!advanced && (
        <Box>
          {sectionTitle(t('admin:nodes.create_dialog.section_sockopt'))}
          <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'center' }}>
            {switchControl(t('admin:nodes.create_dialog.sockopt_enabled'),
              form.sockopt_enabled,
              c => update('sockopt_enabled', c))}
            {form.sockopt_enabled && (
              <>
                <TextField size="small" type="number"
                  label={t('admin:nodes.create_dialog.sockopt_mark')}
                  value={form.sockopt_mark || ''}
                  onChange={e => update('sockopt_mark', Number(e.target.value) || 0)}
                  sx={{ width: 130 }} />
                {switchControl(t('admin:nodes.create_dialog.sockopt_tcp_fast_open'),
                  form.sockopt_tcp_fast_open,
                  c => update('sockopt_tcp_fast_open', c))}
                <TextField size="small" type="number"
                  label={t('admin:nodes.create_dialog.sockopt_tcp_keep_alive_interval')}
                  value={form.sockopt_tcp_keep_alive_interval || ''}
                  onChange={e => update('sockopt_tcp_keep_alive_interval', Number(e.target.value) || 0)}
                  sx={{ width: 200 }} />
                <TextField size="small" type="number"
                  label={t('admin:nodes.create_dialog.sockopt_tcp_keep_alive_idle')}
                  value={form.sockopt_tcp_keep_alive_idle || ''}
                  onChange={e => update('sockopt_tcp_keep_alive_idle', Number(e.target.value) || 0)}
                  sx={{ width: 190 }} />
                <TextField size="small" type="number"
                  label={t('admin:nodes.create_dialog.sockopt_tcp_user_timeout')}
                  value={form.sockopt_tcp_user_timeout || ''}
                  onChange={e => update('sockopt_tcp_user_timeout', Number(e.target.value) || 0)}
                  sx={{ width: 200 }} />
                <TextField select size="small"
                  label={t('admin:nodes.create_dialog.sockopt_tproxy')}
                  value={form.sockopt_tproxy}
                  onChange={e => update('sockopt_tproxy', e.target.value as InboundFormState['sockopt_tproxy'])}
                  sx={{ width: 160 }}>
                  {TPROXY_MODES.map(m => <MenuItem key={m} value={m}>{m || '—'}</MenuItem>)}
                </TextField>
              </>
            )}
          </Box>
        </Box>
      )}

      {/* Sniffing — compact single row. Hidden in advanced mode because
          the raw sniffing JSON textarea covers it. */}
      {!advanced && (
        <Box>
          {sectionTitle(t('admin:nodes.create_dialog.section_sniffing'))}
          <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'center' }}>
            {switchControl(t('admin:nodes.create_dialog.sniffing_enabled'),
              form.sniffing_enabled,
              c => update('sniffing_enabled', c))}
            <TextField size="small" label={t('admin:nodes.create_dialog.sniffing_dest_override')}
              value={form.sniffing_dest_override_text}
              onChange={e => update('sniffing_dest_override_text', e.target.value)}
              sx={{ flex: '1 1 240px' }} />
            {switchControl(t('admin:nodes.create_dialog.sniffing_metadata_only'),
              form.sniffing_metadata_only,
              c => update('sniffing_metadata_only', c))}
            {switchControl(t('admin:nodes.create_dialog.sniffing_route_only'),
              form.sniffing_route_only,
              c => update('sniffing_route_only', c))}
          </Box>
        </Box>
      )}

      {/* Metadata (create only) */}
      {showMetadata && (
        <Box>
          {sectionTitle(t('admin:nodes.create_dialog.section_metadata'))}
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.25 }}>
            <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
              <TextField required size="small" label={t('admin:nodes.field.display_name')}
                value={form.display_name}
                onChange={e => update('display_name', e.target.value)}
                sx={{ flex: '1 1 240px' }} />
              <TextField required size="small" label={t('admin:nodes.field.server_address')}
                value={form.server_address}
                onChange={e => update('server_address', e.target.value)}
                sx={{ flex: '1 1 240px' }} />
            </Box>
            <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
              <TextField required size="small" label={t('admin:nodes.field.region')}
                value={form.region}
                onChange={e => update('region', e.target.value)}
                sx={{ flex: '1 1 120px' }} />
              <Box sx={{ flex: '2 1 240px' }}>
                <TagsAutocomplete
                  label={t('admin:nodes.field.tags')}
                  value={form.tags_text}
                  options={allTags ?? []}
                  onChange={v => update('tags_text', v)} />
              </Box>
            </Box>
          </Box>
        </Box>
      )}
      <RealityTargetScannerDialog
        open={realityScannerOpen}
        panelId={form.panel_id}
        sourceName={servers?.find(s => s.id === form.panel_id)?.name || scanSourceName}
        onClose={() => setRealityScannerOpen(false)}
        onUse={result => {
          setForm(prev => ({
            ...prev,
            reality_dest: result.target,
            reality_server_names_text: result.serverNames.length > 0
              ? result.serverNames.join(', ')
              : prev.reality_server_names_text,
          }))
        }}
      />
    </Box>
  )
}

// SearchOption tags a free-text Autocomplete suggestion with the field it
// came from so MUI's `groupBy` renders headers like "Region" / "Tags" above
// the values from that field. Without the tag the suggestion menu mixes all
// fields into one alphabetical blob and the operator can't tell whether
// "Premium" is a tag, a region, or somebody's display name.
type SearchOption = { label: string; group: string }

// bucketsToOptions flattens a list of (group-name, value-set) pairs into the
// SearchOption[] shape Autocomplete consumes. Values are sorted within each
// group; groups stay in caller-defined order (which is the visual order MUI
// renders them when `groupBy` is used).
function bucketsToOptions(buckets: Array<readonly [string, Set<string>]>): SearchOption[] {
  const out: SearchOption[] = []
  for (const [group, set] of buckets) {
    for (const label of [...set].sort()) out.push({ label, group })
  }
  return out
}

// reorderRows produces the new list order by moving `fromIdx` to `toIdx`
// (insertion semantics: the dragged row lands at position `toIdx` in the
// resulting array). Pure so the same logic can later be lifted into a unit
// test once the frontend has a test runner.
function reorderRows<T>(rows: readonly T[], fromIdx: number, toIdx: number): T[] {
  if (fromIdx === toIdx || fromIdx < 0 || toIdx < 0 || fromIdx >= rows.length || toIdx >= rows.length) {
    return rows.slice()
  }
  const next = rows.slice()
  const [moved] = next.splice(fromIdx, 1)
  next.splice(toIdx, 0, moved)
  return next
}

// commitReorder inlines the (i + 1) * 10 sort_order assignment now that
// it has to split the result into node / separator payloads anyway —
// no shared helper left to extract.

export default function NodesView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])
  const canConfig = useCan('config.write')
  // Admin view: render times in the panel timezone (with the viewer's browser
  // tz disclosed when they differ), consistent with the other admin pages.
  const panelTz = useSiteStore(s => s.timezone)

  const [tab, setTab] = useTabParam<'managed' | 'unmanaged'>('tab', 'managed', ['managed', 'unmanaged'])
  const [managed, setManaged] = useState<Node[]>([])
  // Separators live in nodes_separator (since v3.0.0-beta.7) and load
  // through a dedicated endpoint. They render interleaved with real nodes
  // in the managed table by SortOrder, matching what the subscription
  // render does — admins see the same list order they'd see in a
  // generated config.
  const [separators, setSeparators] = useState<Separator[]>([])
  // Groups feed the multi-select inside the separator dialog when an
  // Groups list was previously loaded for the separator dialog's
  // "show in groups" picker. rc.4 switched separator visibility from
  // group_ids to node_ids, so the dialog picks from `managed` instead
  // and the groups state itself is no longer read.
  const [unmanaged, setUnmanaged] = useState<UnmanagedInbound[]>([])
  // The unmanaged tab is scoped to one server: nothing is fetched until the
  // admin picks a panel. null = no selection yet (shows the empty prompt).
  const [unmanagedPanelId, setUnmanagedPanelId] = useState<number | null>(null)
  // Last error from loading the selected panel's inbounds (e.g. panel
  // unreachable) — surfaced inline with a Retry instead of failing silently.
  const [unmanagedError, setUnmanagedError] = useState<string | null>(null)
  // Free-text filter on the unmanaged-inbound tab. Matches against panel
  // name, protocol, remark, port and inbound ID so the operator can find a
  // specific inbound by whatever piece they remember.
  const [unmanagedSearch, setUnmanagedSearch] = useState('')
  const [loading, setLoading] = useState(false)

  // Distinct values surfaced as Autocomplete suggestions, tagged by source
  // field so MUI's groupBy can render them under section headers like
  // "Server" / "Protocol" / "Remark". Without grouping the menu mixes all
  // fields into one alphabetical blob — e.g. "Premium" (tag) sits next to
  // "HiNet-PQS-Static" (server) and the operator can't tell which field
  // they'd be filtering on. Picking still flows into the same search string.
  // The unmanaged list is already scoped to one panel, so the search no
  // longer offers a panel-name dimension — only protocol / remark within
  // the selected server's inbounds.
  const unmanagedSearchOptions = useMemo<SearchOption[]>(() => {
    const buckets: Array<[string, Set<string>]> = [
      [t('admin:nodes.search_group.protocol'), new Set()],
      [t('admin:nodes.search_group.remark'), new Set()],
    ]
    for (const u of unmanaged) {
      if (u.Protocol) buckets[0][1].add(u.Protocol)
      if (u.Remark) buckets[1][1].add(u.Remark)
    }
    return bucketsToOptions(buckets)
  }, [unmanaged, t])

  const filteredUnmanaged = useMemo(() => {
    const q = unmanagedSearch.trim().toLowerCase()
    if (!q) return unmanaged
    return unmanaged.filter(u =>
      u.Protocol.toLowerCase().includes(q) ||
      (u.Remark || '').toLowerCase().includes(q) ||
      String(u.InboundID) === q ||
      String(u.Port) === q,
    )
  }, [unmanaged, unmanagedSearch])

  // Same UX on the managed tab. Drag-to-reorder is suppressed while the
  // filter narrows the list because the visible row index no longer maps
  // 1:1 to the full managed array — moving a row from displayed-position-3
  // to displayed-position-5 would be ambiguous when filtered-out rows sit
  // between them. Clearing the search re-enables the drag handles.
  const [managedSearch, setManagedSearch] = useState('')

  const managedSearchOptions = useMemo<SearchOption[]>(() => {
    const buckets: Array<[string, Set<string>]> = [
      [t('admin:nodes.table.display_name'), new Set()],
      [t('admin:nodes.table.panel_name'), new Set()],
      [t('admin:nodes.table.region'), new Set()],
      [t('admin:nodes.table.tags'), new Set()],
    ]
    for (const n of managed) {
      if (n.display_name) buckets[0][1].add(n.display_name)
      if (n.panel_name) buckets[1][1].add(n.panel_name)
      if (n.region) buckets[2][1].add(n.region)
      for (const tg of n.tags ?? []) if (tg) buckets[3][1].add(tg)
    }
    return bucketsToOptions(buckets)
  }, [managed, t])

  // Distinct tag pool across all managed nodes, sorted alphabetically.
  // Feeds the Autocomplete dropdowns on the create/edit/import/separator
  // dialogs so admins pick from the existing tag set instead of having to
  // re-type (and risk typo-fragmenting the namespace). freeSolo on the
  // Autocomplete still lets them introduce a brand-new tag — the dropdown
  // is suggestion, not constraint.
  const allTags = useMemo(() => {
    const set = new Set<string>()
    for (const n of managed) for (const tg of n.tags ?? []) if (tg) set.add(tg)
    return Array.from(set).sort((a, b) => a.localeCompare(b))
  }, [managed])

  // Synthesise Node-shaped display rows for separators so the existing
  // table renderer (which already special-cases kind==='separator')
  // doesn't need to learn a union shape. The synthetic node carries the
  // separator's real ID — selection / actions branch on kind, never on
  // ID, so collisions with real node IDs are harmless.
  const separatorRows = useMemo<Node[]>(() => separators.map(s => ({
    id: s.id,
    panel_id: 0,
    panel_name: '',
    inbound_id: 0,
    display_name: s.display_name,
    server_address: '',
    flow: '',
    region: '',
    tags: [],
    sort_order: s.sort_order,
    enabled: s.enabled,
    kind: 'separator',
    region_label: '',
  } as unknown as Node)), [separators])

  // Merged & SortOrder-sorted view that mirrors what the subscription
  // renderer emits, so admin's mental model matches the user's
  // subscription. Tie-breaks: separator above an equally-weighted node.
  const managedCombined = useMemo<Node[]>(() => {
    const out = [...managed, ...separatorRows]
    out.sort((a, b) => {
      if (a.sort_order !== b.sort_order) return a.sort_order - b.sort_order
      const aSep = (a as Node).kind === 'separator'
      const bSep = (b as Node).kind === 'separator'
      if (aSep !== bSep) return aSep ? -1 : 1
      return a.id - b.id
    })
    return out
  }, [managed, separatorRows])

  // Client-side pagination state for the managed table. Server-side
  // would break drag-to-reorder (positions are computed across the
  // full ordering), so we keep the full set in memory and slice in
  // render. Page-size choice persists to localStorage via the shared
  // PSP_PAGE_SIZE convention.
  const [managedPage, setManagedPage] = useState(1)
  const [managedPageSize, setManagedPageSize] = useState<number>(() => {
    try {
      const raw = localStorage.getItem('psp_page_size')
      const n = raw ? parseInt(raw, 10) : 25
      return Number.isFinite(n) && n > 0 ? n : 25
    } catch { return 25 }
  })
  function changeManagedPageSize(n: number) {
    setManagedPageSize(n)
    try { localStorage.setItem('psp_page_size', String(n)) } catch { /* ignore */ }
    setManagedPage(1)
  }
  const filteredManaged = useMemo(() => {
    const q = managedSearch.trim().toLowerCase()
    if (!q) return managedCombined
    return managedCombined.filter(n =>
      n.display_name.toLowerCase().includes(q) ||
      n.panel_name.toLowerCase().includes(q) ||
      n.server_address.toLowerCase().includes(q) ||
      n.region.toLowerCase().includes(q) ||
      (n.tags ?? []).some(tg => tg.toLowerCase().includes(q)) ||
      String(n.id) === q,
    )
  }, [managedCombined, managedSearch])
  const managedFilterActive = managedSearch.trim().length > 0
  // Resliced view that the table actually renders. The drag handlers
  // still operate on absolute indices into filteredManaged, so we
  // translate the page-local index inside onDrop/onDragOver.
  const managedPaged = useMemo(() => {
    const start = (managedPage - 1) * managedPageSize
    return filteredManaged.slice(start, start + managedPageSize)
  }, [filteredManaged, managedPage, managedPageSize])
  // When the filter changes the previous page may be past the new
  // last_page — snap back to 1.
  useEffect(() => { setManagedPage(1) }, [managedSearch])
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [batchBusy, setBatchBusy] = useState<'enable' | 'disable' | 'delete' | ''>('')
  const [enabledBusy, setEnabledBusy] = useState<Record<number, boolean>>({})
  // Drag-to-reorder state. `dragIndex` is the row being dragged (source) and
  // `dropIndex` is the row the cursor is hovering over (target). Both reset
  // on drop / dragend so the row highlights only show during an active drag.
  const [dragIndex, setDragIndex] = useState<number | null>(null)
  const [dropIndex, setDropIndex] = useState<number | null>(null)
  const [reorderBusy, setReorderBusy] = useState(false)

  const [editOpen, setEditOpen] = useState(false)
  const [editBusy, setEditBusy] = useState(false)
  const [editing, setEditing] = useState<Node | null>(null)
  const [editForm, setEditForm] = useState<MetaForm>(EMPTY_META)
  type MetaField = 'display_name' | 'server_address' | 'region'
  const [editMetaErr, setEditMetaErr] = useState<FieldErrors<MetaField>>({})

  const [importOpen, setImportOpen] = useState(false)
  const [importBusy, setImportBusy] = useState(false)
  const [importForm, setImportForm] = useState<ImportForm>(EMPTY_IMPORT)
  const [importErr, setImportErr] = useState<FieldErrors<MetaField>>({})

  // Separator dialog: layout-only rows persisted in the nodes_separator
  // table. Two modes:
  //   null  → POST   /admin/nodes/separator
  //   > 0   → PUT    /admin/nodes/separator/:id
  // Visibility model (rc.4): mode is either 'global' (always visible)
  // or 'node_bound' (visible only when the group includes at least one
  // node from node_ids). Position is always SortOrder (drag-to-reorder).
  type SeparatorForm = {
    display_name: string
    enabled: boolean
    mode: SeparatorMode
    node_ids: number[]
  }
  const EMPTY_SEPARATOR_FORM: SeparatorForm = {
    display_name: '', enabled: true,
    mode: 'global', node_ids: [],
  }
  const [separatorOpen, setSeparatorOpen] = useState(false)
  const [separatorBusy, setSeparatorBusy] = useState(false)
  const [separatorEditingId, setSeparatorEditingId] = useState<number | null>(null)
  const [separatorForm, setSeparatorForm] = useState<SeparatorForm>(EMPTY_SEPARATOR_FORM)
  function openSeparatorCreate() {
    setSeparatorEditingId(null)
    setSeparatorForm(EMPTY_SEPARATOR_FORM)
    setSeparatorOpen(true)
  }
  function openSeparatorEdit(n: Node) {
    // Find the matching separator record (the table row is synthetic).
    const sep = separators.find(s => s.id === n.id)
    if (!sep) return
    setSeparatorEditingId(sep.id)
    setSeparatorForm({
      display_name: sep.display_name,
      enabled: sep.enabled,
      mode: sep.mode,
      node_ids: [...sep.node_ids],
    })
    setSeparatorOpen(true)
  }
  async function submitSeparator() {
    if (!separatorForm.display_name.trim()) {
      pushSnack(t('admin:nodes.create_dialog.validate_required'), 'warning')
      return
    }
    setSeparatorBusy(true)
    try {
      const payload = {
        display_name: separatorForm.display_name.trim(),
        enabled: separatorForm.enabled,
        mode: separatorForm.mode,
        // node_ids is only meaningful under node_bound; sending [] in
        // global mode keeps the backend payload clean.
        node_ids: separatorForm.mode === 'node_bound' ? separatorForm.node_ids : [],
      }
      if (separatorEditingId !== null) {
        await updateSeparator(separatorEditingId, payload)
        pushSnack(t('admin:nodes.toast.separator_updated', { defaultValue: '分隔标题已更新' }), 'success')
      } else {
        await createSeparator(payload)
        pushSnack(t('admin:nodes.toast.separator_created', { defaultValue: '分隔标题已创建' }), 'success')
      }
      setSeparatorOpen(false)
      setSeparatorEditingId(null)
      setSeparatorForm(EMPTY_SEPARATOR_FORM)
      await load()
    } catch { /* axios interceptor toasted */ } finally { setSeparatorBusy(false) }
  }

  const [claimOpen, setClaimOpen] = useState(false)
  const [claimBusy, setClaimBusy] = useState(false)
  const [claimUsers, setClaimUsers] = useState<User[]>([])
  const [claimForm, setClaimForm] = useState({
    panel_id: 0, panel_name: '', inbound_id: 0,
    user_id: 0,
    client_email: '',
    client_uuid: '',
  })
  type ClaimField = 'user_id' | 'client_email'
  const [claimErr, setClaimErr] = useState<FieldErrors<ClaimField>>({})

  const [servers, setServers] = useState<Server[]>([])
  const [createOpen, setCreateOpen] = useState(false)
  const [createBusy, setCreateBusy] = useState(false)
  const [createForm, setCreateForm] = useState<InboundFormState>(EMPTY_INBOUND)
  const [genKeysBusy, setGenKeysBusy] = useState(false)

  const [editInboundOpen, setEditInboundOpen] = useState(false)
  const [editInboundBusy, setEditInboundBusy] = useState(false)
  const [editInboundLoading, setEditInboundLoading] = useState(false)
  const [editInboundForm, setEditInboundForm] = useState<InboundFormState>(EMPTY_INBOUND)
  const [editInboundUnsupported, setEditInboundUnsupported] = useState(false)
  const [editInboundGenBusy, setEditInboundGenBusy] = useState(false)
  const [editingInboundNode, setEditingInboundNode] = useState<Node | null>(null)
  // Advanced-mode toggle is per-dialog UI state, not part of the form
  // (it doesn't persist to backend). Tracked separately so opening edit
  // doesn't carry the create dialog's mode.
  const [createAdvanced, setCreateAdvanced] = useState(false)
  const [editAdvanced, setEditAdvanced] = useState(false)

  const selectableIds = managed.map(n => n.id)
  const allChecked = selectableIds.length > 0 && selectableIds.every(id => selected.has(id))
  const someChecked = selected.size > 0 && !allChecked

  useEffect(() => { void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab])

  useEffect(() => { void loadServers() }, [])

  async function loadServers() {
    // For the picker dropdown we want every panel. Request a large
    // page_size that the backend will clamp to 200, which is plenty
    // for an individual deployment.
    try {
      const res = await listServers({ page: 1, page_size: 200 })
      setServers(res.items)
    } catch { /* toast */ }
  }

  async function load() {
    // Unmanaged is scoped to the picked panel and manages its own loading
    // state, so don't run the managed-tab spinner path for it.
    if (tab === 'unmanaged') {
      await loadUnmanaged(unmanagedPanelId)
      return
    }
    setLoading(true)
    try {
      // Pull nodes + separators in parallel — the table interleaves
      // them but they live on independent endpoints. Group list is
      // also refreshed so the separator dialog's "Show in groups"
      // picker reflects whatever the admin just added in Groups view.
      const [n, s] = await Promise.all([
        // Drag-to-reorder spans the full set, so fetch all nodes here.
        // Large fleets paginate the visible *table* via filteredManaged
        // slicing below but the drag handler still computes positions
        // against the complete ordering.
        listNodes({ page: 1, page_size: 500 }),
        listSeparators().catch(() => [] as Separator[]),
      ])
      setManaged(n.items)
      setSeparators(s)
      setSelected(new Set())
    } finally {
      setLoading(false)
    }
  }

  // loadUnmanaged fetches the selected panel's unmanaged inbounds. With no
  // panel chosen it just clears the list (the UI shows a "pick a server"
  // prompt). A fetch failure (e.g. panel unreachable) is captured for the
  // inline error + Retry affordance.
  // Last-wins guard: switching panels fires a new fetch; a slow earlier
  // panel's response must not overwrite the list/error for the panel now
  // selected.
  const unmanagedSeq = useRef(0)

  async function loadUnmanaged(panelId: number | null) {
    const seq = ++unmanagedSeq.current
    setUnmanagedError(null)
    if (panelId == null) {
      setUnmanaged([])
      return
    }
    setLoading(true)
    try {
      const res = await listUnmanagedInbounds(panelId)
      if (seq === unmanagedSeq.current) setUnmanaged(res.items)
    } catch (e) {
      if (seq === unmanagedSeq.current) {
        setUnmanaged([])
        const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error
          || (e as Error)?.message || String(e)
        setUnmanagedError(msg)
      }
    } finally {
      if (seq === unmanagedSeq.current) setLoading(false)
    }
  }

  function selectUnmanagedPanel(panelId: number | null) {
    setUnmanagedPanelId(panelId)
    setUnmanagedSearch('')
    void loadUnmanaged(panelId)
  }

  function toggleAll(checked: boolean) {
    setSelected(checked ? new Set(selectableIds) : new Set())
  }
  function toggleOne(id: number, checked: boolean) {
    setSelected(prev => {
      const next = new Set(prev)
      if (checked) next.add(id); else next.delete(id)
      return next
    })
  }

  function openEdit(n: Node) {
    setEditing(n)
    setEditForm({
      display_name: n.display_name,
      server_address: n.server_address,
      flow: n.flow ?? '',
      region: n.region,
      tags_text: (n.tags ?? []).join(', '),
      sort_order: n.sort_order,
      relays: (n.relays ?? []).map(r => ({ ...r })),
      hide_direct: !!n.hide_direct,
    })
    setEditMetaErr({})
    setEditOpen(true)
  }

  function validateMeta(f: MetaForm): FieldErrors<MetaField> {
    return {
      display_name: validateName(f.display_name, { required: true, max: 64 }),
      server_address: validateHost(f.server_address, { required: true }),
      region: validateName(f.region, { required: true, max: 32 }),
    }
  }

  async function submitEdit(e: FormEvent) {
    e.preventDefault()
    if (!editing) return
    const errs = validateMeta(editForm)
    setEditMetaErr(errs)
    const firstKey = firstError(errs)
    if (firstKey) { pushSnack(t(`admin:${firstKey}`), 'warning'); return }
    setEditBusy(true)
    try {
      await updateNodeMetadata(editing.id, {
        display_name: editForm.display_name,
        server_address: editForm.server_address,
        flow: editForm.flow || undefined,
        region: editForm.region,
        tags: editForm.tags_text
          ? editForm.tags_text.split(',').map(s => s.trim()).filter(Boolean)
          : [],
        sort_order: editForm.sort_order,
        // Drop blank-address lines, trim, and coerce port to a number so a
        // half-filled row never reaches the backend (which would 400).
        relays: editForm.relays
          .map(r => ({ ...r, name: r.name.trim(), address: r.address.trim(), port: Number(r.port) || 0, sni: r.sni?.trim() || undefined, host: r.host?.trim() || undefined }))
          .filter(r => r.address),
        hide_direct: editForm.hide_direct,
      })
      pushSnack(t('admin:nodes.toast.saved'), 'success')
      setEditOpen(false)
      await load()
    } finally { setEditBusy(false) }
  }

  async function toggleEnabled(n: Node) {
    setEnabledBusy(p => ({ ...p, [n.id]: true }))
    try {
      const next = !n.enabled
      await setNodeEnabled(n.id, next)
      setManaged(prev => prev.map(x => x.id === n.id ? { ...x, enabled: next } : x))
      pushSnack(t(next ? 'admin:nodes.toast.enabled' : 'admin:nodes.toast.disabled'), 'success')
    } finally {
      setEnabledBusy(p => ({ ...p, [n.id]: false }))
    }
  }

  async function confirmDelete(n: Node) {
    const ok = await confirm({
      title: t('admin:nodes.confirm.delete_title'),
      message: t('admin:nodes.confirm.delete_message', { name: n.display_name }),
      destructive: true,
      confirmText: t('admin:nodes.action.delete'),
    })
    if (!ok) return
    if (n.kind === 'separator') {
      await deleteSeparator(n.id)
      setSeparators(prev => prev.filter(s => s.id !== n.id))
    } else {
      await deleteNode(n.id)
      setManaged(prev => prev.filter(x => x.id !== n.id))
    }
    pushSnack(t('admin:nodes.toast.deleted'), 'success')
  }

  // Detach: pure local forget. Drops the node record + ownership whitelist
  // without touching 3X-UI. Use when the upstream server is offline or
  // decommissioned, so PSP doesn't burn retries against a dead panel.
  async function confirmDetach(n: Node) {
    const ok = await confirm({
      title: t('admin:nodes.confirm.detach_title'),
      message: t('admin:nodes.confirm.detach_message', { name: n.display_name }),
      confirmText: t('admin:nodes.action.detach'),
    })
    if (!ok) return
    await detachNode(n.id)
    setManaged(prev => prev.filter(x => x.id !== n.id))
    pushSnack(t('admin:nodes.toast.detached'), 'success')
  }

  // Recreate inbound: PSP rebuilds this node's inbound on its (repointed/empty)
  // server from its captured config and relinks the node. Use after moving the
  // node's Server to a fresh 3X-UI that shows "Connected (0)".
  async function confirmRecreateInbound(n: Node) {
    const ok = await confirm({
      title: t('admin:nodes.confirm.recreate_inbound_title', { defaultValue: '在服务器上重建 inbound?' }),
      message: t('admin:nodes.confirm.recreate_inbound_message', {
        name: n.display_name,
        defaultValue: '用 PSP 保存的配置在「{{name}}」所在服务器上重建 inbound,并把节点指向新 inbound,随后下发 client。适用于把节点迁到全新/空的 3X-UI 后。可重复点击:inbound 已存在则只重新下发 client。',
      }),
      confirmText: t('admin:nodes.action.recreate_inbound', { defaultValue: '重建 inbound' }),
    })
    if (!ok) return
    try {
      await recreateNodeInbound(n.id)
      pushSnack(t('admin:nodes.toast.recreated_inbound', { defaultValue: 'inbound 已在服务器上重建' }), 'success')
      await load()
    } catch (err) {
      const msg = (err as { message?: string }).message ?? 'unknown'
      pushSnack(msg, 'error')
    }
  }

  async function batchSetEnabled(enable: boolean) {
    const rows = managed.filter(n => selected.has(n.id))
    if (!rows.length) return
    setBatchBusy(enable ? 'enable' : 'disable')
    try {
      const results = await Promise.allSettled(rows.map(r => setNodeEnabled(r.id, enable)))
      const failed = results.filter(r => r.status === 'rejected').length
      if (failed > 0) {
        pushSnack(t('admin:nodes.toast.batch_partial', { ok: rows.length - failed, fail: failed }), 'warning')
      } else {
        pushSnack(t(enable ? 'admin:nodes.toast.batch_enabled' : 'admin:nodes.toast.batch_disabled', { count: rows.length }), 'success')
      }
      await load()
    } finally { setBatchBusy('') }
  }

  async function batchDelete() {
    const rows = managed.filter(n => selected.has(n.id))
    if (!rows.length) return
    const ok = await confirm({
      title: t('admin:nodes.confirm.batch_delete_title'),
      message: t('admin:nodes.confirm.batch_delete_message', { count: rows.length }),
      destructive: true,
      confirmText: t('admin:nodes.action.delete'),
    })
    if (!ok) return
    setBatchBusy('delete')
    try {
      const results = await Promise.allSettled(rows.map(r => deleteNode(r.id)))
      const okIds = rows.filter((_, i) => results[i].status === 'fulfilled').map(r => r.id)
      const failed = rows.length - okIds.length
      setManaged(prev => prev.filter(x => !okIds.includes(x.id)))
      setSelected(new Set())
      if (failed > 0) {
        pushSnack(t('admin:nodes.toast.batch_partial', { ok: okIds.length, fail: failed }), 'warning')
      } else {
        pushSnack(t('admin:nodes.toast.batch_deleted', { count: okIds.length }), 'success')
      }
    } finally { setBatchBusy('') }
  }

  // commitReorder: optimistic UI — apply the new order locally first, then
  // push the renumbered (id, sort_order) pairs to the server. On failure
  // revert by reloading. The 10-step renumber keeps room for future single-row
  // inserts without a global shuffle.
  async function commitReorder(fromIdx: number, toIdx: number) {
    if (fromIdx === toIdx) return
    // The drag operates on the COMBINED list (managed + separatorRows
    // sorted by sort_order). Reorder it as one homogeneous sequence,
    // re-assign sort_order in 10-step increments, then SPLIT the
    // outcome into node / separator items and hit the two backend
    // endpoints. Splitting on the client side keeps each PUT
    // homogeneous so the server doesn't have to guess which table an
    // ID belongs to (node IDs and separator IDs come from independent
    // autoincrement sequences and can collide).
    const previousManaged = managed
    const previousSeparators = separators
    const combined = managedCombined
    const next = reorderRows(combined, fromIdx, toIdx)

    // Walk the reordered combined list ONCE, splitting node vs separator
    // by row.kind (NOT by ID set). node IDs and separator IDs come from
    // independent autoincrement sequences so they can collide
    // (sep id=1 and node id=1 both exist) — using an ID set would
    // mis-classify every collided row and either drop one PUT entirely
    // or send duplicate IDs to the separator endpoint, which then
    // 400's on its duplicate-id validator. Sort_order is assigned from
    // the combined position so admins can interleave separators and
    // nodes freely.
    const nodePayload: { id: number; sort_order: number }[] = []
    const sepPayload: { id: number; sort_order: number }[] = []
    const newNodeOrder = new Map<number, number>()
    const newSepOrder = new Map<number, number>()
    next.forEach((row, i) => {
      const sortOrder = (i + 1) * 10
      if ((row as Node).kind === 'separator') {
        sepPayload.push({ id: row.id, sort_order: sortOrder })
        newSepOrder.set(row.id, sortOrder)
      } else {
        nodePayload.push({ id: row.id, sort_order: sortOrder })
        newNodeOrder.set(row.id, sortOrder)
      }
    })

    // Optimistic local state — separate maps keep node/separator updates
    // isolated even when IDs collide.
    setManaged(previousManaged.map(n => ({ ...n, sort_order: newNodeOrder.get(n.id) ?? n.sort_order })))
    setSeparators(previousSeparators.map(s => ({ ...s, sort_order: newSepOrder.get(s.id) ?? s.sort_order })))

    setReorderBusy(true)
    try {
      // Run the two PUTs in parallel — independent tables, no ordering
      // constraint. Promise.all means a failure on either one rejects
      // and we roll the whole optimistic update back together.
      await Promise.all([
        nodePayload.length > 0 ? reorderNodes(nodePayload) : Promise.resolve(),
        sepPayload.length > 0 ? reorderSeparators(sepPayload) : Promise.resolve(),
      ])
      pushSnack(t('admin:nodes.toast.reordered'), 'success')
    } catch (err) {
      setManaged(previousManaged)
      setSeparators(previousSeparators)
      pushSnack(t('admin:nodes.toast.reorder_failed'), 'error')
      // eslint-disable-next-line no-console
      console.error('reorder failed', err)
    } finally {
      setReorderBusy(false)
    }
  }

  function tagsCell(tags: string[]) {
    if (!tags?.length) return <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>—</Typography>
    return (
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5 }}>
        {tags.slice(0, 3).map(tag => (
          <Box key={tag} sx={{
            display: 'inline-block', px: 1.25, py: 0.25,
            borderRadius: 1, fontSize: 12, fontWeight: 500,
            bgcolor: md.surfaceContainerHighest, color: md.onSurfaceVariant, whiteSpace: 'nowrap',
          }}>{tag}</Box>
        ))}
        {tags.length > 3 && (
          <Typography sx={{ fontSize: 11, color: md.onSurfaceVariant, alignSelf: 'center' }}>
            +{tags.length - 3}
          </Typography>
        )}
      </Box>
    )
  }

  // healthDot renders a colored dot for the node's most recent probe
  // outcome with a tooltip carrying the state label, the optional error
  // detail, and the timestamp of the last check. Disabled nodes get a
  // muted "—" since we don't probe them.
  function healthDot(n: Node) {
    if (!n.enabled) {
      return <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>—</Typography>
    }
    const state = n.health_state || ''
    const palette: Record<string, { bg: string; label: string }> = {
      ok:                   { bg: '#22c55e', label: t('admin:nodes.health.ok',                   { defaultValue: '健康' }) },
      panel_unreachable:    { bg: md.error,  label: t('admin:nodes.health.panel_unreachable',    { defaultValue: '面板不可达' }) },
      inbound_missing:      { bg: '#f97316', label: t('admin:nodes.health.inbound_missing',      { defaultValue: 'Inbound 缺失' }) },
      inbound_disabled:     { bg: '#9ca3af', label: t('admin:nodes.health.inbound_disabled',     { defaultValue: 'Inbound 已关闭' }) },
      '':                   { bg: md.outlineVariant, label: t('admin:nodes.health.unknown',      { defaultValue: '尚未探测' }) },
    }
    const p = palette[state] ?? palette['']
    const checkedAt = n.health_checked_at ? formatDualTz(n.health_checked_at, panelTz) : t('admin:nodes.health.never', { defaultValue: '尚未运行' })
    const tooltip = (
      <Box sx={{ fontSize: 12, lineHeight: 1.5 }}>
        <Box sx={{ fontWeight: 600, mb: 0.25 }}>{p.label}</Box>
        {n.health_detail && <Box sx={{ opacity: 0.85, mb: 0.25 }}>{n.health_detail}</Box>}
        <Box sx={{ opacity: 0.7 }}>{t('admin:nodes.health.checked_at', { time: checkedAt, defaultValue: `上次探测：${checkedAt}` })}</Box>
      </Box>
    )
    return (
      <Tooltip title={tooltip} arrow>
        <Box sx={{
          display: 'inline-block', width: 10, height: 10, borderRadius: '50%',
          bgcolor: p.bg, cursor: 'help',
        }} />
      </Tooltip>
    )
  }

  // configSyncDot renders a small SQUARE (distinct from the round health dot) for
  // the node's inbound-config snapshot state — whether PSP's locally-stored config
  // (the render truth source since v3.5) is in sync with 3X-UI. "drift"/"pending"
  // are the states worth noticing (reconcile re-pushes); "synced" is the steady
  // state; "" means never captured (render live-fetches this node). Only shown for
  // enabled real nodes — disabled nodes aren't reconciled, so their state is moot.
  function configSyncDot(n: Node) {
    const state = n.config_sync_state || ''
    const palette: Record<string, { bg: string; label: string }> = {
      synced:  { bg: '#22c55e',           label: t('admin:nodes.config_sync.synced',     { defaultValue: '配置已同步' }) },
      drift:   { bg: '#f97316',           label: t('admin:nodes.config_sync.drift',      { defaultValue: '配置漂移（reconcile 将下发对齐）' }) },
      pending: { bg: md.error,            label: t('admin:nodes.config_sync.pending',    { defaultValue: '配置下发待重试' }) },
      '':      { bg: md.outlineVariant,   label: t('admin:nodes.config_sync.uncaptured', { defaultValue: '未捕获本地配置（渲染时回源）' }) },
    }
    const p = palette[state] ?? palette['']
    const syncedAt = n.config_synced_at ? formatDualTz(n.config_synced_at, panelTz) : t('admin:nodes.config_sync.never', { defaultValue: '尚未捕获' })
    const tooltip = (
      <Box sx={{ fontSize: 12, lineHeight: 1.5 }}>
        <Box sx={{ fontWeight: 600, mb: 0.25 }}>{p.label}</Box>
        <Box sx={{ opacity: 0.7 }}>{t('admin:nodes.config_sync.synced_at', { time: syncedAt, defaultValue: `上次捕获：${syncedAt}` })}</Box>
      </Box>
    )
    return (
      <Tooltip title={tooltip} arrow>
        <Box sx={{
          display: 'inline-block', width: 9, height: 9, borderRadius: '2px',
          bgcolor: p.bg, cursor: 'help',
        }} />
      </Tooltip>
    )
  }

  function openCreate() {
    if (servers.length === 0) {
      pushSnack(t('admin:nodes.create_dialog.no_servers'), 'warning')
      return
    }
    setCreateForm({ ...EMPTY_INBOUND, panel_id: servers[0].id, server_address: hostFromURL(servers[0].url) })
    setCreateAdvanced(false)
    setCreateOpen(true)
  }

  async function genKeys() {
    setGenKeysBusy(true)
    try {
      const kp = await generateRealityKeypair()
      setCreateForm(f => ({
        ...f,
        private_key: kp.private_key,
        public_key: kp.public_key,
        short_ids_text: kp.short_id,
      }))
    } finally { setGenKeysBusy(false) }
  }

  function genSSPasswordCreate() {
    const method = SS2022_METHODS.find(m => m.value === createForm.ss_method)
    setCreateForm(f => ({ ...f, ss_password: randomBase64(method?.bytes ?? 32) }))
  }

  async function submitCreate(e: FormEvent) {
    e.preventDefault()
    const f = createForm
    if (createAdvanced) {
      // Advanced mode skips the structured validator (which checks
      // fields like reality_dest that the raw JSON path doesn't care
      // about). We only require the metadata + JSON parses below.
      if (!f.display_name || !f.server_address || !f.region) {
        pushSnack(t('admin:nodes.create_dialog.validate_required'), 'warning'); return
      }
      for (const [label, raw] of [['settings', f.raw_settings], ['streamSettings', f.raw_stream_settings], ['sniffing', f.raw_sniffing]] as const) {
        try { JSON.parse(raw || '{}') }
        catch { pushSnack(t('admin:nodes.create_dialog.advanced_invalid_json', { field: label, defaultValue: `${label} 不是合法的 JSON` }), 'warning'); return }
      }
    } else {
      const err = validateInboundForm(f, t)
      if (err) { pushSnack(err, 'warning'); return }
    }

    const settings = createAdvanced
      ? (f.raw_settings || '{}')
      : JSON.stringify(settingsBuilderFor(f.protocol)(f))
    const streamSettings = createAdvanced
      ? (f.raw_stream_settings || '{}')
      : JSON.stringify(buildStreamSettings(f))
    const sniffing = createAdvanced
      ? (f.raw_sniffing || '{}')
      : JSON.stringify(buildSniffing(f))

    setCreateBusy(true)
    try {
      const res = await createInbound({
        panel_id: f.panel_id,
        display_name: f.display_name,
        server_address: f.server_address,
        flow: f.protocol === 'vless' ? f.vless_flow.trim() : '',
        region: f.region,
        tags: f.tags_text ? f.tags_text.split(',').map(s => s.trim()).filter(Boolean) : [],
        sort_order: f.sort_order,
        inbound: {
          remark: f.display_name,
          enable: f.enable,
          listen: f.listen,
          port: f.port,
          protocol: xuiProtocolName(f.protocol),
          settings,
          stream_settings: streamSettings,
          sniffing,
          allocate: '',
          expiry_time: 0,
        },
      })
      // If the cert source is "PSP-managed", bind + deploy the picked cert now
      // that the node and its live inbound exist. Best-effort: a binding failure
      // doesn't undo the successful node creation (the operator can retry from
      // the edit dialog). Only possible for a synchronously-created node (the
      // queued path has no id yet — those bind later via the edit dialog).
      if (!('queued' in res) && f.tls_cert_mode === 'psp_managed' && f.cert_id > 0) {
        try {
          await setNodeCertSource(res.id, 'psp_managed', f.cert_id)
        } catch { /* binding error surfaces via the axios interceptor */ }
      }
      pushSnack(
        'queued' in res
          ? t('admin:nodes.create_dialog.queued')
          : t('admin:nodes.create_dialog.created'),
        'success',
      )
      setCreateOpen(false)
      setTab('managed')
      await load()
    } finally { setCreateBusy(false) }
  }

  async function openEditInbound(n: Node) {
    setEditingInboundNode(n)
    setEditInboundUnsupported(false)
    setEditInboundLoading(true)
    setEditAdvanced(false)
    setEditInboundOpen(true)
    setEditInboundForm({
      ...EMPTY_INBOUND,
      panel_id: n.panel_id,
      display_name: n.display_name,
      server_address: n.server_address,
      region: n.region,
      tags_text: (n.tags ?? []).join(', '),
      sort_order: n.sort_order,
      vless_flow: n.flow ?? 'xtls-rprx-vision',
      enable: n.enabled,
    })
    try {
      const detail = await getNode(n.id)
      if (!detail.inbound) {
        setEditInboundUnsupported(true)
        return
      }
      const ib = detail.inbound
      if (ib.protocol !== 'vless' && ib.protocol !== 'shadowsocks' &&
          ib.protocol !== 'vmess' && ib.protocol !== 'trojan' &&
          ib.protocol !== 'hysteria2') {
        setEditInboundUnsupported(true)
        return
      }
      setEditInboundForm(parseInboundForEdit(n, ib as InboundDetail))
    } catch (err) {
      const msg = (err as { message?: string }).message ?? 'unknown'
      pushSnack(t('admin:nodes.edit_inbound_dialog.load_failed', { error: msg }), 'error')
      setEditInboundOpen(false)
    } finally { setEditInboundLoading(false) }
  }

  async function genKeysForEdit() {
    setEditInboundGenBusy(true)
    try {
      const kp = await generateRealityKeypair()
      setEditInboundForm(f => ({
        ...f,
        private_key: kp.private_key,
        public_key: kp.public_key,
        short_ids_text: kp.short_id,
      }))
    } finally { setEditInboundGenBusy(false) }
  }

  function genSSPasswordEdit() {
    const method = SS2022_METHODS.find(m => m.value === editInboundForm.ss_method)
    setEditInboundForm(f => ({ ...f, ss_password: randomBase64(method?.bytes ?? 32) }))
  }

  async function submitEditInbound(e: FormEvent) {
    e.preventDefault()
    if (!editingInboundNode) return
    const f = editInboundForm
    if (editAdvanced) {
      for (const [label, raw] of [['settings', f.raw_settings], ['streamSettings', f.raw_stream_settings], ['sniffing', f.raw_sniffing]] as const) {
        try { JSON.parse(raw || '{}') }
        catch { pushSnack(t('admin:nodes.create_dialog.advanced_invalid_json', { field: label, defaultValue: `${label} 不是合法的 JSON` }), 'warning'); return }
      }
    } else if (f.protocol === 'vless' && f.vless_security === 'reality') {
      if (!f.private_key || !f.public_key || splitList(f.short_ids_text).length === 0) {
        pushSnack(t('admin:nodes.create_dialog.validate_reality_keys'), 'warning'); return
      }
      if (!f.reality_dest || splitList(f.reality_server_names_text).length === 0) {
        pushSnack(t('admin:nodes.create_dialog.validate_reality_target'), 'warning'); return
      }
    } else if (f.protocol === 'ss2022') {
      if (!f.ss_method || !f.ss_password) {
        pushSnack(t('admin:nodes.create_dialog.validate_ss2022'), 'warning'); return
      }
    }
    const settings = editAdvanced
      ? (f.raw_settings || '{}')
      : JSON.stringify(settingsBuilderFor(f.protocol)(f))
    const streamSettings = editAdvanced
      ? (f.raw_stream_settings || '{}')
      : JSON.stringify(buildStreamSettings(f))
    const sniffing = editAdvanced
      ? (f.raw_sniffing || '{}')
      : JSON.stringify(buildSniffing(f))
    setEditInboundBusy(true)
    try {
      await updateInboundConfig(editingInboundNode.id, {
        remark: f.display_name,
        enable: f.enable,
        listen: f.listen,
        port: f.port,
        protocol: xuiProtocolName(f.protocol),
        settings, stream_settings: streamSettings, sniffing,
        allocate: '',
      })
      // VLESS flow is a node-level property (stored on the node row, consumed by
      // the subscription renderers) — updateInboundConfig does NOT carry it, so
      // a flow change made in this dialog would otherwise be silently dropped.
      // Persist it via the metadata endpoint when it actually changed, mirroring
      // the metadata-edit dialog. (Non-VLESS protocols clear it.)
      const newFlow = f.protocol === 'vless' ? f.vless_flow.trim() : ''
      if (newFlow !== (editingInboundNode.flow ?? '')) {
        try {
          await updateNodeMetadata(editingInboundNode.id, {
            display_name: editingInboundNode.display_name,
            server_address: editingInboundNode.server_address,
            flow: newFlow || undefined,
            region: editingInboundNode.region,
            tags: editingInboundNode.tags ?? [],
            sort_order: editingInboundNode.sort_order,
          })
        } catch { /* toast via interceptor */ }
      }
      // Reconcile the managed-cert binding (the unified Certificate-source
      // selector). For psp_managed we ALWAYS re-deploy: the updateInboundConfig
      // push above emitted certificates:[] (managed mode carries no inline cert),
      // so the bound cert must be re-inlined by SetNodeCertSource→DeployToNode,
      // which re-reads the just-saved inbound and pushes it back with the cert.
      const wantManaged = f.tls_cert_mode === 'psp_managed' && f.cert_id > 0
      const prevManaged = editingInboundNode.cert_source === 'psp_managed'
      try {
        if (wantManaged) {
          await setNodeCertSource(editingInboundNode.id, 'psp_managed', f.cert_id)
        } else if (prevManaged) {
          // Switched away from managed → unbind so the renewal worker stops
          // re-deploying. The manual cert (if any) was already pushed above.
          await setNodeCertSource(editingInboundNode.id, 'manual', 0)
        }
      } catch { /* toast via interceptor */ }
      pushSnack(t('admin:nodes.edit_inbound_dialog.saved'), 'success')
      setEditInboundOpen(false)
      await load()
    } finally { setEditInboundBusy(false) }
  }

  async function startClaim(u: UnmanagedInbound) {
    setClaimForm({
      panel_id: u.PanelID, panel_name: u.PanelName, inbound_id: u.InboundID,
      user_id: 0, client_email: '', client_uuid: '',
    })
    setClaimOpen(true)
    try {
      const res = await listUsers({ page: 1, page_size: 200 })
      setClaimUsers(res.items)
    } catch { /* toasted */ }
  }

  async function submitClaim(e: FormEvent) {
    e.preventDefault()
    const errs: FieldErrors<ClaimField> = {
      user_id: claimForm.user_id ? '' : 'validation.required',
      // 3X-UI uses the client_email field as a unique key per inbound, not
      // an actual mailbox — but it still has to look like one. Reject typos
      // (spaces, missing @) before they hit the panel and confuse downstream
      // matching.
      client_email: validateEmail(claimForm.client_email, { required: true }),
    }
    setClaimErr(errs)
    const firstKey = firstError(errs)
    if (firstKey) { pushSnack(t(`admin:${firstKey}`), 'warning'); return }
    setClaimBusy(true)
    try {
      await claimClient({
        user_id: claimForm.user_id,
        panel_id: claimForm.panel_id,
        inbound_id: claimForm.inbound_id,
        client_email: claimForm.client_email,
        client_uuid: claimForm.client_uuid || undefined,
      })
      pushSnack(t('admin:nodes.claim_dialog.claimed'), 'success')
      setClaimOpen(false)
      await load()
    } finally { setClaimBusy(false) }
  }

  function startImport(u: UnmanagedInbound) {
    setImportForm({
      ...EMPTY_IMPORT,
      panel_id: u.PanelID,
      panel_name: u.PanelName,
      inbound_id: u.InboundID,
      protocol: (u.Protocol || '').toLowerCase(),
      display_name: u.Remark || `${u.Protocol}:${u.Port}`,
      // Pre-fill Address with the source panel's host (editable default —
      // proxy host usually equals the 3X-UI host, but admins can override).
      server_address: hostFromURL(servers.find(s => s.id === u.PanelID)?.url ?? ''),
    })
    setImportErr({})
    setImportOpen(true)
  }

  async function submitImport(e: FormEvent) {
    e.preventDefault()
    const errs = validateMeta(importForm)
    setImportErr(errs)
    const firstKey = firstError(errs)
    if (firstKey) { pushSnack(t(`admin:${firstKey}`), 'warning'); return }
    setImportBusy(true)
    try {
      await importNode({
        panel_id: importForm.panel_id,
        inbound_id: importForm.inbound_id,
        display_name: importForm.display_name,
        server_address: importForm.server_address,
        protocol: importForm.protocol || undefined,
        // Flow only applies to VLESS; never persist it for other protocols.
        flow: importForm.protocol === 'vless' ? (importForm.flow || undefined) : undefined,
        region: importForm.region,
        tags: importForm.tags_text
          ? importForm.tags_text.split(',').map(s => s.trim()).filter(Boolean)
          : [],
        sort_order: importForm.sort_order,
      })
      pushSnack(t('admin:nodes.imported'), 'success')
      setImportOpen(false)
      setTab('managed')
      await load()
    } finally { setImportBusy(false) }
  }

  return (
    <Box sx={{ p: 3 }}>
      <PageHeader
        title={t('admin:nodes.title')}
        subtitle={t('admin:nodes.subtitle')}
        actions={canConfig && <>
          <Button variant="outlined" startIcon={<AddIcon />} onClick={openSeparatorCreate}>
            {t('admin:nodes.create_separator', { defaultValue: '新增分隔标题' })}
          </Button>
          <Button variant="contained" startIcon={<AddIcon />} onClick={openCreate}>
            {t('admin:nodes.create')}
          </Button>
        </>}
      />
      <Tabs value={tab} onChange={(_, v) => setTab(v)} sx={{ mt: 2, mb: 2, borderBottom: `1px solid ${md.outlineVariant}` }}>
        <Tab value="managed" label={t('admin:nodes.tab_managed')} />
        <Tab value="unmanaged" label={t('admin:nodes.tab_unmanaged')} />
      </Tabs>
      {tab === 'managed' && selected.size > 0 && (
        <Box sx={{
          display: 'flex', alignItems: 'center', gap: 1, mb: 2, px: 2, py: 1,
          borderRadius: 9999, bgcolor: md.secondaryContainer, color: md.onSecondaryContainer,
          width: 'fit-content',
        }}>
          <Typography sx={{ fontSize: 13, fontWeight: 500, mr: 1 }}>
            {t('admin:nodes.selection_count', { count: selected.size })}
          </Typography>
          <Button size="small" variant="text" sx={{ color: 'inherit' }}
            disabled={batchBusy !== ''}
            startIcon={batchBusy === 'enable' ? <CircularProgress size={14} /> : undefined}
            onClick={() => batchSetEnabled(true)}>
            {t('admin:nodes.batch_enable')}
          </Button>
          <Button size="small" variant="text" sx={{ color: 'inherit' }}
            disabled={batchBusy !== ''}
            startIcon={batchBusy === 'disable' ? <CircularProgress size={14} /> : undefined}
            onClick={() => batchSetEnabled(false)}>
            {t('admin:nodes.batch_disable')}
          </Button>
          {canConfig && <Button size="small" variant="text" color="error"
            disabled={batchBusy !== ''}
            startIcon={batchBusy === 'delete' ? <CircularProgress size={14} /> : <DeleteIcon />}
            onClick={batchDelete}>
            {t('admin:nodes.batch_delete')}
          </Button>}
        </Box>
      )}
      <Card sx={{ bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
        {tab === 'managed' && (
          <>
            <Box sx={{ p: 2, display: 'flex', alignItems: 'center', gap: 1.5, borderBottom: `1px solid ${md.outlineVariant}`, flexWrap: 'wrap' }}>
              <Autocomplete
                freeSolo
                size="small"
                options={managedSearchOptions}
                groupBy={(o) => o.group}
                getOptionLabel={(o) => typeof o === 'string' ? o : o.label}
                value={managedSearch}
                inputValue={managedSearch}
                onInputChange={(_, v) => setManagedSearch(v)}
                onChange={(_, v) => {
                  if (v == null) setManagedSearch('')
                  else if (typeof v === 'string') setManagedSearch(v)
                  else setManagedSearch(v.label)
                }}
                sx={{ width: 320, maxWidth: '100%' }}
                renderInput={(params) => (
                  <TextField {...params} placeholder={t('admin:nodes.managed_search_placeholder')} />
                )}
              />
              <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
                {t('admin:nodes.managed_count', {
                  shown: filteredManaged.length,
                  total: managed.length,
                })}
              </Typography>
              {managedFilterActive && (
                <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, fontStyle: 'italic' }}>
                  {t('admin:nodes.managed_search_disables_drag')}
                </Typography>
              )}
            </Box>
          <TableContainer>
            <Table>
              <TableHead>
                <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                  <TableCell padding="none" sx={{ width: 32 }} />
                  <TableCell padding="checkbox">
                    <Checkbox indeterminate={someChecked} checked={allChecked}
                      onChange={(_, c) => toggleAll(c)}
                      disabled={selectableIds.length === 0} />
                  </TableCell>
                  <TableCell>{t('admin:nodes.table.id')}</TableCell>
                  <TableCell>{t('admin:nodes.table.display_name')}</TableCell>
                  <TableCell>{t('admin:nodes.table.panel_name')}</TableCell>
                  <TableCell>{t('admin:nodes.table.server_address')}</TableCell>
                  <TableCell>{t('admin:nodes.table.region')}</TableCell>
                  <TableCell>{t('admin:nodes.table.tags')}</TableCell>
                  <TableCell align="center">{t('admin:nodes.table.health', { defaultValue: '状态' })}</TableCell>
                  <TableCell align="center">{t('admin:nodes.table.enabled')}</TableCell>
                  <TableCell align="right">{t('admin:nodes.table.actions')}</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {loading && managed.length === 0 && (
                  <TableRow><TableCell colSpan={11} sx={{ textAlign: 'center', py: 6 }}>
                    <CircularProgress size={24} />
                  </TableCell></TableRow>
                )}
                {!loading && filteredManaged.length === 0 && (
                  <TableRow><TableCell colSpan={11} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>
                    {managed.length === 0 ? '—' : t('admin:nodes.managed_filter_empty')}
                  </TableCell></TableRow>
                )}
                {managedPaged.map((n, pageIdx) => {
                  const idx = (managedPage - 1) * managedPageSize + pageIdx
                  const isSep = n.kind === 'separator'
                  return (
                    <TableRow key={isSep ? `sep-${n.id}` : `node-${n.id}`} hover
                      draggable={!reorderBusy && !managedFilterActive}
                      onDragStart={e => {
                        setDragIndex(idx)
                        // Required for Firefox to actually start the drag.
                        try { e.dataTransfer.setData('text/plain', String(n.id)) } catch { /* ignore */ }
                        e.dataTransfer.effectAllowed = 'move'
                      }}
                      onDragOver={e => {
                        if (dragIndex === null) return
                        e.preventDefault()
                        e.dataTransfer.dropEffect = 'move'
                        if (dropIndex !== idx) setDropIndex(idx)
                      }}
                      onDragLeave={() => {
                        if (dropIndex === idx) setDropIndex(null)
                      }}
                      onDrop={e => {
                        e.preventDefault()
                        const from = dragIndex
                        setDragIndex(null)
                        setDropIndex(null)
                        if (from === null || from === idx) return
                        void commitReorder(from, idx)
                      }}
                      onDragEnd={() => {
                        setDragIndex(null)
                        setDropIndex(null)
                      }}
                      sx={{
                        '& td': { borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' },
                        opacity: dragIndex === idx ? 0.4 : (n.enabled ? 1 : 0.65),
                        cursor: reorderBusy ? 'wait' : 'default',
                        bgcolor: dropIndex === idx && dragIndex !== null && dragIndex !== idx
                          ? alpha(md.primary, 0.08)
                          : 'transparent',
                        transition: 'background-color 120ms',
                      }}>
                      <TableCell padding="none" sx={{ width: 32, textAlign: 'center', color: md.onSurfaceVariant, cursor: reorderBusy ? 'wait' : (managedFilterActive ? 'not-allowed' : 'grab'), opacity: managedFilterActive ? 0.4 : 1 }}>
                        <Tooltip title={t('admin:nodes.action.drag_to_reorder')}>
                          <DragIndicatorIcon fontSize="small" sx={{ verticalAlign: 'middle', opacity: 0.7 }} />
                        </Tooltip>
                      </TableCell>
                      <TableCell padding="checkbox">
                        <Checkbox checked={selected.has(n.id)} onChange={(_, c) => toggleOne(n.id, c)} />
                      </TableCell>
                      <TableCell sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{n.id}</TableCell>
                      <TableCell sx={{ fontWeight: 500, fontStyle: isSep ? 'italic' : 'normal', color: isSep ? md.primary : 'inherit' }}>
                        {n.display_name}
                      </TableCell>
                      <TableCell sx={{ fontSize: 13 }}>{isSep ? '—' : n.panel_name}</TableCell>
                      <TableCell sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{isSep ? '—' : n.server_address}</TableCell>
                      <TableCell sx={{ fontSize: 13 }}>{isSep && !n.region ? '—' : n.region}</TableCell>
                      <TableCell>{isSep && (!n.tags || n.tags.length === 0) ? '—' : tagsCell(n.tags)}</TableCell>
                      <TableCell align="center">{isSep ? '—' : (
                        <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.75 }}>
                          {healthDot(n)}
                          {n.enabled && configSyncDot(n)}
                        </Box>
                      )}</TableCell>
                      <TableCell align="center">
                        <Switch checked={n.enabled} onChange={() => toggleEnabled(n)} disabled={enabledBusy[n.id]} />
                      </TableCell>
                      <TableCell align="right">
                        {canConfig && (isSep ? (
                          // Separators don't have inbound config / 3X-UI binding /
                          // detach semantics — only edit + delete are meaningful.
                          // Edit routes through openSeparatorEdit so the dialog
                          // shows only the layout-relevant fields rather than the
                          // full real-node edit form.
                          (<>
                            <Tooltip title={t('admin:nodes.action.edit')}>
                              <IconButton size="small" onClick={() => openSeparatorEdit(n)}>
                                <EditIcon fontSize="small" />
                              </IconButton>
                            </Tooltip>
                            <Tooltip title={t('admin:nodes.action.delete')}>
                              <IconButton size="small" onClick={() => confirmDelete(n)} sx={{ color: md.error }}>
                                <DeleteIcon fontSize="small" />
                              </IconButton>
                            </Tooltip>
                          </>)
                        ) : (
                          <>
                            <Tooltip title={t('admin:nodes.action.edit')}>
                              <IconButton size="small" onClick={() => openEdit(n)}><EditIcon fontSize="small" /></IconButton>
                            </Tooltip>
                            <Tooltip title={t('admin:nodes.edit_inbound')}>
                              <IconButton size="small" onClick={() => openEditInbound(n)}>
                                <KeyIcon fontSize="small" />
                              </IconButton>
                            </Tooltip>
                            <Tooltip title={t('admin:nodes.action.recreate_inbound', { defaultValue: '在服务器上重建 inbound' })}>
                              <IconButton size="small" onClick={() => confirmRecreateInbound(n)}>
                                <CloudSyncIcon fontSize="small" />
                              </IconButton>
                            </Tooltip>
                            <Tooltip title={t('admin:nodes.action.detach')}>
                              <IconButton size="small" onClick={() => confirmDetach(n)} sx={{ color: md.tertiary }}>
                                <LinkOffIcon fontSize="small" />
                              </IconButton>
                            </Tooltip>
                            <Tooltip title={t('admin:nodes.action.delete')}>
                              <IconButton size="small" onClick={() => confirmDelete(n)} sx={{ color: md.error }}>
                                <DeleteIcon fontSize="small" />
                              </IconButton>
                            </Tooltip>
                          </>
                        ))}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </TableContainer>
          <PagedTableFooter
            total={filteredManaged.length}
            page={managedPage}
            pageSize={managedPageSize}
            onPageChange={setManagedPage}
            onPageSizeChange={changeManagedPageSize}
          />
          </>
        )}

        {tab === 'unmanaged' && (
          <>
            <Box sx={{ p: 2, display: 'flex', alignItems: 'center', gap: 1.5, borderBottom: `1px solid ${md.outlineVariant}`, flexWrap: 'wrap' }}>
              {/* Server picker drives the whole tab: only the chosen panel is
                  queried. Type-to-filter via Autocomplete for many servers. */}
              <Autocomplete
                size="small"
                options={servers}
                getOptionLabel={(o) => o.name}
                isOptionEqualToValue={(a, b) => a.id === b.id}
                value={servers.find(s => s.id === unmanagedPanelId) ?? null}
                onChange={(_, v) => selectUnmanagedPanel(v ? v.id : null)}
                sx={{ width: 280, maxWidth: '100%' }}
                renderInput={(params) => (
                  <TextField {...params} label={t('admin:nodes.unmanaged_server_label', { defaultValue: '服务器' })}
                    placeholder={t('admin:nodes.unmanaged_server_placeholder', { defaultValue: '选择服务器…' })} />
                )}
              />
              {unmanagedPanelId != null && !unmanagedError && (
                <>
                  <Autocomplete
                    freeSolo
                    size="small"
                    options={unmanagedSearchOptions}
                    groupBy={(o) => o.group}
                    getOptionLabel={(o) => typeof o === 'string' ? o : o.label}
                    value={unmanagedSearch}
                    inputValue={unmanagedSearch}
                    onInputChange={(_, v) => setUnmanagedSearch(v)}
                    onChange={(_, v) => {
                      if (v == null) setUnmanagedSearch('')
                      else if (typeof v === 'string') setUnmanagedSearch(v)
                      else setUnmanagedSearch(v.label)
                    }}
                    sx={{ width: 280, maxWidth: '100%' }}
                    renderInput={(params) => (
                      <TextField {...params} placeholder={t('admin:nodes.unmanaged_search_placeholder')} />
                    )}
                  />
                  <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
                    {t('admin:nodes.unmanaged_count', {
                      shown: filteredUnmanaged.length,
                      total: unmanaged.length,
                    })}
                  </Typography>
                </>
              )}
            </Box>

            {/* No server picked yet — prompt instead of scanning every panel. */}
            {unmanagedPanelId == null && (
              <Box sx={{ textAlign: 'center', py: 8, color: md.onSurfaceVariant }}>
                {t('admin:nodes.unmanaged_pick_server', { defaultValue: '选择一个服务器以查看其未托管的 inbound' })}
              </Box>
            )}

            {/* Selected panel failed to load (e.g. unreachable) — scoped to
                this server, so it never blocks the rest of the UI. */}
            {unmanagedPanelId != null && unmanagedError && !loading && (
              <Box sx={{ textAlign: 'center', py: 8, color: md.onSurfaceVariant, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 1.5 }}>
                <Typography sx={{ color: md.error }}>
                  {t('admin:nodes.unmanaged_load_failed', { defaultValue: '加载该服务器的 inbound 失败' })}：{unmanagedError}
                </Typography>
                <Button size="small" variant="outlined" onClick={() => void loadUnmanaged(unmanagedPanelId)}>
                  {t('common:actions.retry', { defaultValue: '重试' })}
                </Button>
              </Box>
            )}

            {unmanagedPanelId != null && !unmanagedError && (
              <TableContainer>
                <Table>
                  <TableHead>
                    <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                      <TableCell>Inbound ID</TableCell>
                      <TableCell>Protocol</TableCell>
                      <TableCell align="right">Port</TableCell>
                      <TableCell>Remark</TableCell>
                      <TableCell align="right">Clients</TableCell>
                      <TableCell align="right">{t('admin:nodes.table.actions')}</TableCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {loading && (
                      <TableRow><TableCell colSpan={6} sx={{ textAlign: 'center', py: 6 }}>
                        <CircularProgress size={24} />
                      </TableCell></TableRow>
                    )}
                    {!loading && filteredUnmanaged.length === 0 && (
                      <TableRow><TableCell colSpan={6} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>
                        {unmanaged.length === 0 ? '—' : t('admin:nodes.unmanaged_filter_empty')}
                      </TableCell></TableRow>
                    )}
                    {!loading && filteredUnmanaged.map((u, idx) => (
                      <TableRow key={`${u.PanelID}-${u.InboundID}-${idx}`} hover sx={{ '& td': { borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                        <TableCell sx={{ fontSize: 13 }}>{u.InboundID}</TableCell>
                        <TableCell sx={{ fontSize: 13 }}>{u.Protocol}</TableCell>
                        <TableCell align="right" sx={{ fontSize: 13 }}>{u.Port}</TableCell>
                        <TableCell sx={{ fontSize: 13 }}>{u.Remark}</TableCell>
                        <TableCell align="right" sx={{ fontVariantNumeric: 'tabular-nums' }}>{u.ClientCount}</TableCell>
                        <TableCell align="right" sx={{ whiteSpace: 'nowrap' }}>
                          {canConfig && <>
                          {u.ClientCount > 0 && (
                            <Button size="small" variant="text" onClick={() => startClaim(u)} sx={{ mr: 1 }}>
                              {t('admin:nodes.claim')}
                            </Button>
                          )}
                          <Button size="small" variant="outlined" onClick={() => startImport(u)}>
                            {t('admin:nodes.import')}
                          </Button>
                          </>}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </TableContainer>
            )}
          </>
        )}
      </Card>
      {/* Create inbound dialog (multi-protocol) */}
      <Dialog open={createOpen} onClose={() => !createBusy && setCreateOpen(false)}
        slotProps={{
          paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 800, maxWidth: '95vw' } }
        }}>
        <DialogTitle sx={{ pt: 2.5, pb: 1, fontSize: 18 }}>
          {t('admin:nodes.create_dialog.title_dynamic', {
            protocol: PROTOCOL_OPTIONS.find(o => o.value === createForm.protocol)?.label ?? createForm.protocol,
          })}
        </DialogTitle>
        <DialogContent sx={{ pt: 1 }}>
          <Box component="form" id="create-form" onSubmit={submitCreate}>
            <InboundFormFields form={createForm} setForm={setCreateForm}
              showMetadata
              servers={servers}
              onGenKeys={genKeys}
              onGenSSPassword={genSSPasswordCreate}
              genKeysBusy={genKeysBusy}
              advanced={createAdvanced}
              onSetAdvanced={setCreateAdvanced}
              allTags={allTags}
            />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setCreateOpen(false)} disabled={createBusy} variant="text">{t('common:actions.cancel')}</Button>
          <Button type="submit" form="create-form" variant="contained" disabled={createBusy}
            startIcon={createBusy ? <CircularProgress size={16} color="inherit" /> : null}>
            {t('common:actions.ok')}
          </Button>
        </DialogActions>
      </Dialog>
      {/* Edit Inbound config dialog (multi-protocol) */}
      <Dialog open={editInboundOpen} onClose={() => !editInboundBusy && setEditInboundOpen(false)}
        slotProps={{
          paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 800, maxWidth: '95vw' } }
        }}>
        <DialogTitle sx={{ pt: 2.5, pb: 1, fontSize: 18 }}>
          {t('admin:nodes.edit_inbound_dialog.title')}{editingInboundNode ? ` — ${editingInboundNode.display_name}` : ''}
        </DialogTitle>
        <DialogContent sx={{ pt: 1 }}>
          {editInboundLoading ? (
            <Box sx={{ display: 'grid', placeItems: 'center', py: 4 }}><CircularProgress size={24} /></Box>
          ) : editInboundUnsupported ? (
            <Typography sx={{ color: md.onSurfaceVariant, py: 2 }}>
              {t('admin:nodes.edit_inbound_dialog.unsupported')}
            </Typography>
          ) : (
            <Box component="form" id="edit-inbound-form" onSubmit={submitEditInbound}>
              <InboundFormFields form={editInboundForm} setForm={setEditInboundForm}
                showMetadata={false}
                scanSourceName={editingInboundNode?.panel_name}
                onGenKeys={genKeysForEdit}
                onGenSSPassword={genSSPasswordEdit}
                genKeysBusy={editInboundGenBusy}
                protocolReadonly
                advanced={editAdvanced}
                onSetAdvanced={setEditAdvanced}
              />
            </Box>
          )}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setEditInboundOpen(false)} disabled={editInboundBusy} variant="text">{t('common:actions.cancel')}</Button>
          {!editInboundUnsupported && !editInboundLoading && (
            <Button type="submit" form="edit-inbound-form" variant="contained" disabled={editInboundBusy}
              startIcon={editInboundBusy ? <CircularProgress size={16} color="inherit" /> : null}>
              {t('common:actions.ok')}
            </Button>
          )}
        </DialogActions>
      </Dialog>
      {/* Claim existing 3X-UI client dialog */}
      <Dialog open={claimOpen} onClose={() => !claimBusy && setClaimOpen(false)}
        slotProps={{
          paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 520, maxWidth: '90vw' } }
        }}>
        <DialogTitle>{t('admin:nodes.claim_dialog.title')}</DialogTitle>
        <DialogContent>
          <Typography variant="body2" sx={{ mb: 2, color: md.onSurfaceVariant }}>
            {t('admin:nodes.claim_dialog.subtitle', { id: claimForm.inbound_id })}
          </Typography>
          <Box component="form" id="claim-form" onSubmit={submitClaim} sx={{ display: 'flex', flexDirection: 'column', gap: 2.5 }}>
            <Select required size="small" fullWidth value={claimForm.user_id || ''} displayEmpty
              error={!!claimErr.user_id}
              onChange={e => setClaimForm({ ...claimForm, user_id: Number(e.target.value) })}>
              <MenuItem value="" disabled>{t('admin:nodes.claim_dialog.user')}</MenuItem>
              {claimUsers.map(u => (
                <MenuItem key={u.id} value={u.id}>
                  {u.display_name ? `${u.display_name} (${u.upn})` : u.upn}
                </MenuItem>
              ))}
            </Select>
            <TextField required fullWidth label={t('admin:nodes.claim_dialog.client_email')}
              value={claimForm.client_email}
              onChange={e => setClaimForm({ ...claimForm, client_email: e.target.value })}
              error={!!claimErr.client_email}
              helperText={claimErr.client_email ? t(`admin:${claimErr.client_email}`) : ''} />
            <TextField fullWidth label={t('admin:nodes.claim_dialog.client_uuid')}
              value={claimForm.client_uuid}
              onChange={e => setClaimForm({ ...claimForm, client_uuid: e.target.value })} />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setClaimOpen(false)} disabled={claimBusy} variant="text">{t('common:actions.cancel')}</Button>
          <Button type="submit" form="claim-form" variant="contained" disabled={claimBusy}
            startIcon={claimBusy ? <CircularProgress size={16} color="inherit" /> : null}>
            {t('admin:nodes.claim_dialog.submit')}
          </Button>
        </DialogActions>
      </Dialog>
      {/* Import unmanaged-inbound dialog */}
      <Dialog open={importOpen} onClose={() => !importBusy && setImportOpen(false)}
        slotProps={{
          paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 520, maxWidth: '90vw' } }
        }}>
        <DialogTitle>{t('admin:nodes.import_dialog.title')}</DialogTitle>
        <DialogContent>
          <Typography variant="body2" sx={{ mb: 2 }}>
            {importForm.panel_name && `${importForm.panel_name} · inbound #${importForm.inbound_id}`}
          </Typography>
          <Box component="form" id="import-form" onSubmit={submitImport} sx={{ display: 'flex', flexDirection: 'column', gap: 2.5 }}>
            <TextField required fullWidth label={t('admin:nodes.import_dialog.display_name')}
              value={importForm.display_name}
              onChange={e => setImportForm({ ...importForm, display_name: e.target.value })}
              error={!!importErr.display_name}
              helperText={importErr.display_name ? t(`admin:${importErr.display_name}`) : ''} />
            <TextField required fullWidth label={t('admin:nodes.import_dialog.server_address')}
              value={importForm.server_address}
              onChange={e => setImportForm({ ...importForm, server_address: e.target.value })}
              error={!!importErr.server_address}
              helperText={importErr.server_address ? t(`admin:${importErr.server_address}`) : ''} />
            {/* Flow is a VLESS-only knob (xtls-rprx-vision et al.); hide it for
                SS / VMess / Trojan / Hysteria2 inbounds so it can't be set on a
                protocol that ignores it. */}
            {importForm.protocol === 'vless' && (
              <Box>
                <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.25 }}>
                  {t('admin:nodes.import_dialog.flow')}
                </Typography>
                <Select size="small" fullWidth value={importForm.flow} displayEmpty
                  onChange={e => setImportForm({ ...importForm, flow: e.target.value })}>
                  {VLESS_FLOWS.map(f => <MenuItem key={f} value={f}>{f || '—'}</MenuItem>)}
                </Select>
              </Box>
            )}
            <TextField required fullWidth label={t('admin:nodes.import_dialog.region')}
              value={importForm.region}
              onChange={e => setImportForm({ ...importForm, region: e.target.value })}
              error={!!importErr.region}
              helperText={importErr.region ? t(`admin:${importErr.region}`) : ''} />
            <TagsAutocomplete
              label={t('admin:nodes.import_dialog.tags')}
              value={importForm.tags_text}
              options={allTags}
              onChange={v => setImportForm({ ...importForm, tags_text: v })} />
            <TextField fullWidth type="number" label={t('admin:nodes.import_dialog.sort_order')}
              value={importForm.sort_order}
              onChange={e => setImportForm({ ...importForm, sort_order: Number(e.target.value) })}
              helperText={t('admin:nodes.import_dialog.sort_order_hint', { defaultValue: '0 = 自动排到列表末尾' })} />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setImportOpen(false)} disabled={importBusy} variant="text">{t('common:actions.cancel')}</Button>
          <Button type="submit" form="import-form" variant="contained" disabled={importBusy}
            startIcon={importBusy ? <CircularProgress size={16} color="inherit" /> : null}>
            {t('admin:nodes.import')}
          </Button>
        </DialogActions>
      </Dialog>
      {/* Metadata edit dialog */}
      <Dialog open={editOpen} onClose={() => !editBusy && setEditOpen(false)}
        slotProps={{
          paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 520, maxWidth: '90vw' } }
        }}>
        <DialogTitle>
          {t('admin:nodes.edit_title')} — {editing?.display_name}
        </DialogTitle>
        <DialogContent>
          <Box component="form" id="node-form" onSubmit={submitEdit} sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, pt: 1 }}>
            <TextField required fullWidth label={t('admin:nodes.field.display_name')}
              value={editForm.display_name}
              onChange={e => setEditForm({ ...editForm, display_name: e.target.value })}
              error={!!editMetaErr.display_name}
              helperText={editMetaErr.display_name ? t(`admin:${editMetaErr.display_name}`) : ''} />
            <TextField required fullWidth label={t('admin:nodes.field.server_address')}
              value={editForm.server_address}
              onChange={e => setEditForm({ ...editForm, server_address: e.target.value })}
              error={!!editMetaErr.server_address}
              helperText={editMetaErr.server_address ? t(`admin:${editMetaErr.server_address}`) : ''} />
            {/* Flow is a VLESS inbound-level setting, configured where the
                inbound is created / imported — not a node-meta field. It's
                deliberately NOT editable here; editForm.flow round-trips the
                node's existing value unchanged on save. */}
            <TextField required fullWidth label={t('admin:nodes.field.region')}
              value={editForm.region}
              onChange={e => setEditForm({ ...editForm, region: e.target.value })}
              error={!!editMetaErr.region}
              helperText={editMetaErr.region ? t(`admin:${editMetaErr.region}`) : ''} />
            <TagsAutocomplete
              label={t('admin:nodes.field.tags')}
              value={editForm.tags_text}
              options={allTags}
              onChange={v => setEditForm({ ...editForm, tags_text: v })} />

            {/* Transit / 中转 lines: the same landing offered through extra
                relay fronts. Server-side forwarding (realm/gost/iptables) is
                set up outside the panel; here we only record each front's
                address/port (+ optional CDN SNI/Host). */}
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
              <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                <Typography sx={{ fontSize: 14, fontWeight: 600 }}>{t('admin:nodes.relay.title')}</Typography>
                <Button size="small" startIcon={<AddIcon />}
                  onClick={() => setEditForm(f => ({ ...f, relays: [...f.relays, { name: '', address: '', port: 0, sni: '', host: '', enabled: true }] }))}>
                  {t('admin:nodes.relay.add')}
                </Button>
              </Box>
              <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t('admin:nodes.relay.hint')}</Typography>
              {editForm.relays.length === 0 ? (
                <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, fontStyle: 'italic' }}>{t('admin:nodes.relay.empty')}</Typography>
              ) : editForm.relays.map((r, i) => {
                const update = (patch: Partial<RelayLine>) =>
                  setEditForm(f => ({ ...f, relays: f.relays.map((x, j) => (j === i ? { ...x, ...patch } : x)) }))
                return (
                  <Box key={i} sx={{ border: `1px solid ${md.outlineVariant}`, borderRadius: 2, p: 1.5, display: 'flex', flexDirection: 'column', gap: 1 }}>
                    <Box sx={{ display: 'flex', gap: 1, alignItems: 'center' }}>
                      <TextField size="small" label={t('admin:nodes.relay.name')} value={r.name}
                        onChange={e => update({ name: e.target.value })} sx={{ flex: 1 }}
                        placeholder={t('admin:nodes.relay.name_ph')} />
                      <FormControlLabel sx={{ m: 0, '& .MuiFormControlLabel-label': { ml: 0.75 } }} label={t('admin:nodes.relay.enabled')}
                        control={<Switch size="small" checked={r.enabled} onChange={(_, c) => update({ enabled: c })} />} />
                      <Tooltip title={t('admin:nodes.relay.remove')}>
                        <IconButton size="small" onClick={() => setEditForm(f => ({ ...f, relays: f.relays.filter((_, j) => j !== i) }))}>
                          <DeleteIcon fontSize="small" />
                        </IconButton>
                      </Tooltip>
                    </Box>
                    <Box sx={{ display: 'flex', gap: 1 }}>
                      <TextField size="small" required label={t('admin:nodes.relay.address')} value={r.address}
                        onChange={e => update({ address: e.target.value })} sx={{ flex: 2 }} placeholder="relay.example.com" />
                      <TextField size="small" type="number" label={t('admin:nodes.relay.port')} value={r.port || ''}
                        onChange={e => update({ port: Number(e.target.value) || 0 })} sx={{ width: 120 }}
                        helperText={t('admin:nodes.relay.port_hint')} />
                    </Box>
                    <Box sx={{ display: 'flex', gap: 1 }}>
                      <TextField size="small" label={t('admin:nodes.relay.sni')} value={r.sni ?? ''}
                        onChange={e => update({ sni: e.target.value })} sx={{ flex: 1 }}
                        placeholder={t('admin:nodes.relay.cdn_optional')} />
                      <TextField size="small" label={t('admin:nodes.relay.host')} value={r.host ?? ''}
                        onChange={e => update({ host: e.target.value })} sx={{ flex: 1 }}
                        placeholder={t('admin:nodes.relay.cdn_optional')} />
                    </Box>
                  </Box>
                )
              })}
              {editForm.relays.some(r => r.enabled) && (
                <FormControlLabel sx={{ m: 0, '& .MuiFormControlLabel-label': { ml: 0.75 } }} label={t('admin:nodes.relay.hide_direct')}
                  control={<Switch size="small" checked={editForm.hide_direct} onChange={(_, c) => setEditForm(f => ({ ...f, hide_direct: c }))} />} />
              )}
            </Box>
            <Box sx={{ display: 'none' }}>{alpha(md.error, 0.5)}</Box>
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setEditOpen(false)} disabled={editBusy} variant="text">{t('common:actions.cancel')}</Button>
          <Button type="submit" form="node-form" variant="contained" disabled={editBusy}
            startIcon={editBusy ? <CircularProgress size={16} color="inherit" /> : null}>
            {t('common:actions.ok')}
          </Button>
        </DialogActions>
      </Dialog>
      {/* Separator dialog: layout-only row, persisted to nodes_separator. */}
      <Dialog open={separatorOpen} onClose={() => !separatorBusy && setSeparatorOpen(false)}
        slotProps={{
          paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 520, maxWidth: '90vw' } }
        }}>
        <DialogTitle>
          {separatorEditingId !== null
            ? t('admin:nodes.edit_separator_dialog.title', { defaultValue: '编辑分隔标题' })
            : t('admin:nodes.create_separator_dialog.title', { defaultValue: '新增分隔标题' })}
        </DialogTitle>
        <DialogContent>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, pt: 1 }}>
            <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
              {t('admin:nodes.create_separator_dialog.hint', {
                defaultValue: '分隔标题以 DIRECT 形式出现在客户端的节点列表中，仅用于视觉分组。「全局」=所有分组都显示；「按节点关联」=只有该分组包含至少一个关联节点时才显示。位置由拖拽排序决定。',
              })}
            </Typography>
            <TextField required fullWidth label={t('admin:nodes.field.display_name')}
              value={separatorForm.display_name}
              onChange={e => setSeparatorForm({ ...separatorForm, display_name: e.target.value })}
              placeholder="---- Taiwan HiNet ----" />
            <TextField select fullWidth label={t('admin:nodes.field.separator_mode', { defaultValue: '可见模式' })}
              value={separatorForm.mode}
              onChange={e => setSeparatorForm({ ...separatorForm, mode: e.target.value as SeparatorMode })}
              helperText={separatorForm.mode === 'global'
                ? t('admin:nodes.field.separator_mode_hint_global', { defaultValue: '所有分组都显示此分隔符。' })
                : t('admin:nodes.field.separator_mode_hint_node_bound', { defaultValue: '仅当该分组包含下方关联的任意节点时才显示。位置仍由拖拽排序决定。' })
              }>
              <MenuItem value="global">{t('admin:nodes.field.separator_mode_global', { defaultValue: '全局（所有分组显示）' })}</MenuItem>
              <MenuItem value="node_bound">{t('admin:nodes.field.separator_mode_node_bound', { defaultValue: '按节点关联（节点出现时显示）' })}</MenuItem>
            </TextField>
            {separatorForm.mode === 'node_bound' && (
              <Autocomplete
                multiple
                disableCloseOnSelect
                options={managed}
                getOptionLabel={(n) => n.display_name}
                isOptionEqualToValue={(a, b) => a.id === b.id}
                value={managed.filter(n => separatorForm.node_ids.includes(n.id))}
                onChange={(_, v) => setSeparatorForm({ ...separatorForm, node_ids: (v as Node[]).map(n => n.id) })}
                renderValue={(value, getItemProps) =>
                  value.map((option, index) => {
                    const tagProps = getItemProps({ index })
                    return <Chip {...tagProps} key={option.id} label={option.display_name} size="small" />
                  })
                }
                renderInput={(params) => (
                  <TextField {...params}
                    label={t('admin:nodes.field.separator_node_ids', { defaultValue: '关联节点' })}
                    helperText={t('admin:nodes.field.separator_node_ids_hint', { defaultValue: '订阅渲染时，凡是用户能看到这里任一节点的分组都会显示此分隔符；不填则永远不出现。' })} />
                )}
              />
            )}
            <FormControlLabel
              label={t('admin:nodes.field.enabled', { defaultValue: '启用' })}
              control={
                <Switch checked={separatorForm.enabled}
                  onChange={(_, c) => setSeparatorForm({ ...separatorForm, enabled: c })} />
              }
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }}
            />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setSeparatorOpen(false)} disabled={separatorBusy} variant="text">{t('common:actions.cancel')}</Button>
          <Button onClick={submitSeparator} disabled={separatorBusy} variant="contained"
            startIcon={separatorBusy ? <CircularProgress size={16} color="inherit" /> : null}>
            {t('common:actions.ok')}
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  );
}
