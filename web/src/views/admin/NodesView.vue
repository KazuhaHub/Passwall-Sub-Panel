<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Delete, Lock, Unlock } from '@element-plus/icons-vue'
import {
  createInbound,
  deleteNode,
  generateRealityKeypair,
  getNode,
  importNode,
  listNodes,
  listUnmanagedInbounds,
  setNodeEnabled,
  updateInboundConfig,
  updateNodeMetadata,
} from '@/api/nodes'
import { listServers, type Server } from '@/api/servers'
import type { Node, UnmanagedInbound } from '@/api/types'

const tab = ref<'managed' | 'unmanaged'>('managed')
const managed = ref<Node[]>([])
const unmanaged = ref<UnmanagedInbound[]>([])
const servers = ref<Server[]>([])
const loading = ref(false)
const enabledBusy = ref<Record<number, boolean>>({})
const selectedManaged = ref<Node[]>([])
const batchBusy = ref<'enable' | 'disable' | 'delete' | ''>('')
const selectedManagedCount = computed(() => selectedManaged.value.length)
type CreateProtocol = 'vless' | 'ss2022'
type VlessNetwork = 'tcp' | 'ws' | 'grpc'
type VlessSecurity = 'none' | 'tls' | 'reality'
type SS2022Method = '2022-blake3-aes-128-gcm' | '2022-blake3-aes-256-gcm' | '2022-blake3-chacha20-poly1305'
type ConfigMode = 'visual' | 'custom'

const vlessNetworkOptions = [
  { label: 'TCP', value: 'tcp' },
  { label: 'WebSocket', value: 'ws' },
  { label: 'gRPC', value: 'grpc' },
] as const
const vlessSecurityOptions = [
  { label: 'None', value: 'none' },
  { label: 'TLS', value: 'tls' },
  { label: 'Reality', value: 'reality' },
] as const
const vlessFlowOptions = [
  { label: 'none', value: '' },
  { label: 'xtls-rprx-vision', value: 'xtls-rprx-vision' },
  { label: 'xtls-rprx-vision-udp443', value: 'xtls-rprx-vision-udp443' },
] as const
const ss2022Methods = [
  { label: '2022-blake3-aes-128-gcm', value: '2022-blake3-aes-128-gcm', bytes: 16 },
  { label: '2022-blake3-aes-256-gcm', value: '2022-blake3-aes-256-gcm', bytes: 32 },
  { label: '2022-blake3-chacha20-poly1305', value: '2022-blake3-chacha20-poly1305', bytes: 32 },
] as const
const fingerprintOptions = ['chrome', 'firefox', 'safari', 'ios', 'android', 'edge', '360', 'qq', 'random', 'randomized']

async function loadServers() {
  try {
    servers.value = await listServers()
  } catch {
    servers.value = []
  }
}

const importDialog = ref(false)
const importBusy = ref(false)
const importForm = reactive({
  panel_id: 0,
  panel_name: '',
  inbound_id: 0,
  display_name: '',
  server_address: '',
  flow: '',
  region: '',
  tags_text: '',
  sort_order: 100,
})

const editDialog = ref(false)
const editBusy = ref(false)
const editing = ref<Node | null>(null)
const editMode = ref<ConfigMode>('visual')
const editForm = reactive({
  display_name: '',
  server_address: '',
  flow: '',
  region: '',
  tags_text: '',
  sort_order: 0,
})
const inboundEditLoaded = ref(false)
const inboundEditError = ref('')
const inboundEditForm = reactive({
  remark: '',
  enable: true,
  listen: '',
  port: 443,
  protocol: '',
  settings: '',
  stream_settings: '',
  sniffing: '',
  allocate: '',
  expiry_time: 0,
})
const editVisual = reactive({
  protocol: 'vless' as CreateProtocol,
  vless_flow: 'xtls-rprx-vision',
  vless_encryption: 'none',
  vless_network: 'tcp' as VlessNetwork,
  vless_security: 'reality' as VlessSecurity,
  tcp_accept_proxy_protocol: false,
  tcp_header_type: 'none',
  ws_accept_proxy_protocol: false,
  ws_path: '/',
  ws_host: '',
  grpc_service_name: '',
  grpc_authority: '',
  grpc_multi_mode: false,
  tls_server_name: '',
  tls_alpn_text: 'h2,http/1.1',
  tls_min_version: '',
  tls_max_version: '',
  reality_dest: '',
  reality_server_names_text: '',
  private_key: '',
  public_key: '',
  short_ids_text: '',
  reality_fingerprint: 'chrome',
  reality_spider_x: '/',
  reality_xver: 0,
  reality_max_timediff: 0,
  reality_min_client: '',
  reality_max_client: '',
  ss_method: '2022-blake3-aes-256-gcm' as SS2022Method,
  ss_password: '',
  ss_network: 'tcp,udp',
  sniffing_enabled: true,
  sniffing_dest_override_text: 'http,tls,quic',
  sniffing_metadata_only: false,
  sniffing_route_only: false,
})

const createDialog = ref(false)
const createBusy = ref(false)
const createMode = ref<ConfigMode>('visual')
const createDialogTitle = computed(() => `新增 inbound (${createForm.protocol === 'ss2022' ? 'SS-2022' : 'VLESS'})`)
const createForm = reactive({
  protocol: 'vless' as CreateProtocol,
  enable: true,
  panel_id: 0,
  display_name: '',
  server_address: '',
  region: '',
  tags_text: '',
  sort_order: 100,
  listen: '',
  port: 443,
  expiry_time: 0,
  vless_flow: 'xtls-rprx-vision',
  vless_encryption: 'none',
  vless_network: 'tcp' as VlessNetwork,
  vless_security: 'reality' as VlessSecurity,
  tcp_accept_proxy_protocol: false,
  tcp_header_type: 'none',
  ws_accept_proxy_protocol: false,
  ws_path: '/',
  ws_host: '',
  grpc_service_name: '',
  grpc_authority: '',
  grpc_multi_mode: false,
  tls_server_name: '',
  tls_alpn_text: 'h2,http/1.1',
  tls_min_version: '',
  tls_max_version: '',
  reality_dest: 'yahoo.com:443',
  reality_server_names_text: 'yahoo.com',
  private_key: '',
  public_key: '',
  short_ids_text: '',
  reality_fingerprint: 'chrome',
  reality_spider_x: '/',
  reality_xver: 0,
  reality_max_timediff: 0,
  reality_min_client: '',
  reality_max_client: '',
  ss_method: '2022-blake3-aes-256-gcm' as SS2022Method,
  ss_password: '',
  ss_network: 'tcp,udp',
  sniffing_enabled: true,
  sniffing_dest_override_text: 'http,tls,quic',
  sniffing_metadata_only: false,
  sniffing_route_only: false,
  advanced_enabled: false,
  settings_json: '',
  stream_settings_json: '',
  sniffing_json: '',
  allocate_json: '',
})

function openCreateCheckServers() {
  if (servers.value.length === 0) {
    ElMessage.warning('请先到「服务器」页面添加 3X-UI 服务器')
    return
  }
  openCreate()
}

function openCreate() {
  createMode.value = 'visual'
  createForm.protocol = 'vless'
  createForm.enable = true
  createForm.panel_id = servers.value[0]?.id ?? 0
  createForm.display_name = ''
  createForm.server_address = ''
  createForm.region = ''
  createForm.tags_text = 'reality'
  createForm.sort_order = 100
  createForm.listen = ''
  createForm.port = 443
  createForm.expiry_time = 0
  createForm.vless_flow = 'xtls-rprx-vision'
  createForm.vless_encryption = 'none'
  createForm.vless_network = 'tcp'
  createForm.vless_security = 'reality'
  createForm.tcp_accept_proxy_protocol = false
  createForm.tcp_header_type = 'none'
  createForm.ws_accept_proxy_protocol = false
  createForm.ws_path = '/'
  createForm.ws_host = ''
  createForm.grpc_service_name = ''
  createForm.grpc_authority = ''
  createForm.grpc_multi_mode = false
  createForm.tls_server_name = ''
  createForm.tls_alpn_text = 'h2,http/1.1'
  createForm.tls_min_version = ''
  createForm.tls_max_version = ''
  createForm.reality_dest = 'yahoo.com:443'
  createForm.reality_server_names_text = 'yahoo.com'
  createForm.private_key = ''
  createForm.public_key = ''
  createForm.short_ids_text = ''
  createForm.reality_fingerprint = 'chrome'
  createForm.reality_spider_x = '/'
  createForm.reality_xver = 0
  createForm.reality_max_timediff = 0
  createForm.reality_min_client = ''
  createForm.reality_max_client = ''
  createForm.ss_method = '2022-blake3-aes-256-gcm'
  createForm.ss_password = ''
  createForm.ss_network = 'tcp,udp'
  createForm.sniffing_enabled = true
  createForm.sniffing_dest_override_text = 'http,tls,quic'
  createForm.sniffing_metadata_only = false
  createForm.sniffing_route_only = false
  createForm.advanced_enabled = false
  createForm.settings_json = ''
  createForm.stream_settings_json = ''
  createForm.sniffing_json = ''
  createForm.allocate_json = ''
  createDialog.value = true
}

async function genKeys() {
  const kp = await generateRealityKeypair()
  createForm.private_key = kp.private_key
  createForm.public_key = kp.public_key
  createForm.short_ids_text = kp.short_id
  ElMessage.success('Reality 密钥已生成')
}

async function genEditKeys() {
  const kp = await generateRealityKeypair()
  editVisual.private_key = kp.private_key
  editVisual.public_key = kp.public_key
  editVisual.short_ids_text = kp.short_id
  ElMessage.success('Reality 密钥已生成')
}

function randomBase64(byteLength: number) {
  const bytes = new Uint8Array(byteLength)
  crypto.getRandomValues(bytes)
  let binary = ''
  bytes.forEach((b) => {
    binary += String.fromCharCode(b)
  })
  return btoa(binary)
}

function generateSSPassword() {
  const method = ss2022Methods.find((m) => m.value === createForm.ss_method)
  createForm.ss_password = randomBase64(method?.bytes ?? 32)
  ElMessage.success('SS-2022 服务端 PSK 已生成')
}

function generateEditSSPassword() {
  const method = ss2022Methods.find((m) => m.value === editVisual.ss_method)
  editVisual.ss_password = randomBase64(method?.bytes ?? 32)
  ElMessage.success('SS-2022 服务端 PSK 已生成')
}

watch(
  () => createForm.vless_security,
  (security) => {
    if (security === 'reality' && !createForm.vless_flow) {
      createForm.vless_flow = 'xtls-rprx-vision'
    } else if (security !== 'reality' && createForm.vless_flow === 'xtls-rprx-vision') {
      createForm.vless_flow = ''
    }
  },
)

watch(
  () => editVisual.vless_security,
  (security) => {
    if (security === 'reality' && !editVisual.vless_flow) {
      editVisual.vless_flow = 'xtls-rprx-vision'
    } else if (security !== 'reality' && editVisual.vless_flow === 'xtls-rprx-vision') {
      editVisual.vless_flow = ''
    }
  },
)

watch(createMode, (mode) => {
  if (mode === 'custom') {
    refreshAdvancedJSON()
  }
})

watch(editMode, (mode) => {
  if (!inboundEditLoaded.value) return
  if (mode === 'custom') {
    refreshEditJSON()
  } else {
    loadEditVisualFromJSON()
  }
})

function splitList(value: string) {
  return value
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function compactJSON(value: unknown) {
  return JSON.stringify(value)
}

function jsonOrGenerated(raw: string, label: string, generated: unknown) {
  if (createMode.value !== 'custom' || !raw.trim()) {
    return compactJSON(generated)
  }
  try {
    return compactJSON(JSON.parse(raw))
  } catch (e: any) {
    throw new Error(`${label} JSON 格式错误：${e?.message ?? e}`)
  }
}

function prettifyJSON(raw: string) {
  if (!raw.trim()) return ''
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

function compactEditorJSON(raw: string, label: string) {
  if (!raw.trim()) return ''
  try {
    return compactJSON(JSON.parse(raw))
  } catch (e: any) {
    throw new Error(`${label} JSON 格式错误：${e?.message ?? e}`)
  }
}

function parseJSONForEdit(raw: string) {
  if (!raw.trim()) return {}
  try {
    return JSON.parse(raw)
  } catch {
    return {}
  }
}

function listToText(value: unknown) {
  return Array.isArray(value) ? value.filter((item) => item !== '').join(',') : ''
}

function boolValue(value: unknown, fallback = false) {
  return typeof value === 'boolean' ? value : fallback
}

function numberValue(value: unknown, fallback = 0) {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

function stringValue(value: unknown, fallback = '') {
  return typeof value === 'string' ? value : fallback
}

function buildVlessSettings() {
  return {
    clients: [],
    decryption: createForm.vless_encryption || 'none',
    fallbacks: [],
  }
}

function buildSS2022Settings() {
  return {
    method: createForm.ss_method,
    password: createForm.ss_password,
    network: createForm.ss_network,
    clients: [],
  }
}

function buildStreamSettings() {
  if (createForm.protocol === 'ss2022') {
    return {
      network: 'tcp',
      security: 'none',
      externalProxy: [],
      tcpSettings: {
        acceptProxyProtocol: false,
        header: { type: 'none' },
      },
    }
  }
  const stream: Record<string, any> = {
    network: createForm.vless_network,
    security: createForm.vless_security,
    externalProxy: [],
  }
  if (createForm.vless_network === 'tcp') {
    stream.tcpSettings = {
      acceptProxyProtocol: createForm.tcp_accept_proxy_protocol,
      header: { type: createForm.tcp_header_type },
    }
  } else if (createForm.vless_network === 'ws') {
    stream.wsSettings = {
      acceptProxyProtocol: createForm.ws_accept_proxy_protocol,
      path: createForm.ws_path || '/',
      host: createForm.ws_host,
      headers: createForm.ws_host ? { Host: createForm.ws_host } : {},
      heartbeatPeriod: 0,
    }
  } else if (createForm.vless_network === 'grpc') {
    stream.grpcSettings = {
      serviceName: createForm.grpc_service_name,
      authority: createForm.grpc_authority,
      multiMode: createForm.grpc_multi_mode,
    }
  }
  if (createForm.vless_security === 'tls') {
    stream.tlsSettings = {
      serverName: createForm.tls_server_name,
      minVersion: createForm.tls_min_version,
      maxVersion: createForm.tls_max_version,
      cipherSuites: [],
      alpn: splitList(createForm.tls_alpn_text),
      certificates: [],
      rejectUnknownSni: false,
      disableSystemRoot: false,
      enableSessionResumption: false,
    }
  } else if (createForm.vless_security === 'reality') {
    stream.realitySettings = {
      show: false,
      xver: createForm.reality_xver,
      dest: createForm.reality_dest,
      serverNames: splitList(createForm.reality_server_names_text),
      privateKey: createForm.private_key,
      minClient: createForm.reality_min_client,
      maxClient: createForm.reality_max_client,
      maxTimediff: createForm.reality_max_timediff,
      shortIds: splitList(createForm.short_ids_text),
      settings: {
        publicKey: createForm.public_key,
        fingerprint: createForm.reality_fingerprint,
        serverName: '',
        spiderX: createForm.reality_spider_x || '/',
      },
    }
  }
  return stream
}

function buildSniffing() {
  return {
    enabled: createForm.sniffing_enabled,
    destOverride: splitList(createForm.sniffing_dest_override_text),
    metadataOnly: createForm.sniffing_metadata_only,
    routeOnly: createForm.sniffing_route_only,
  }
}

function refreshAdvancedJSON() {
  createForm.settings_json = JSON.stringify(
    createForm.protocol === 'ss2022' ? buildSS2022Settings() : buildVlessSettings(),
    null,
    2,
  )
  createForm.stream_settings_json = JSON.stringify(buildStreamSettings(), null, 2)
  createForm.sniffing_json = JSON.stringify(buildSniffing(), null, 2)
  createForm.allocate_json = ''
}

function loadEditVisualFromJSON() {
  const settings: any = parseJSONForEdit(inboundEditForm.settings)
  const stream: any = parseJSONForEdit(inboundEditForm.stream_settings)
  const sniffing: any = parseJSONForEdit(inboundEditForm.sniffing)
  editVisual.protocol =
    inboundEditForm.protocol === 'shadowsocks' && String(settings.method ?? '').startsWith('2022-') ? 'ss2022' : 'vless'
  editVisual.vless_encryption = stringValue(settings.decryption, 'none')
  editVisual.vless_network = stringValue(stream.network, 'tcp') as VlessNetwork
  editVisual.vless_security = stringValue(stream.security, 'none') as VlessSecurity
  editVisual.tcp_accept_proxy_protocol = boolValue(stream.tcpSettings?.acceptProxyProtocol)
  editVisual.tcp_header_type = stringValue(stream.tcpSettings?.header?.type, 'none')
  editVisual.ws_accept_proxy_protocol = boolValue(stream.wsSettings?.acceptProxyProtocol)
  editVisual.ws_path = stringValue(stream.wsSettings?.path, '/')
  editVisual.ws_host = stringValue(stream.wsSettings?.host || stream.wsSettings?.headers?.Host, '')
  editVisual.grpc_service_name = stringValue(stream.grpcSettings?.serviceName, '')
  editVisual.grpc_authority = stringValue(stream.grpcSettings?.authority, '')
  editVisual.grpc_multi_mode = boolValue(stream.grpcSettings?.multiMode)
  editVisual.tls_server_name = stringValue(stream.tlsSettings?.serverName, '')
  editVisual.tls_alpn_text = listToText(stream.tlsSettings?.alpn) || 'h2,http/1.1'
  editVisual.tls_min_version = stringValue(stream.tlsSettings?.minVersion, '')
  editVisual.tls_max_version = stringValue(stream.tlsSettings?.maxVersion, '')
  editVisual.reality_dest = stringValue(stream.realitySettings?.dest, '')
  editVisual.reality_server_names_text = listToText(stream.realitySettings?.serverNames)
  editVisual.private_key = stringValue(stream.realitySettings?.privateKey, '')
  editVisual.public_key = stringValue(stream.realitySettings?.settings?.publicKey, '')
  editVisual.short_ids_text = listToText(stream.realitySettings?.shortIds)
  editVisual.reality_fingerprint = stringValue(stream.realitySettings?.settings?.fingerprint, 'chrome')
  editVisual.reality_spider_x = stringValue(stream.realitySettings?.settings?.spiderX, '/')
  editVisual.reality_xver = numberValue(stream.realitySettings?.xver)
  editVisual.reality_max_timediff = numberValue(stream.realitySettings?.maxTimediff)
  editVisual.reality_min_client = stringValue(stream.realitySettings?.minClient, '')
  editVisual.reality_max_client = stringValue(stream.realitySettings?.maxClient, '')
  editVisual.vless_flow = editForm.flow || (editVisual.vless_security === 'reality' ? 'xtls-rprx-vision' : '')
  editVisual.ss_method = stringValue(settings.method, '2022-blake3-aes-256-gcm') as SS2022Method
  editVisual.ss_password = stringValue(settings.password, '')
  editVisual.ss_network = stringValue(settings.network, 'tcp,udp')
  editVisual.sniffing_enabled = boolValue(sniffing.enabled, true)
  editVisual.sniffing_dest_override_text = listToText(sniffing.destOverride) || 'http,tls,quic'
  editVisual.sniffing_metadata_only = boolValue(sniffing.metadataOnly)
  editVisual.sniffing_route_only = boolValue(sniffing.routeOnly)
}

function buildEditSettings() {
  const current: any = parseJSONForEdit(inboundEditForm.settings)
  if (editVisual.protocol === 'ss2022') {
    return {
      ...current,
      method: editVisual.ss_method,
      password: editVisual.ss_password,
      network: editVisual.ss_network,
      clients: Array.isArray(current.clients) ? current.clients : [],
    }
  }
  return {
    ...current,
    clients: Array.isArray(current.clients) ? current.clients : [],
    decryption: editVisual.vless_encryption || 'none',
    fallbacks: Array.isArray(current.fallbacks) ? current.fallbacks : [],
  }
}

function buildEditStreamSettings() {
  if (editVisual.protocol === 'ss2022') {
    return {
      network: 'tcp',
      security: 'none',
      externalProxy: [],
      tcpSettings: {
        acceptProxyProtocol: false,
        header: { type: 'none' },
      },
    }
  }
  const stream: Record<string, any> = {
    network: editVisual.vless_network,
    security: editVisual.vless_security,
    externalProxy: Array.isArray((parseJSONForEdit(inboundEditForm.stream_settings) as any).externalProxy)
      ? (parseJSONForEdit(inboundEditForm.stream_settings) as any).externalProxy
      : [],
  }
  if (editVisual.vless_network === 'tcp') {
    stream.tcpSettings = {
      acceptProxyProtocol: editVisual.tcp_accept_proxy_protocol,
      header: { type: editVisual.tcp_header_type },
    }
  } else if (editVisual.vless_network === 'ws') {
    stream.wsSettings = {
      acceptProxyProtocol: editVisual.ws_accept_proxy_protocol,
      path: editVisual.ws_path || '/',
      host: editVisual.ws_host,
      headers: editVisual.ws_host ? { Host: editVisual.ws_host } : {},
      heartbeatPeriod: 0,
    }
  } else if (editVisual.vless_network === 'grpc') {
    stream.grpcSettings = {
      serviceName: editVisual.grpc_service_name,
      authority: editVisual.grpc_authority,
      multiMode: editVisual.grpc_multi_mode,
    }
  }
  if (editVisual.vless_security === 'tls') {
    stream.tlsSettings = {
      serverName: editVisual.tls_server_name,
      minVersion: editVisual.tls_min_version,
      maxVersion: editVisual.tls_max_version,
      cipherSuites: [],
      alpn: splitList(editVisual.tls_alpn_text),
      certificates: [],
      rejectUnknownSni: false,
      disableSystemRoot: false,
      enableSessionResumption: false,
    }
  } else if (editVisual.vless_security === 'reality') {
    stream.realitySettings = {
      show: false,
      xver: editVisual.reality_xver,
      dest: editVisual.reality_dest,
      serverNames: splitList(editVisual.reality_server_names_text),
      privateKey: editVisual.private_key,
      minClient: editVisual.reality_min_client,
      maxClient: editVisual.reality_max_client,
      maxTimediff: editVisual.reality_max_timediff,
      shortIds: splitList(editVisual.short_ids_text),
      settings: {
        publicKey: editVisual.public_key,
        fingerprint: editVisual.reality_fingerprint,
        serverName: '',
        spiderX: editVisual.reality_spider_x || '/',
      },
    }
  }
  return stream
}

function buildEditSniffing() {
  return {
    ...parseJSONForEdit(inboundEditForm.sniffing),
    enabled: editVisual.sniffing_enabled,
    destOverride: splitList(editVisual.sniffing_dest_override_text),
    metadataOnly: editVisual.sniffing_metadata_only,
    routeOnly: editVisual.sniffing_route_only,
  }
}

function refreshEditJSON() {
  inboundEditForm.settings = JSON.stringify(buildEditSettings(), null, 2)
  inboundEditForm.stream_settings = JSON.stringify(buildEditStreamSettings(), null, 2)
  inboundEditForm.sniffing = JSON.stringify(buildEditSniffing(), null, 2)
  inboundEditForm.allocate = prettifyJSON(inboundEditForm.allocate)
}

async function submitCreate() {
  if (!createForm.display_name || !createForm.server_address || !createForm.region) {
    ElMessage.warning('显示名 / 服务器地址 / region 必填')
    return
  }
  if (createMode.value === 'visual' && createForm.protocol === 'vless' && createForm.vless_security === 'reality') {
    if (!createForm.private_key || !createForm.public_key || splitList(createForm.short_ids_text).length === 0) {
      ElMessage.warning('请先生成或填写 Reality privateKey / publicKey / shortId')
      return
    }
    if (!createForm.reality_dest || splitList(createForm.reality_server_names_text).length === 0) {
      ElMessage.warning('Reality dest 和 SNI 必填')
      return
    }
  }
  if (createMode.value === 'visual' && createForm.protocol === 'ss2022' && (!createForm.ss_method || !createForm.ss_password)) {
    ElMessage.warning('SS-2022 method 和服务端 PSK 必填')
    return
  }
  createBusy.value = true
  try {
    const generatedSettings = createForm.protocol === 'ss2022' ? buildSS2022Settings() : buildVlessSettings()
    const settings = jsonOrGenerated(createForm.settings_json, 'settings', generatedSettings)
    const streamSettings = jsonOrGenerated(createForm.stream_settings_json, 'streamSettings', buildStreamSettings())
    const sniffing = jsonOrGenerated(createForm.sniffing_json, 'sniffing', buildSniffing())
    const allocate = jsonOrGenerated(createForm.allocate_json, 'allocate', {})

    const res = await createInbound({
      panel_id: createForm.panel_id,
      display_name: createForm.display_name,
      server_address: createForm.server_address,
      flow: createForm.protocol === 'vless' ? createForm.vless_flow.trim() : '',
      region: createForm.region,
      tags: createForm.tags_text
        ? createForm.tags_text.split(',').map((t) => t.trim()).filter(Boolean)
        : [],
      sort_order: createForm.sort_order,
      inbound: {
        remark: createForm.display_name,
        enable: createForm.enable,
        listen: createForm.listen,
        port: createForm.port,
        protocol: createForm.protocol === 'ss2022' ? 'shadowsocks' : 'vless',
        settings,
        stream_settings: streamSettings,
        sniffing,
        allocate,
        expiry_time: createForm.expiry_time,
      },
    })
    ElMessage.success('queued' in res ? '已加入同步任务' : '已创建')
    createDialog.value = false
    tab.value = 'managed'
    await load()
  } catch (e: any) {
    if (!e?.response) {
      ElMessage.error(e?.message ?? '创建失败')
    }
  } finally {
    createBusy.value = false
  }
}

async function load() {
  loading.value = true
  try {
    if (tab.value === 'managed') {
      managed.value = await listNodes()
      selectedManaged.value = []
    } else {
      const res = await listUnmanagedInbounds()
      unmanaged.value = res.items
    }
  } finally {
    loading.value = false
  }
}

function handleManagedSelectionChange(rows: Node[]) {
  selectedManaged.value = rows
}

function startImport(row: UnmanagedInbound) {
  importForm.panel_id = row.PanelID
  importForm.panel_name = row.PanelName
  importForm.inbound_id = row.InboundID
  importForm.display_name = row.Remark || `${row.Protocol}:${row.Port}`
  importForm.server_address = ''
  importForm.flow = ''
  importForm.region = ''
  importForm.tags_text = ''
  importForm.sort_order = 100
  importDialog.value = true
}

async function submitImport() {
  if (!importForm.server_address || !importForm.region) {
    ElMessage.warning('请填写服务器地址和 region')
    return
  }
  importBusy.value = true
  try {
    await importNode({
      panel_id: importForm.panel_id,
      inbound_id: importForm.inbound_id,
      display_name: importForm.display_name,
      server_address: importForm.server_address,
      flow: importForm.flow.trim(),
      region: importForm.region,
      tags: importForm.tags_text
        ? importForm.tags_text.split(',').map((t) => t.trim()).filter(Boolean)
        : [],
      sort_order: importForm.sort_order,
    })
    ElMessage.success('已纳管')
    importDialog.value = false
    tab.value = 'managed'
    await load()
  } finally {
    importBusy.value = false
  }
}

async function openEdit(row: Node) {
  editing.value = row
  editForm.display_name = row.display_name
  editForm.server_address = row.server_address
  editForm.flow = row.flow ?? ''
  editForm.region = row.region
  editForm.tags_text = (row.tags ?? []).join(', ')
  editForm.sort_order = row.sort_order
  inboundEditLoaded.value = false
  inboundEditError.value = ''
  editMode.value = 'visual'
  inboundEditForm.remark = row.display_name
  inboundEditForm.enable = row.enabled
  inboundEditForm.listen = ''
  inboundEditForm.port = 443
  inboundEditForm.protocol = ''
  inboundEditForm.settings = ''
  inboundEditForm.stream_settings = ''
  inboundEditForm.sniffing = ''
  inboundEditForm.allocate = ''
  inboundEditForm.expiry_time = 0
  editDialog.value = true
  try {
    const detail = await getNode(row.id)
    if (detail.inbound) {
      inboundEditForm.remark = detail.inbound.remark
      inboundEditForm.enable = detail.inbound.enable
      inboundEditForm.listen = detail.inbound.listen
      inboundEditForm.port = detail.inbound.port
      inboundEditForm.protocol = detail.inbound.protocol
      inboundEditForm.settings = prettifyJSON(detail.inbound.settings)
      inboundEditForm.stream_settings = prettifyJSON(detail.inbound.stream_settings)
      inboundEditForm.sniffing = prettifyJSON(detail.inbound.sniffing)
      inboundEditForm.allocate = prettifyJSON(detail.inbound.allocate)
      inboundEditForm.expiry_time = detail.inbound.expiry_time ?? 0
      loadEditVisualFromJSON()
      inboundEditLoaded.value = true
    } else if (detail.inbound_error) {
      inboundEditError.value = detail.inbound_error
    }
  } catch (e: any) {
    inboundEditError.value = e?.response?.data?.error ?? e?.message ?? '读取 inbound 配置失败'
  }
}

async function submitEdit() {
  if (!editing.value) return
  if (!editForm.display_name || !editForm.region) {
    ElMessage.warning('显示名和 region 必填')
    return
  }
  editBusy.value = true
  try {
    if (inboundEditLoaded.value) {
      const settings =
        editMode.value === 'visual'
          ? compactJSON(buildEditSettings())
          : compactEditorJSON(inboundEditForm.settings, 'settings')
      const streamSettings =
        editMode.value === 'visual'
          ? compactJSON(buildEditStreamSettings())
          : compactEditorJSON(inboundEditForm.stream_settings, 'streamSettings')
      const sniffing =
        editMode.value === 'visual'
          ? compactJSON(buildEditSniffing())
          : compactEditorJSON(inboundEditForm.sniffing, 'sniffing')
      await updateInboundConfig(editing.value.id, {
        remark: inboundEditForm.remark || editForm.display_name,
        enable: inboundEditForm.enable,
        listen: inboundEditForm.listen,
        port: inboundEditForm.port,
        protocol:
          editMode.value === 'visual'
            ? editVisual.protocol === 'ss2022'
              ? 'shadowsocks'
              : 'vless'
            : inboundEditForm.protocol,
        settings,
        stream_settings: streamSettings,
        sniffing,
        allocate: compactEditorJSON(inboundEditForm.allocate, 'allocate'),
        expiry_time: inboundEditForm.expiry_time,
      })
    }
    await updateNodeMetadata(editing.value.id, {
      display_name: editForm.display_name,
      server_address: editForm.server_address,
      flow:
        editMode.value === 'visual' && inboundEditLoaded.value
          ? editVisual.protocol === 'vless'
            ? editVisual.vless_flow.trim()
            : ''
          : editForm.flow.trim(),
      region: editForm.region,
      tags: editForm.tags_text
        ? editForm.tags_text.split(',').map((t) => t.trim()).filter(Boolean)
        : [],
      sort_order: editForm.sort_order,
    })
    ElMessage.success('已保存')
    editDialog.value = false
    await load()
  } finally {
    editBusy.value = false
  }
}

async function confirmDelete(row: Node) {
  await ElMessageBox.confirm(
    `确定删除节点 ${row.display_name}？会先清除该 inbound 内所有本系统纳管 client，再删除 inbound 本身（要求 inbound 内只剩纳管 client）。`,
    '确认删除',
    { type: 'warning' },
  )
  await deleteNode(row.id)
  ElMessage.success('已删除')
  await load()
}

async function batchSetManagedEnabled(enabled: boolean) {
  if (selectedManaged.value.length === 0) return
  const rows = selectedManaged.value.slice()
  batchBusy.value = enabled ? 'enable' : 'disable'
  try {
    const results = await Promise.allSettled(rows.map((row) => setNodeEnabled(row.id, enabled)))
    const failed = results.filter((result) => result.status === 'rejected').length
    if (failed > 0) {
      ElMessage.warning(`已${enabled ? '启用' : '禁用'} ${rows.length - failed} 个节点，失败 ${failed} 个`)
    } else {
      ElMessage.success(`已${enabled ? '启用' : '禁用'} ${rows.length} 个节点`)
    }
    await load()
  } finally {
    batchBusy.value = ''
  }
}

async function batchDeleteManaged() {
  if (selectedManaged.value.length === 0) return
  const rows = selectedManaged.value.slice()
  const names = rows.slice(0, 5).map((row) => row.display_name).join('、')
  const suffix = rows.length > 5 ? ` 等 ${rows.length} 个节点` : ''
  try {
    await ElMessageBox.confirm(
      `确定删除 ${names}${suffix}？会先清除这些 inbound 内所有本系统纳管 client，再删除 inbound 本身。`,
      '批量删除节点',
      { type: 'warning' },
    )
  } catch {
    return
  }
  batchBusy.value = 'delete'
  try {
    const results = await Promise.allSettled(rows.map((row) => deleteNode(row.id)))
    const deletedRows = rows.filter((_, index) => results[index].status === 'fulfilled')
    const failed = rows.length - deletedRows.length
    managed.value = managed.value.filter((node) => !deletedRows.some((row) => row.id === node.id))
    selectedManaged.value = []
    if (failed > 0) {
      ElMessage.warning(`已删除 ${deletedRows.length} 个节点，失败 ${failed} 个`)
    } else {
      ElMessage.success(`已删除 ${deletedRows.length} 个节点`)
    }
  } finally {
    batchBusy.value = ''
  }
}

async function changeEnabled(row: Node, enabled: boolean) {
  const previous = row.enabled
  row.enabled = enabled
  enabledBusy.value = { ...enabledBusy.value, [row.id]: true }
  try {
    await setNodeEnabled(row.id, enabled)
    if (editing.value?.id === row.id) {
      editing.value.enabled = enabled
    }
    await load()
  } catch (e: any) {
    row.enabled = previous
    if (editing.value?.id === row.id) {
      editing.value.enabled = previous
    }
    ElMessage.error(e?.response?.data?.error ?? e?.message ?? '切换失败')
  } finally {
    enabledBusy.value = { ...enabledBusy.value, [row.id]: false }
  }
}

function onRowEnabledChange(row: Node, value: boolean | string | number) {
  void changeEnabled(row, Boolean(value))
}

function onEditingEnabledChange(value: boolean | string | number) {
  if (!editing.value) return
  void changeEnabled(editing.value, Boolean(value))
}

onMounted(async () => {
  await loadServers()
  await load()
})
</script>

<template>
  <div class="psp-page">
    <div class="psp-page-header">
      <div class="psp-page-title">节点管理</div>
      <el-button type="primary" @click="openCreateCheckServers">新增 inbound</el-button>
    </div>

    <el-tabs v-model="tab" @tab-change="load">
      <el-tab-pane label="纳管中" name="managed">
        <div v-if="selectedManagedCount > 0" class="psp-toolbar">
          <span class="selection-count">已选 {{ selectedManagedCount }}</span>
          <el-button
            :icon="Unlock"
            :loading="batchBusy === 'enable'"
            :disabled="batchBusy !== ''"
            @click="batchSetManagedEnabled(true)"
          >
            批量启用
          </el-button>
          <el-button
            :icon="Lock"
            :loading="batchBusy === 'disable'"
            :disabled="batchBusy !== ''"
            @click="batchSetManagedEnabled(false)"
          >
            批量禁用
          </el-button>
          <el-button
            type="danger"
            :icon="Delete"
            :loading="batchBusy === 'delete'"
            :disabled="batchBusy !== ''"
            @click="batchDeleteManaged"
          >
            批量删除
          </el-button>
        </div>
        <el-table v-loading="loading" :data="managed" stripe @selection-change="handleManagedSelectionChange">
          <el-table-column type="selection" width="48" />
          <el-table-column prop="id" label="ID" width="80" />
          <el-table-column prop="display_name" label="显示名" min-width="180" />
          <el-table-column prop="panel_name" label="服务器名称" min-width="180" />
          <el-table-column prop="server_address" label="连接地址" min-width="200" />
          <el-table-column prop="region" label="Region" width="80" />
          <el-table-column label="Tags" min-width="180">
            <template #default="{ row }">
              <el-tag v-for="t in row.tags" :key="t" size="small" style="margin-right: 4px">
                {{ t }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="Inbound" width="100">
            <template #default="{ row }">
              {{ row.inbound_id }}
            </template>
          </el-table-column>
          <el-table-column label="Flow" min-width="160">
            <template #default="{ row }">
              {{ row.flow || '-' }}
            </template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-switch
                :model-value="row.enabled"
                :loading="enabledBusy[row.id]"
                @change="onRowEnabledChange(row, $event)"
              />
            </template>
          </el-table-column>
          <el-table-column label="操作" width="220">
            <template #default="{ row }">
              <el-button size="small" type="primary" @click="openEdit(row)">编辑</el-button>
              <el-button size="small" type="danger" @click="confirmDelete(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="未纳管" name="unmanaged">
        <el-table v-loading="loading" :data="unmanaged" stripe>
          <el-table-column prop="PanelName" label="服务器" width="120" />
          <el-table-column prop="InboundID" label="inbound" width="100" />
          <el-table-column prop="Protocol" label="协议" width="100" />
          <el-table-column prop="Port" label="端口" width="80" />
          <el-table-column prop="Remark" label="3X-UI 备注" min-width="240" />
          <el-table-column label="client 数" width="100">
            <template #default="{ row }">{{ row.ClientCount }}</template>
          </el-table-column>
          <el-table-column label="操作" width="120">
            <template #default="{ row }">
              <el-button type="primary" size="small" @click="startImport(row)">纳管</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="createDialog" :title="createDialogTitle" width="760px" top="4vh">
      <el-form label-width="140px">
        <el-form-item label="配置方式">
          <el-segmented
            v-model="createMode"
            :options="[
              { label: '可视化', value: 'visual' },
              { label: '自定义 JSON', value: 'custom' },
            ]"
          />
        </el-form-item>
        <el-divider content-position="left">服务器与端口</el-divider>
        <el-form-item label="服务器" required>
          <el-select v-model="createForm.panel_id" style="width: 100%" placeholder="选择 3X-UI 服务器">
            <el-option
              v-for="s in servers"
              :key="s.id"
              :label="`${s.name} — ${s.url}`"
              :value="s.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="协议" required>
          <el-segmented
            v-model="createForm.protocol"
            :options="[
              { label: 'VLESS', value: 'vless' },
              { label: 'SS-2022', value: 'ss2022' },
            ]"
          />
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="createForm.enable" />
        </el-form-item>
        <el-form-item label="监听地址">
          <el-input v-model="createForm.listen" placeholder="留空 = 0.0.0.0 / ::" />
        </el-form-item>
        <el-form-item label="监听端口" required>
          <el-input-number v-model="createForm.port" :min="1" :max="65535" />
        </el-form-item>
        <el-form-item label="到期时间">
          <el-input-number v-model="createForm.expiry_time" :min="0" />
          <span class="input-suffix">ms epoch，0 = 不设置</span>
        </el-form-item>

        <el-divider content-position="left">显示与分组</el-divider>
        <el-form-item label="显示名" required>
          <el-input v-model="createForm.display_name" placeholder="TW Static / SS-2022" />
        </el-form-item>
        <el-form-item label="服务器地址" required>
          <el-input v-model="createForm.server_address" placeholder="hinet.example.com" />
          <div style="color: var(--text-muted); font-size: 12px; margin-top: 4px">
            朋友连接时实际拨号的公网域名 / IP
          </div>
        </el-form-item>
        <el-form-item label="Region" required>
          <el-input v-model="createForm.region" placeholder="TW / US / HK / ..." />
        </el-form-item>
        <el-form-item label="Tags">
          <el-input v-model="createForm.tags_text" placeholder="reality, global (逗号分隔)" />
        </el-form-item>
        <el-form-item label="排序权重">
          <el-input-number v-model="createForm.sort_order" />
        </el-form-item>

        <template v-if="createMode === 'visual' && createForm.protocol === 'vless'">
          <el-divider content-position="left">VLESS</el-divider>
          <el-form-item label="加密">
            <el-select v-model="createForm.vless_encryption" style="width: 100%">
              <el-option label="none" value="none" />
              <el-option label="X25519" value="X25519" />
              <el-option label="ML-KEM-768" value="ML-KEM-768" />
            </el-select>
          </el-form-item>
          <el-form-item label="传输">
            <el-select v-model="createForm.vless_network" style="width: 100%">
              <el-option
                v-for="opt in vlessNetworkOptions"
                :key="opt.value"
                :label="opt.label"
                :value="opt.value"
              />
            </el-select>
          </el-form-item>
          <el-form-item label="安全层">
            <el-select v-model="createForm.vless_security" style="width: 100%">
              <el-option
                v-for="opt in vlessSecurityOptions"
                :key="opt.value"
                :label="opt.label"
                :value="opt.value"
              />
            </el-select>
          </el-form-item>
          <el-form-item label="Flow">
            <el-select
              v-model="createForm.vless_flow"
              filterable
              allow-create
              default-first-option
              clearable
              style="width: 100%"
              placeholder="留空 = 不写 flow"
            >
              <el-option
                v-for="opt in vlessFlowOptions"
                :key="opt.value || 'none'"
                :label="opt.label"
                :value="opt.value"
              />
            </el-select>
            <div style="color: var(--text-muted); font-size: 12px; margin-top: 4px">
              写入节点 flow 设置；同步扫描会把该 inbound 下纳管的 VLESS client 更新为此值
            </div>
          </el-form-item>

          <template v-if="createForm.vless_network === 'tcp'">
            <el-form-item label="PROXY Protocol">
              <el-switch v-model="createForm.tcp_accept_proxy_protocol" />
            </el-form-item>
            <el-form-item label="TCP Header">
              <el-select v-model="createForm.tcp_header_type" style="width: 100%">
                <el-option label="none" value="none" />
                <el-option label="http" value="http" />
              </el-select>
            </el-form-item>
          </template>

          <template v-if="createForm.vless_network === 'ws'">
            <el-form-item label="WS Path">
              <el-input v-model="createForm.ws_path" placeholder="/" />
            </el-form-item>
            <el-form-item label="WS Host">
              <el-input v-model="createForm.ws_host" placeholder="cdn.example.com" />
            </el-form-item>
            <el-form-item label="PROXY Protocol">
              <el-switch v-model="createForm.ws_accept_proxy_protocol" />
            </el-form-item>
          </template>

          <template v-if="createForm.vless_network === 'grpc'">
            <el-form-item label="Service Name">
              <el-input v-model="createForm.grpc_service_name" placeholder="grpc-service" />
            </el-form-item>
            <el-form-item label="Authority">
              <el-input v-model="createForm.grpc_authority" placeholder="可留空" />
            </el-form-item>
            <el-form-item label="Multi Mode">
              <el-switch v-model="createForm.grpc_multi_mode" />
            </el-form-item>
          </template>

          <template v-if="createForm.vless_security === 'tls'">
            <el-divider content-position="left">TLS 参数</el-divider>
            <el-form-item label="Server Name">
              <el-input v-model="createForm.tls_server_name" placeholder="example.com" />
            </el-form-item>
            <el-form-item label="ALPN">
              <el-input v-model="createForm.tls_alpn_text" placeholder="h2,http/1.1" />
            </el-form-item>
            <el-form-item label="TLS 版本">
              <div class="inline-fields">
                <el-input v-model="createForm.tls_min_version" placeholder="min，例如 1.2" />
                <el-input v-model="createForm.tls_max_version" placeholder="max，例如 1.3" />
              </div>
            </el-form-item>
          </template>

          <template v-if="createForm.vless_security === 'reality'">
            <el-divider content-position="left">Reality 参数</el-divider>
            <el-form-item label="伪装目标 (dest)" required>
              <el-input v-model="createForm.reality_dest" placeholder="yahoo.com:443" />
            </el-form-item>
            <el-form-item label="SNI" required>
              <el-input v-model="createForm.reality_server_names_text" placeholder="yahoo.com,www.yahoo.com" />
            </el-form-item>
            <el-form-item label="uTLS 指纹">
              <el-select v-model="createForm.reality_fingerprint" style="width: 100%">
                <el-option v-for="fp in fingerprintOptions" :key="fp" :label="fp" :value="fp" />
              </el-select>
            </el-form-item>
            <el-form-item label="SpiderX">
              <el-input v-model="createForm.reality_spider_x" placeholder="/" />
            </el-form-item>
            <el-form-item label="xver / maxDiff">
              <div class="inline-fields">
                <el-input-number v-model="createForm.reality_xver" :min="0" />
                <el-input-number v-model="createForm.reality_max_timediff" :min="0" />
              </div>
            </el-form-item>
            <el-form-item label="Client 版本">
              <div class="inline-fields">
                <el-input v-model="createForm.reality_min_client" placeholder="minClient，可留空" />
                <el-input v-model="createForm.reality_max_client" placeholder="maxClient，可留空" />
              </div>
            </el-form-item>
            <el-form-item label="密钥对" required>
              <el-button @click="genKeys">生成 Reality 密钥</el-button>
              <div v-if="createForm.private_key" class="key-block">
                <div class="key-label">privateKey</div>
                <code>{{ createForm.private_key }}</code>
                <div class="key-label">publicKey</div>
                <code>{{ createForm.public_key }}</code>
              </div>
            </el-form-item>
            <el-form-item label="shortIds" required>
              <el-input v-model="createForm.short_ids_text" placeholder="逗号分隔，可多个" />
            </el-form-item>
          </template>
        </template>

        <template v-else-if="createMode === 'visual'">
          <el-divider content-position="left">SS-2022</el-divider>
          <el-form-item label="Method" required>
            <el-select v-model="createForm.ss_method" style="width: 100%">
              <el-option
                v-for="method in ss2022Methods"
                :key="method.value"
                :label="method.label"
                :value="method.value"
              />
            </el-select>
          </el-form-item>
          <el-form-item label="服务端 PSK" required>
            <el-input v-model="createForm.ss_password" placeholder="SS-2022 server PSK">
              <template #append>
                <el-button @click="generateSSPassword">生成</el-button>
              </template>
            </el-input>
          </el-form-item>
          <el-form-item label="Network">
            <el-select v-model="createForm.ss_network" style="width: 100%">
              <el-option label="tcp,udp" value="tcp,udp" />
              <el-option label="tcp" value="tcp" />
              <el-option label="udp" value="udp" />
            </el-select>
          </el-form-item>
        </template>

        <template v-if="createMode === 'visual'">
          <el-divider content-position="left">Sniffing</el-divider>
          <el-form-item label="启用 sniffing">
            <el-switch v-model="createForm.sniffing_enabled" />
          </el-form-item>
          <el-form-item label="Dest Override">
            <el-input v-model="createForm.sniffing_dest_override_text" placeholder="http,tls,quic" />
          </el-form-item>
          <el-form-item label="Sniffing 模式">
            <el-checkbox v-model="createForm.sniffing_metadata_only">metadataOnly</el-checkbox>
            <el-checkbox v-model="createForm.sniffing_route_only">routeOnly</el-checkbox>
          </el-form-item>
        </template>

        <template v-if="createMode === 'custom'">
          <el-divider content-position="left">自定义 JSON</el-divider>
          <el-form-item label="生成 JSON">
            <el-button @click="refreshAdvancedJSON">用当前可视化参数填入 JSON</el-button>
          </el-form-item>
          <el-form-item label="settings">
            <el-input v-model="createForm.settings_json" type="textarea" :rows="6" />
          </el-form-item>
          <el-form-item label="streamSettings">
            <el-input v-model="createForm.stream_settings_json" type="textarea" :rows="8" />
          </el-form-item>
          <el-form-item label="sniffing">
            <el-input v-model="createForm.sniffing_json" type="textarea" :rows="5" />
          </el-form-item>
          <el-form-item label="allocate">
            <el-input v-model="createForm.allocate_json" type="textarea" :rows="4" placeholder="{}" />
          </el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="createDialog = false">取消</el-button>
        <el-button type="primary" :loading="createBusy" @click="submitCreate">创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="editDialog" title="编辑节点" width="760px" top="4vh">
      <div v-if="editing">
        <el-descriptions :column="1" border size="small" style="margin-bottom: 16px">
          <el-descriptions-item label="服务器 / inbound">
            {{ editing.panel_name }} / {{ editing.inbound_id }}
          </el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-switch
              :model-value="editing.enabled"
              :loading="enabledBusy[editing.id]"
              @change="onEditingEnabledChange"
            />
          </el-descriptions-item>
        </el-descriptions>

        <el-form label-width="110px">
          <el-divider content-position="left">面板元数据</el-divider>
          <el-form-item label="显示名" required>
            <el-input v-model="editForm.display_name" />
          </el-form-item>
          <el-form-item label="服务器地址">
            <el-input v-model="editForm.server_address" placeholder="hinet.kazuha.org" />
            <div style="color: var(--text-muted); font-size: 12px; margin-top: 4px">
              朋友连接时使用的公网域名/IP；改这里只影响订阅渲染，不动 3X-UI 端配置
            </div>
          </el-form-item>
          <el-form-item v-if="!inboundEditLoaded || editMode === 'custom'" label="Flow">
            <el-input v-model="editForm.flow" placeholder="VLESS Reality 常用 xtls-rprx-vision；留空则不强制覆盖" />
          </el-form-item>
          <el-form-item label="Region" required>
            <el-input v-model="editForm.region" placeholder="TW / US / HK / ..." />
          </el-form-item>
          <el-form-item label="Tags">
            <el-input v-model="editForm.tags_text" placeholder="reality, global (逗号分隔)" />
          </el-form-item>
          <el-form-item label="排序权重">
            <el-input-number v-model="editForm.sort_order" />
            <span style="margin-left: 8px; color: var(--text-muted); font-size: 12px">
              越小越靠前；分组级 layout 可覆盖此值
            </span>
          </el-form-item>

          <el-divider content-position="left">3X-UI inbound 配置</el-divider>
          <el-alert
            v-if="inboundEditError"
            type="warning"
            :closable="false"
            show-icon
            style="margin-bottom: 14px"
          >
            <template #title>读取 inbound 配置失败：{{ inboundEditError }}</template>
          </el-alert>
          <template v-if="inboundEditLoaded">
            <el-form-item label="配置方式">
              <el-segmented
                v-model="editMode"
                :options="[
                  { label: '可视化', value: 'visual' },
                  { label: '自定义 JSON', value: 'custom' },
                ]"
              />
            </el-form-item>
            <el-form-item label="Remark">
              <el-input v-model="inboundEditForm.remark" />
            </el-form-item>
            <el-form-item label="启用">
              <el-switch v-model="inboundEditForm.enable" />
            </el-form-item>
            <el-form-item label="监听地址">
              <el-input v-model="inboundEditForm.listen" placeholder="留空 = 0.0.0.0 / ::" />
            </el-form-item>
            <el-form-item label="监听端口">
              <el-input-number v-model="inboundEditForm.port" :min="1" :max="65535" />
            </el-form-item>
            <el-form-item label="协议">
              <el-segmented
                v-if="editMode === 'visual'"
                v-model="editVisual.protocol"
                :options="[
                  { label: 'VLESS', value: 'vless' },
                  { label: 'SS-2022', value: 'ss2022' },
                ]"
              />
              <el-input v-else v-model="inboundEditForm.protocol" />
            </el-form-item>
            <el-form-item label="到期时间">
              <el-input-number v-model="inboundEditForm.expiry_time" :min="0" />
              <span class="input-suffix">ms epoch，0 = 不设置</span>
            </el-form-item>

            <template v-if="editMode === 'visual' && editVisual.protocol === 'vless'">
              <el-divider content-position="left">VLESS</el-divider>
              <el-form-item label="加密">
                <el-select v-model="editVisual.vless_encryption" style="width: 100%">
                  <el-option label="none" value="none" />
                  <el-option label="X25519" value="X25519" />
                  <el-option label="ML-KEM-768" value="ML-KEM-768" />
                </el-select>
              </el-form-item>
              <el-form-item label="传输">
                <el-select v-model="editVisual.vless_network" style="width: 100%">
                  <el-option
                    v-for="opt in vlessNetworkOptions"
                    :key="opt.value"
                    :label="opt.label"
                    :value="opt.value"
                  />
                </el-select>
              </el-form-item>
              <el-form-item label="安全层">
                <el-select v-model="editVisual.vless_security" style="width: 100%">
                  <el-option
                    v-for="opt in vlessSecurityOptions"
                    :key="opt.value"
                    :label="opt.label"
                    :value="opt.value"
                  />
                </el-select>
              </el-form-item>
              <el-form-item label="Flow">
                <el-select
                  v-model="editVisual.vless_flow"
                  filterable
                  allow-create
                  default-first-option
                  clearable
                  style="width: 100%"
                  placeholder="留空 = 不写 flow"
                >
                  <el-option
                    v-for="opt in vlessFlowOptions"
                    :key="opt.value || 'none'"
                    :label="opt.label"
                    :value="opt.value"
                  />
                </el-select>
              </el-form-item>

              <template v-if="editVisual.vless_network === 'tcp'">
                <el-form-item label="PROXY Protocol">
                  <el-switch v-model="editVisual.tcp_accept_proxy_protocol" />
                </el-form-item>
                <el-form-item label="TCP Header">
                  <el-select v-model="editVisual.tcp_header_type" style="width: 100%">
                    <el-option label="none" value="none" />
                    <el-option label="http" value="http" />
                  </el-select>
                </el-form-item>
              </template>

              <template v-if="editVisual.vless_network === 'ws'">
                <el-form-item label="WS Path">
                  <el-input v-model="editVisual.ws_path" placeholder="/" />
                </el-form-item>
                <el-form-item label="WS Host">
                  <el-input v-model="editVisual.ws_host" placeholder="cdn.example.com" />
                </el-form-item>
                <el-form-item label="PROXY Protocol">
                  <el-switch v-model="editVisual.ws_accept_proxy_protocol" />
                </el-form-item>
              </template>

              <template v-if="editVisual.vless_network === 'grpc'">
                <el-form-item label="Service Name">
                  <el-input v-model="editVisual.grpc_service_name" placeholder="grpc-service" />
                </el-form-item>
                <el-form-item label="Authority">
                  <el-input v-model="editVisual.grpc_authority" placeholder="可留空" />
                </el-form-item>
                <el-form-item label="Multi Mode">
                  <el-switch v-model="editVisual.grpc_multi_mode" />
                </el-form-item>
              </template>

              <template v-if="editVisual.vless_security === 'tls'">
                <el-divider content-position="left">TLS 参数</el-divider>
                <el-form-item label="Server Name">
                  <el-input v-model="editVisual.tls_server_name" placeholder="example.com" />
                </el-form-item>
                <el-form-item label="ALPN">
                  <el-input v-model="editVisual.tls_alpn_text" placeholder="h2,http/1.1" />
                </el-form-item>
                <el-form-item label="TLS 版本">
                  <div class="inline-fields">
                    <el-input v-model="editVisual.tls_min_version" placeholder="min，例如 1.2" />
                    <el-input v-model="editVisual.tls_max_version" placeholder="max，例如 1.3" />
                  </div>
                </el-form-item>
              </template>

              <template v-if="editVisual.vless_security === 'reality'">
                <el-divider content-position="left">Reality 参数</el-divider>
                <el-form-item label="伪装目标 (dest)">
                  <el-input v-model="editVisual.reality_dest" placeholder="yahoo.com:443" />
                </el-form-item>
                <el-form-item label="SNI">
                  <el-input v-model="editVisual.reality_server_names_text" placeholder="yahoo.com,www.yahoo.com" />
                </el-form-item>
                <el-form-item label="uTLS 指纹">
                  <el-select v-model="editVisual.reality_fingerprint" style="width: 100%">
                    <el-option v-for="fp in fingerprintOptions" :key="fp" :label="fp" :value="fp" />
                  </el-select>
                </el-form-item>
                <el-form-item label="SpiderX">
                  <el-input v-model="editVisual.reality_spider_x" placeholder="/" />
                </el-form-item>
                <el-form-item label="xver / maxDiff">
                  <div class="inline-fields">
                    <el-input-number v-model="editVisual.reality_xver" :min="0" />
                    <el-input-number v-model="editVisual.reality_max_timediff" :min="0" />
                  </div>
                </el-form-item>
                <el-form-item label="Client 版本">
                  <div class="inline-fields">
                    <el-input v-model="editVisual.reality_min_client" placeholder="minClient，可留空" />
                    <el-input v-model="editVisual.reality_max_client" placeholder="maxClient，可留空" />
                  </div>
                </el-form-item>
                <el-form-item label="密钥对">
                  <el-button @click="genEditKeys">重新生成 Reality 密钥</el-button>
                  <div v-if="editVisual.private_key" class="key-block">
                    <div class="key-label">privateKey</div>
                    <code>{{ editVisual.private_key }}</code>
                    <div class="key-label">publicKey</div>
                    <code>{{ editVisual.public_key }}</code>
                  </div>
                </el-form-item>
                <el-form-item label="shortIds">
                  <el-input v-model="editVisual.short_ids_text" placeholder="逗号分隔，可多个" />
                </el-form-item>
              </template>
            </template>

            <template v-else-if="editMode === 'visual'">
              <el-divider content-position="left">SS-2022</el-divider>
              <el-form-item label="Method">
                <el-select v-model="editVisual.ss_method" style="width: 100%">
                  <el-option
                    v-for="method in ss2022Methods"
                    :key="method.value"
                    :label="method.label"
                    :value="method.value"
                  />
                </el-select>
              </el-form-item>
              <el-form-item label="服务端 PSK">
                <el-input v-model="editVisual.ss_password" placeholder="SS-2022 server PSK">
                  <template #append>
                    <el-button @click="generateEditSSPassword">生成</el-button>
                  </template>
                </el-input>
              </el-form-item>
              <el-form-item label="Network">
                <el-select v-model="editVisual.ss_network" style="width: 100%">
                  <el-option label="tcp,udp" value="tcp,udp" />
                  <el-option label="tcp" value="tcp" />
                  <el-option label="udp" value="udp" />
                </el-select>
              </el-form-item>
            </template>

            <template v-if="editMode === 'visual'">
              <el-divider content-position="left">Sniffing</el-divider>
              <el-form-item label="启用 sniffing">
                <el-switch v-model="editVisual.sniffing_enabled" />
              </el-form-item>
              <el-form-item label="Dest Override">
                <el-input v-model="editVisual.sniffing_dest_override_text" placeholder="http,tls,quic" />
              </el-form-item>
              <el-form-item label="Sniffing 模式">
                <el-checkbox v-model="editVisual.sniffing_metadata_only">metadataOnly</el-checkbox>
                <el-checkbox v-model="editVisual.sniffing_route_only">routeOnly</el-checkbox>
              </el-form-item>
            </template>

            <template v-if="editMode === 'custom'">
              <el-divider content-position="left">自定义 JSON</el-divider>
              <el-form-item label="生成 JSON">
                <el-button @click="refreshEditJSON">用当前可视化参数填入 JSON</el-button>
              </el-form-item>
              <el-form-item label="settings">
                <el-input v-model="inboundEditForm.settings" type="textarea" :rows="7" />
              </el-form-item>
              <el-form-item label="streamSettings">
                <el-input v-model="inboundEditForm.stream_settings" type="textarea" :rows="8" />
              </el-form-item>
              <el-form-item label="sniffing">
                <el-input v-model="inboundEditForm.sniffing" type="textarea" :rows="5" />
              </el-form-item>
              <el-form-item label="allocate">
                <el-input v-model="inboundEditForm.allocate" type="textarea" :rows="4" />
              </el-form-item>
            </template>
          </template>
        </el-form>
      </div>
      <template #footer>
        <el-button @click="editDialog = false">取消</el-button>
        <el-button type="primary" :loading="editBusy" @click="submitEdit">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="importDialog" title="纳管现有 inbound" width="500px">
      <el-form label-width="120px" :model="importForm">
        <el-form-item label="服务器">
          <el-input :model-value="importForm.panel_name" disabled />
        </el-form-item>
        <el-form-item label="inbound ID">
          <el-input :model-value="importForm.inbound_id" disabled />
        </el-form-item>
        <el-form-item label="显示名" required>
          <el-input v-model="importForm.display_name" />
        </el-form-item>
        <el-form-item label="服务器地址" required>
          <el-input v-model="importForm.server_address" placeholder="hinet.kazuha.org" />
          <div style="color: var(--text-muted); font-size: 12px; margin-top: 4px">
            朋友连接时使用的公网域名/IP
          </div>
        </el-form-item>
        <el-form-item label="Flow">
          <el-input v-model="importForm.flow" placeholder="VLESS Reality 可填 xtls-rprx-vision；留空则不强制覆盖" />
        </el-form-item>
        <el-form-item label="Region" required>
          <el-input v-model="importForm.region" placeholder="TW / US / HK / ..." />
        </el-form-item>
        <el-form-item label="Tags">
          <el-input v-model="importForm.tags_text" placeholder="reality, global (逗号分隔)" />
        </el-form-item>
        <el-form-item label="排序权重">
          <el-input-number v-model="importForm.sort_order" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="importDialog = false">取消</el-button>
        <el-button type="primary" :loading="importBusy" @click="submitImport">纳管</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.selection-count {
  color: var(--text-muted);
  white-space: nowrap;
}

.input-suffix {
  margin-left: 8px;
  color: var(--text-muted);
  font-size: 12px;
}

.inline-fields {
  display: flex;
  gap: 8px;
  width: 100%;
}

.inline-fields > * {
  flex: 1;
}

.key-block {
  margin-top: 8px;
  width: 100%;
}

.key-label {
  color: var(--text-muted);
  font-size: 12px;
  margin-top: 4px;
}

.key-block code {
  display: block;
  font-size: 12px;
  word-break: break-all;
}
</style>
