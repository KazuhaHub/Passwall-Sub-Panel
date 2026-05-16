import { useEffect, useState, type FormEvent, type Dispatch, type SetStateAction } from 'react'
import {
  Box,
  Button,
  Card,
  Checkbox,
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
import DeleteIcon from '@mui/icons-material/DeleteOutline'
import EditIcon from '@mui/icons-material/EditOutlined'
import LinkOffIcon from '@mui/icons-material/LinkOff'
import { useTranslation } from 'react-i18next'

import {
  claimClient,
  createInbound,
  deleteNode,
  detachNode,
  generateRealityKeypair,
  getNode,
  importNode,
  listNodes,
  listUnmanagedInbounds,
  setNodeEnabled,
  updateInboundConfig,
  updateNodeMetadata,
} from '@/api/nodes'
import { listUsers } from '@/api/users'
import { listServers, type Server } from '@/api/servers'
import { MenuItem, Select, FormControlLabel } from '@mui/material'
import KeyIcon from '@mui/icons-material/VpnKey'
import type { Node, UnmanagedInbound, User } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { useTabParam } from '@/hooks/useTabParam'
import {
  type FieldErrors,
  firstError,
  validateEmail,
  validateHost,
  validateName,
} from '@/utils/validators'

type CreateProtocol = 'vless' | 'vmess' | 'trojan' | 'ss2022' | 'hysteria2'
type VlessNetwork = 'tcp' | 'ws' | 'grpc' | 'httpupgrade'
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

interface MetaForm {
  display_name: string
  server_address: string
  flow: string
  region: string
  tags_text: string
  sort_order: number
}

interface ImportForm extends MetaForm {
  panel_id: number
  panel_name: string
  inbound_id: number
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
  // TLS
  tls_server_name: string
  tls_alpn_text: string
  tls_min_version: string
  tls_max_version: string
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
  display_name: '', server_address: '', flow: '', region: '', tags_text: '', sort_order: 100,
}

const EMPTY_IMPORT: ImportForm = {
  ...EMPTY_META, panel_id: 0, panel_name: '', inbound_id: 0,
}

const EMPTY_INBOUND: InboundFormState = {
  panel_id: 0,
  display_name: '',
  server_address: '',
  region: '',
  tags_text: '',
  sort_order: 100,
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
  tls_server_name: '',
  tls_alpn_text: 'h2,http/1.1',
  tls_min_version: '',
  tls_max_version: '',
  reality_dest: 'www.tesla.com:443',
  reality_server_names_text: 'www.tesla.com',
  private_key: '',
  public_key: '',
  short_ids_text: '',
  reality_fingerprint: 'chrome',
  reality_spider_x: '/drive',
  reality_xver: 0,
  reality_max_timediff: 0,
  reality_min_client: '',
  reality_max_client: '',
  ss_method: '2022-blake3-aes-256-gcm',
  ss_password: '',
  ss_network: 'tcp,udp',
  ss_iv_check: false,
  hy2_obfs_password: '',
  hy2_udp_idle_timeout: 60,
  hy2_masquerade_type: '',
  hy2_masquerade_content: '',
  sniffing_enabled: true,
  sniffing_dest_override_text: 'http,tls,quic',
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
]
const VLESS_SECURITIES: { value: VlessSecurity; label: string }[] = [
  { value: 'none', label: 'None' },
  { value: 'tls', label: 'TLS' },
  { value: 'reality', label: 'Reality' },
]
const FINGERPRINTS = ['chrome', 'firefox', 'safari', 'ios', 'android', 'edge', '360', 'qq', 'random', 'randomized']
const VLESS_FLOWS = ['', 'xtls-rprx-vision', 'xtls-rprx-vision-udp443']
const TCP_HEADER_TYPES = ['none', 'http']
const TLS_VERSIONS = ['', '1.0', '1.1', '1.2', '1.3']
const SS2022_METHODS: { value: SS2022Method; bytes: number }[] = [
  { value: '2022-blake3-aes-128-gcm', bytes: 16 },
  { value: '2022-blake3-aes-256-gcm', bytes: 32 },
  { value: '2022-blake3-chacha20-poly1305', bytes: 32 },
]

function splitList(value: string): string[] {
  return value.split(/[\n,]/).map(s => s.trim()).filter(Boolean)
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
      tlsSettings: {
        serverName: f.tls_server_name,
        alpn: splitList(f.tls_alpn_text),
        certificates: [],
      },
      hysteriaSettings,
      finalmask,
      externalProxy: [],
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
  }
  if (security === 'tls') {
    stream.tlsSettings = {
      serverName: f.tls_server_name,
      minVersion: f.tls_min_version,
      maxVersion: f.tls_max_version,
      cipherSuites: [],
      alpn: splitList(f.tls_alpn_text),
      certificates: [],
      rejectUnknownSni: false,
      disableSystemRoot: false,
      enableSessionResumption: false,
    }
  } else if (security === 'reality') {
    stream.realitySettings = {
      show: false,
      xver: f.reality_xver,
      dest: f.reality_dest,
      serverNames: splitList(f.reality_server_names_text),
      privateKey: f.private_key,
      minClient: f.reality_min_client,
      maxClient: f.reality_max_client,
      maxTimediff: f.reality_max_timediff,
      shortIds: splitList(f.short_ids_text),
      settings: {
        publicKey: f.public_key,
        fingerprint: f.reality_fingerprint,
        serverName: '',
        spiderX: f.reality_spider_x || '/drive',
      },
    }
  }
  return stream
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
  const reality = (stream.realitySettings as Record<string, unknown>) ?? {}
  const realityInner = (reality.settings as Record<string, unknown>) ?? {}

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
    tls_server_name: stringValue(tls.serverName),
    tls_alpn_text: listToText(tls.alpn) || 'h2,http/1.1',
    tls_min_version: stringValue(tls.minVersion),
    tls_max_version: stringValue(tls.maxVersion),
    reality_dest: stringValue(reality.dest, 'www.tesla.com:443'),
    reality_server_names_text: listToText(reality.serverNames) || 'www.tesla.com',
    private_key: stringValue(reality.privateKey),
    public_key: stringValue(realityInner.publicKey),
    short_ids_text: listToText(reality.shortIds),
    reality_fingerprint: stringValue(realityInner.fingerprint, 'chrome'),
    reality_spider_x: stringValue(realityInner.spiderX, '/drive'),
    reality_xver: numberValue(reality.xver),
    reality_max_timediff: numberValue(reality.maxTimediff),
    reality_min_client: stringValue(reality.minClient),
    reality_max_client: stringValue(reality.maxClient),
    ss_method: (stringValue(settings.method, '2022-blake3-aes-256-gcm') as SS2022Method),
    ss_password: stringValue(settings.password),
    ss_network: stringValue(settings.network, 'tcp,udp'),
    ss_iv_check: boolValue(settings.ivCheck),
    hy2_obfs_password: stringValue(salamanderSettings.password),
    hy2_udp_idle_timeout: numberValue(hysteriaSettings.udpIdleTimeout, 60),
    hy2_masquerade_type: masqueradeType,
    hy2_masquerade_content: masqueradeContent,
    sniffing_enabled: boolValue(sniffing.enabled, true),
    sniffing_dest_override_text: listToText(sniffing.destOverride) || 'http,tls,quic',
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
}

function InboundFormFields({ form, setForm, showMetadata, servers, onGenKeys, onGenSSPassword, genKeysBusy, protocolReadonly, advanced, onSetAdvanced }: FieldsProps) {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])

  const update = <K extends keyof InboundFormState>(key: K, value: InboundFormState[K]) => {
    setForm(prev => ({ ...prev, [key]: value }))
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

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.75 }}>
      {/* Target + protocol + listening (combined header). The advanced
          toggle lives on the right so it's visible regardless of which
          mode we're in. */}
      <Box>
        <Box sx={{ display: 'flex', alignItems: 'center', mb: 0.75 }}>
          <Box sx={{ flex: 1 }}>{sectionTitle(t('admin:nodes.create_dialog.section_inbound'))}</Box>
          {onSetAdvanced && (
            <FormControlLabel
              label={t('admin:nodes.create_dialog.advanced', { defaultValue: '高级 (JSON)' })}
              control={<Switch size="small" checked={!!advanced}
                onChange={(_, c) => toggleAdvanced(c)} />}
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1, fontSize: 13 } }}
            />
          )}
        </Box>
        <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
          {servers && (
            <Box sx={{ flex: '2 1 240px', minWidth: 200 }}>
              {fieldLabel(t('admin:nodes.create_dialog.panel'))}
              <Select size="small" fullWidth value={form.panel_id}
                onChange={e => update('panel_id', Number(e.target.value))}>
                {servers.map(s => <MenuItem key={s.id} value={s.id}>{s.name}</MenuItem>)}
              </Select>
            </Box>
          )}
          <Box sx={{ flex: '1 1 160px', minWidth: 140 }}>
            {fieldLabel(t('admin:nodes.create_dialog.protocol'))}
            <Select size="small" fullWidth value={form.protocol}
              disabled={protocolReadonly || !!advanced}
              onChange={e => update('protocol', e.target.value as CreateProtocol)}>
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
            {sectionTitle(t('admin:nodes.create_dialog.section_vless'))}
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
                    // VMess doesn't speak REALITY (client-side support never
                    // landed), so hide it to prevent invalid combinations.
                    .filter(o => !(form.protocol === 'vmess' && o.value === 'reality'))
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

          {/* Security-specific */}
          {form.vless_security === 'tls' && (
            <Box>
              {sectionTitle(t('admin:nodes.create_dialog.section_tls'))}
              <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
                <TextField size="small" label={t('admin:nodes.create_dialog.tls_server_name')}
                  value={form.tls_server_name}
                  onChange={e => update('tls_server_name', e.target.value)}
                  sx={{ flex: '2 1 240px' }} />
                <TextField size="small" label={t('admin:nodes.create_dialog.tls_alpn')}
                  value={form.tls_alpn_text}
                  onChange={e => update('tls_alpn_text', e.target.value)}
                  sx={{ flex: '2 1 200px' }} />
                <Box sx={{ flex: '1 1 110px', minWidth: 100 }}>
                  {fieldLabel(t('admin:nodes.create_dialog.tls_min_version'))}
                  <Select size="small" fullWidth value={form.tls_min_version}
                    onChange={e => update('tls_min_version', e.target.value)} displayEmpty>
                    {TLS_VERSIONS.map(v => <MenuItem key={v} value={v}>{v || '—'}</MenuItem>)}
                  </Select>
                </Box>
                <Box sx={{ flex: '1 1 110px', minWidth: 100 }}>
                  {fieldLabel(t('admin:nodes.create_dialog.tls_max_version'))}
                  <Select size="small" fullWidth value={form.tls_max_version}
                    onChange={e => update('tls_max_version', e.target.value)} displayEmpty>
                    {TLS_VERSIONS.map(v => <MenuItem key={v} value={v}>{v || '—'}</MenuItem>)}
                  </Select>
                </Box>
              </Box>
            </Box>
          )}

          {form.protocol === 'vless' && form.vless_security === 'reality' && (
            <Box>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                <Box sx={{ flex: 1 }}>{sectionTitle(t('admin:nodes.create_dialog.section_reality'))}</Box>
                <Button size="small" variant="outlined" onClick={() => onGenKeys()} disabled={genKeysBusy}
                  startIcon={genKeysBusy ? <CircularProgress size={14} /> : <KeyIcon />}
                  sx={{ mb: 0.75 }}>
                  {t('admin:nodes.create_dialog.gen_keys')}
                </Button>
              </Box>
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.25 }}>
                <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
                  <TextField required size="small" label={t('admin:nodes.create_dialog.reality_dest')}
                    value={form.reality_dest}
                    onChange={e => update('reality_dest', e.target.value)}
                    sx={{ flex: '1 1 240px' }} />
                  <TextField required size="small" label={t('admin:nodes.create_dialog.reality_server_names')}
                    value={form.reality_server_names_text}
                    onChange={e => update('reality_server_names_text', e.target.value)}
                    sx={{ flex: '1 1 240px' }} />
                </Box>
                <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
                  <Box sx={{ flex: '1 1 180px', minWidth: 140 }}>
                    {fieldLabel(t('admin:nodes.create_dialog.reality_fingerprint'))}
                    <Select size="small" fullWidth value={form.reality_fingerprint}
                      onChange={e => update('reality_fingerprint', e.target.value)}>
                      {FINGERPRINTS.map(fp => <MenuItem key={fp} value={fp}>{fp}</MenuItem>)}
                    </Select>
                  </Box>
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
                  <MenuItem value="proxy">proxy (反代到 URL)</MenuItem>
                  <MenuItem value="file">file (返回静态目录)</MenuItem>
                  <MenuItem value="string">string (返回固定内容)</MenuItem>
                </Select>
              </Box>
              <Box sx={{ flex: '2 1 280px' }}>
                {fieldLabel(
                  form.hy2_masquerade_type === 'proxy' ? 'Upstream URL'
                  : form.hy2_masquerade_type === 'file' ? 'Directory'
                  : form.hy2_masquerade_type === 'string' ? 'Response body'
                  : t('admin:nodes.create_dialog.hy2_masquerade_content', { defaultValue: '内容 / URL / 目录' })
                )}
                <TextField size="small" fullWidth
                  value={form.hy2_masquerade_content}
                  onChange={e => update('hy2_masquerade_content', e.target.value)}
                  disabled={!form.hy2_masquerade_type} />
              </Box>
            </Box>
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
              {t('admin:nodes.create_dialog.hy2_cert_hint', {
                defaultValue: '证书 / 私钥请在 3X-UI 面板侧配置；或切换"高级 (JSON)"在 streamSettings.tlsSettings.certificates 中粘贴。',
              })}
            </Typography>
          </Box>
        </Box>
      )}

      {!advanced && form.protocol === 'ss2022' && (
        <Box>
          {sectionTitle(t('admin:nodes.create_dialog.section_ss2022'))}
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.25 }}>
            <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
              <Box sx={{ flex: '2 1 280px' }}>
                {fieldLabel(t('admin:nodes.create_dialog.ss_method'))}
                <Select size="small" fullWidth value={form.ss_method}
                  onChange={e => update('ss_method', e.target.value as SS2022Method)}>
                  {SS2022_METHODS.map(m => <MenuItem key={m.value} value={m.value}>{m.value}</MenuItem>)}
                </Select>
              </Box>
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
              <TextField size="small" label={t('admin:nodes.field.tags')}
                value={form.tags_text}
                onChange={e => update('tags_text', e.target.value)}
                sx={{ flex: '2 1 240px' }} />
              <TextField size="small" type="number" label={t('admin:nodes.field.sort_order')}
                value={form.sort_order}
                onChange={e => update('sort_order', Number(e.target.value))}
                sx={{ width: 110 }} />
            </Box>
          </Box>
        </Box>
      )}
    </Box>
  )
}

export default function NodesView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])

  const [tab, setTab] = useTabParam<'managed' | 'unmanaged'>('tab', 'managed', ['managed', 'unmanaged'])
  const [managed, setManaged] = useState<Node[]>([])
  const [unmanaged, setUnmanaged] = useState<UnmanagedInbound[]>([])
  const [loading, setLoading] = useState(false)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [batchBusy, setBatchBusy] = useState<'enable' | 'disable' | 'delete' | ''>('')
  const [enabledBusy, setEnabledBusy] = useState<Record<number, boolean>>({})

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
    try { setServers(await listServers()) }
    catch { /* toast */ }
  }

  async function load() {
    setLoading(true)
    try {
      if (tab === 'managed') {
        setManaged(await listNodes())
        setSelected(new Set())
      } else {
        const res = await listUnmanagedInbounds()
        setUnmanaged(res.items)
      }
    } finally {
      setLoading(false)
    }
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
    await deleteNode(n.id)
    setManaged(prev => prev.filter(x => x.id !== n.id))
    pushSnack(t('admin:nodes.toast.deleted'), 'success')
  }

  // Detach: stop managing the node but keep the upstream inbound (and any
  // non-panel clients) untouched. Useful for inbounds shared with users
  // outside the panel — admin reclaims their inbound without nuking it.
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
    const checkedAt = n.health_checked_at ? new Date(n.health_checked_at).toLocaleString() : t('admin:nodes.health.never', { defaultValue: '尚未运行' })
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

  function openCreate() {
    if (servers.length === 0) {
      pushSnack(t('admin:nodes.create_dialog.no_servers'), 'warning')
      return
    }
    setCreateForm({ ...EMPTY_INBOUND, panel_id: servers[0].id })
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
      display_name: u.Remark || `${u.Protocol}:${u.Port}`,
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
        flow: importForm.flow || undefined,
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
      <Box sx={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', flexWrap: 'wrap', gap: 2, mb: 1 }}>
        <Box>
          <Typography variant="h4">{t('admin:nodes.title')}</Typography>
          <Typography variant="body2" sx={{ mt: 0.5 }}>{t('admin:nodes.subtitle')}</Typography>
        </Box>
        <Button variant="contained" startIcon={<AddIcon />} onClick={openCreate}>
          {t('admin:nodes.create')}
        </Button>
      </Box>

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
          <Button size="small" variant="text" color="error"
            disabled={batchBusy !== ''}
            startIcon={batchBusy === 'delete' ? <CircularProgress size={14} /> : <DeleteIcon />}
            onClick={batchDelete}>
            {t('admin:nodes.batch_delete')}
          </Button>
        </Box>
      )}

      <Card sx={{ bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
        {tab === 'managed' && (
          <TableContainer>
            <Table>
              <TableHead>
                <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
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
                  <TableCell align="right">{t('admin:nodes.table.sort_order')}</TableCell>
                  <TableCell align="center">{t('admin:nodes.table.health', { defaultValue: '健康' })}</TableCell>
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
                {!loading && managed.length === 0 && (
                  <TableRow><TableCell colSpan={11} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>—</TableCell></TableRow>
                )}
                {managed.map(n => (
                  <TableRow key={n.id} hover sx={{
                    '& td': { borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' },
                    opacity: n.enabled ? 1 : 0.65,
                  }}>
                    <TableCell padding="checkbox">
                      <Checkbox checked={selected.has(n.id)} onChange={(_, c) => toggleOne(n.id, c)} />
                    </TableCell>
                    <TableCell sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{n.id}</TableCell>
                    <TableCell sx={{ fontWeight: 500 }}>{n.display_name}</TableCell>
                    <TableCell sx={{ fontSize: 13 }}>{n.panel_name}</TableCell>
                    <TableCell sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{n.server_address}</TableCell>
                    <TableCell sx={{ fontSize: 13 }}>{n.region}</TableCell>
                    <TableCell>{tagsCell(n.tags)}</TableCell>
                    <TableCell align="right" sx={{ fontVariantNumeric: 'tabular-nums' }}>{n.sort_order}</TableCell>
                    <TableCell align="center">{healthDot(n)}</TableCell>
                    <TableCell align="center">
                      <Switch checked={n.enabled} onChange={() => toggleEnabled(n)} disabled={enabledBusy[n.id]} />
                    </TableCell>
                    <TableCell align="right">
                      <Tooltip title={t('admin:nodes.action.edit')}>
                        <IconButton size="small" onClick={() => openEdit(n)}><EditIcon fontSize="small" /></IconButton>
                      </Tooltip>
                      <Tooltip title={t('admin:nodes.edit_inbound')}>
                        <IconButton size="small" onClick={() => openEditInbound(n)}>
                          <KeyIcon fontSize="small" />
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
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableContainer>
        )}

        {tab === 'unmanaged' && (
          <TableContainer>
            <Table>
              <TableHead>
                <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                  <TableCell>{t('admin:nodes.table.panel_name')}</TableCell>
                  <TableCell>Inbound ID</TableCell>
                  <TableCell>Protocol</TableCell>
                  <TableCell align="right">Port</TableCell>
                  <TableCell>Remark</TableCell>
                  <TableCell align="right">Clients</TableCell>
                  <TableCell align="right">{t('admin:nodes.table.actions')}</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {loading && unmanaged.length === 0 && (
                  <TableRow><TableCell colSpan={7} sx={{ textAlign: 'center', py: 6 }}>
                    <CircularProgress size={24} />
                  </TableCell></TableRow>
                )}
                {!loading && unmanaged.length === 0 && (
                  <TableRow><TableCell colSpan={7} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>—</TableCell></TableRow>
                )}
                {unmanaged.map((u, idx) => (
                  <TableRow key={`${u.PanelID}-${u.InboundID}-${idx}`} hover sx={{ '& td': { borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                    <TableCell sx={{ fontWeight: 500 }}>{u.PanelName}</TableCell>
                    <TableCell sx={{ fontSize: 13 }}>{u.InboundID}</TableCell>
                    <TableCell sx={{ fontSize: 13 }}>{u.Protocol}</TableCell>
                    <TableCell align="right" sx={{ fontSize: 13 }}>{u.Port}</TableCell>
                    <TableCell sx={{ fontSize: 13 }}>{u.Remark}</TableCell>
                    <TableCell align="right" sx={{ fontVariantNumeric: 'tabular-nums' }}>{u.ClientCount}</TableCell>
                    <TableCell align="right" sx={{ whiteSpace: 'nowrap' }}>
                      {u.ClientCount > 0 && (
                        <Button size="small" variant="text" onClick={() => startClaim(u)} sx={{ mr: 1 }}>
                          {t('admin:nodes.claim')}
                        </Button>
                      )}
                      <Button size="small" variant="outlined" onClick={() => startImport(u)}>
                        {t('admin:nodes.import')}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableContainer>
        )}
      </Card>

      {/* Create inbound dialog (multi-protocol) */}
      <Dialog open={createOpen} onClose={() => !createBusy && setCreateOpen(false)}
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 800, maxWidth: '95vw' } }}>
        <DialogTitle sx={{ pt: 2.5, pb: 1, fontSize: 18 }}>
          {t('admin:nodes.create_dialog.title_dynamic', {
            protocol: createForm.protocol === 'ss2022' ? 'SS-2022' : 'VLESS',
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
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 800, maxWidth: '95vw' } }}>
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
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 520, maxWidth: '90vw' } }}>
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
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 520, maxWidth: '90vw' } }}>
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
            <Box>
              <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.25 }}>
                {t('admin:nodes.import_dialog.flow')}
              </Typography>
              <Select size="small" fullWidth value={importForm.flow} displayEmpty
                onChange={e => setImportForm({ ...importForm, flow: e.target.value })}>
                {VLESS_FLOWS.map(f => <MenuItem key={f} value={f}>{f || '—'}</MenuItem>)}
              </Select>
            </Box>
            <TextField required fullWidth label={t('admin:nodes.import_dialog.region')}
              value={importForm.region}
              onChange={e => setImportForm({ ...importForm, region: e.target.value })}
              error={!!importErr.region}
              helperText={importErr.region ? t(`admin:${importErr.region}`) : ''} />
            <TextField fullWidth label={t('admin:nodes.import_dialog.tags')}
              value={importForm.tags_text}
              onChange={e => setImportForm({ ...importForm, tags_text: e.target.value })} />
            <TextField fullWidth type="number" label={t('admin:nodes.import_dialog.sort_order')}
              value={importForm.sort_order}
              onChange={e => setImportForm({ ...importForm, sort_order: Number(e.target.value) })} />
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
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 520, maxWidth: '90vw' } }}>
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
            <Box>
              <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.25 }}>
                {t('admin:nodes.field.flow')}
              </Typography>
              <Select size="small" fullWidth value={editForm.flow} displayEmpty
                onChange={e => setEditForm({ ...editForm, flow: e.target.value })}>
                {VLESS_FLOWS.map(f => <MenuItem key={f} value={f}>{f || '—'}</MenuItem>)}
              </Select>
            </Box>
            <TextField required fullWidth label={t('admin:nodes.field.region')}
              value={editForm.region}
              onChange={e => setEditForm({ ...editForm, region: e.target.value })}
              error={!!editMetaErr.region}
              helperText={editMetaErr.region ? t(`admin:${editMetaErr.region}`) : ''} />
            <TextField fullWidth label={t('admin:nodes.field.tags')}
              value={editForm.tags_text}
              onChange={e => setEditForm({ ...editForm, tags_text: e.target.value })} />
            <TextField fullWidth type="number" label={t('admin:nodes.field.sort_order')}
              value={editForm.sort_order}
              onChange={e => setEditForm({ ...editForm, sort_order: Number(e.target.value) })} />
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
    </Box>
  )
}
