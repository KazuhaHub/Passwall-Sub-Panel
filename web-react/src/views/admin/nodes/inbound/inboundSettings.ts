// AUTOIMPORT-BEGIN
import { CreateProtocol, EMPTY_INBOUND, InboundFormState, SS2022Method, SUI_ONLY_PROTOCOLS, VlessNetwork, VlessSecurity, boolValue, defaultAlpn, durationSeconds, listToText, numberValue, parseJSONSafe, splitList, stringValue, usesVlessStream } from './inboundModel'
import { PanelType } from '@/api/servers'
import { Node } from '@/api/types'
// AUTOIMPORT-END
export function buildVlessSettings(f: InboundFormState): unknown {
  return { clients: [], decryption: f.vless_encryption || 'none', fallbacks: [] }
}

// VMess inbound settings — 3X-UI's VmessSettings.toJson emits ONLY
// clients[]. disableInsecureEncryption and alterId are not present in
// the model (3X-UI dropped AEAD-only legacy support).
export function buildVmessSettings(_f: InboundFormState): unknown {
  return { clients: [] }
}

// Trojan inbound settings: clients[] always, fallbacks only when present
// (3X-UI omits the key when empty; emitting [] is tolerated but breaks
// round-trip equality on edit).
export function buildTrojanSettings(_f: InboundFormState): unknown {
  return { clients: [] }
}

// SS-2022 inbound settings — ivCheck is a server toggle to reject
// replayed IVs (defaults false in 3X-UI).
export function buildSS2022Settings(f: InboundFormState): unknown {
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
export function buildHysteria2Settings(_f: InboundFormState): unknown {
  return { version: 2, clients: [] }
}

export function buildAnyTLSSettings(f: InboundFormState): unknown {
  return {
    padding_scheme: f.anytls_padding_scheme_text.split(/\r?\n/).map(s => s.trim()).filter(Boolean),
  }
}

export function buildTUICSettings(f: InboundFormState): unknown {
  return {
    congestion_control: f.tuic_congestion_control || 'cubic',
    auth_timeout: f.tuic_auth_timeout > 0 ? `${f.tuic_auth_timeout}s` : '',
    zero_rtt_handshake: f.tuic_zero_rtt_handshake,
    heartbeat: f.tuic_heartbeat > 0 ? `${f.tuic_heartbeat}s` : '',
  }
}

export function buildNaiveSettings(f: InboundFormState): unknown {
  return {
    network: f.naive_network,
    quic_congestion_control: f.naive_quic_congestion_control,
  }
}

// settingsBuilderFor picks the protocol-specific inbound settings builder
// so submit handlers stay free of switch statements.
export function settingsBuilderFor(p: CreateProtocol): (f: InboundFormState) => unknown {
  switch (p) {
    case 'ss2022': return buildSS2022Settings
    case 'vmess': return buildVmessSettings
    case 'trojan': return buildTrojanSettings
    case 'hysteria2': return buildHysteria2Settings
    case 'anytls': return buildAnyTLSSettings
    case 'tuic': return buildTUICSettings
    case 'naive': return buildNaiveSettings
    default: return buildVlessSettings
  }
}

// canonicalProtocolName maps the form enum to the stable protocol name used
// by the admin API. Each panel adapter translates that canonical value.
export function canonicalProtocolName(p: CreateProtocol): string {
  switch (p) {
    case 'ss2022': return 'shadowsocks'
    case 'vmess': return 'vmess'
    case 'trojan': return 'trojan'
    case 'hysteria2': return 'hysteria2'
    case 'anytls': return 'anytls'
    case 'tuic': return 'tuic'
    case 'naive': return 'naive'
    default: return 'vless'
  }
}

export function buildStreamSettings(f: InboundFormState): unknown {
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
  if (SUI_ONLY_PROTOCOLS.has(f.protocol)) {
    return {
      network: 'tcp',
      security: 'tls',
      tlsSettings: buildTLSSettings(f),
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
  } else if (f.vless_network === 'http') {
    stream.httpSettings = {
      path: f.httpupgrade_path || '/',
      host: f.httpupgrade_host ? [f.httpupgrade_host] : [],
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
export function buildTLSSettings(f: InboundFormState): Record<string, unknown> {
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
    cipherSuites: f.tls_cipher_suites,
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
export function buildSockoptWrapper(f: InboundFormState): Record<string, unknown> {
  if (!f.sockopt_enabled) return {}
  const so: Record<string, unknown> = {}
  if (f.sockopt_mark > 0) so.mark = f.sockopt_mark
  if (f.sockopt_accept_proxy_protocol) so.acceptProxyProtocol = true
  if (f.sockopt_tcp_fast_open) so.tcpFastOpen = true
  if (f.sockopt_tcp_keep_alive_interval > 0) so.tcpKeepAliveInterval = f.sockopt_tcp_keep_alive_interval
  if (f.sockopt_tcp_keep_alive_idle > 0) so.tcpKeepAliveIdle = f.sockopt_tcp_keep_alive_idle
  if (f.sockopt_tcp_user_timeout > 0) so.tcpUserTimeout = f.sockopt_tcp_user_timeout
  if (f.sockopt_tproxy) so.tproxy = f.sockopt_tproxy
  if (Object.keys(so).length === 0) return {}
  return { sockopt: so }
}

export function buildSniffing(f: InboundFormState): unknown {
  return {
    enabled: f.sniffing_enabled,
    destOverride: splitList(f.sniffing_dest_override_text),
    metadataOnly: f.sniffing_metadata_only,
    routeOnly: f.sniffing_route_only,
  }
}

export interface InboundDetail {
  protocol: string
  listen?: string
  port?: number
  enable: boolean
  settings: string
  stream_settings: string
  sniffing: string
}

export function parseInboundForEdit(node: Node, ib: InboundDetail): InboundFormState {
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
  const httpTransport = (stream.httpSettings as Record<string, unknown>) ?? {}
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
  // Only the three allowlisted SS-2022 methods reach this structured parser;
  // legacy Shadowsocks must never be silently mapped onto VLESS.
  let protocol: CreateProtocol = 'vless'
  switch (ib.protocol) {
    case 'vmess': protocol = 'vmess'; break
    case 'trojan': protocol = 'trojan'; break
    case 'hysteria2': protocol = 'hysteria2'; break
    case 'anytls': protocol = 'anytls'; break
    case 'tuic': protocol = 'tuic'; break
    case 'naive': protocol = 'naive'; break
    case 'shadowsocks':
      protocol = 'ss2022'
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
    httpupgrade_path: stringValue(httpupgrade.path) || stringValue(httpTransport.path, '/'),
    httpupgrade_host: stringValue(httpupgrade.host) || stringValue(httpupgradeHeaders.Host) || listToText(httpTransport.host),
    httpupgrade_accept_proxy_protocol: boolValue(httpupgrade.acceptProxyProtocol),
    xhttp_path: stringValue(xhttp.path, '/'),
    xhttp_host: stringValue(xhttp.host),
    xhttp_mode: stringValue(xhttp.mode),
    tls_server_name: stringValue(tls.serverName),
    tls_alpn_text: listToText(tls.alpn) || defaultAlpn(protocol),
    tls_min_version: stringValue(tls.minVersion),
    tls_max_version: stringValue(tls.maxVersion),
    tls_cipher_suites: stringValue(tls.cipherSuites),
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
    anytls_padding_scheme_text: Array.isArray(settings.padding_scheme)
      ? settings.padding_scheme.map(v => String(v)).join('\n')
      : EMPTY_INBOUND.anytls_padding_scheme_text,
    tuic_congestion_control: (['cubic', 'new_reno', 'bbr'].includes(stringValue(settings.congestion_control))
      ? stringValue(settings.congestion_control)
      : 'cubic') as InboundFormState['tuic_congestion_control'],
    tuic_auth_timeout: durationSeconds(settings.auth_timeout, 3),
    tuic_zero_rtt_handshake: boolValue(settings.zero_rtt_handshake),
    tuic_heartbeat: durationSeconds(settings.heartbeat, 10),
    naive_network: (['tcp', 'udp'].includes(stringValue(settings.network))
      ? stringValue(settings.network)
      : '') as InboundFormState['naive_network'],
    naive_quic_congestion_control: (['bbr', 'bbr_standard', 'bbr2', 'bbr2_variant', 'cubic', 'reno'].includes(stringValue(settings.quic_congestion_control))
      ? stringValue(settings.quic_congestion_control)
      : '') as InboundFormState['naive_quic_congestion_control'],
    sockopt_enabled: Object.keys(sockopt).length > 0,
    sockopt_mark: numberValue(sockopt.mark),
    sockopt_accept_proxy_protocol: boolValue(sockopt.acceptProxyProtocol),
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

export function validateInboundForm(f: InboundFormState, panelType: PanelType, t: (k: string) => string): string | null {
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
  const tlsInbound = f.protocol === 'trojan' || f.protocol === 'hysteria2' ||
    SUI_ONLY_PROTOCOLS.has(f.protocol) || (usesVlessStream(f.protocol) && f.vless_security === 'tls')
  if (panelType === 'sui' && tlsInbound) {
    const complete = (f.tls_cert_mode === 'file' && !!f.tls_cert_file && !!f.tls_key_file) ||
      (f.tls_cert_mode === 'inline' && !!f.tls_cert_pem && !!f.tls_key_pem) ||
      (f.tls_cert_mode === 'psp_managed' && f.cert_id > 0)
    if (!complete) return 'S-UI TLS 入站必须选择完整的证书与私钥，或绑定一个有效的 PSP 托管证书。'
  }
  return null
}

