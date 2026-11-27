import type { PanelType, Server } from '@/api/servers'
import { ALPN_HY2, ALPN_TLS, type CreateProtocol, type InboundFormState, SUI_ONLY_PROTOCOLS, type VlessSecurity, defaultAlpn, serverHost } from './inboundModel'
import { buildSniffing, buildStreamSettings, settingsBuilderFor } from './inboundSettings'

/** The panel type the form is for; a server not known yet is the original, a 3X-UI one. */
export const panelTypeOf = (server?: Server): PanelType => server?.panel_type ?? '3xui'

/** JSON mode is a 3X-UI escape hatch only: the other adapters take the structured canonical subset, and raw JSON would be lossy there. */
export function canUseJson(server?: Server): boolean {
  return panelTypeOf(server) === '3xui'
}

/** The Xray core version behind a server, which decides the REALITY fingerprint rule; undefined where there is no Xray to ask. */
export function xrayVersionOf(server?: Server): string | undefined {
  const type = panelTypeOf(server)
  if (type === '3xui') return server?.xray_version
  if (type === 'psp' && server?.core_engine === 'xray') return server.core_version ?? server.xray_version
  return undefined
}

/**
 * What turning JSON mode on does: the structured form's current values become the raw text, unless the
 * person already has some. Turning it off again does NOT parse the text back into the fields: the raw
 * JSON may hold transports or options the form does not model, and silently dropping them would surprise
 * the admin, so leaving is lossy on purpose and the caller confirms it.
 */
export function snapshotRaw(prev: InboundFormState): InboundFormState {
  return {
    ...prev,
    raw_settings: prev.raw_settings || JSON.stringify(settingsBuilderFor(prev.protocol)(prev), null, 2),
    raw_stream_settings: prev.raw_stream_settings || JSON.stringify(buildStreamSettings(prev), null, 2),
    raw_sniffing: prev.raw_sniffing || JSON.stringify(buildSniffing(prev), null, 2),
  }
}

/**
 * A protocol change. Security is normalised so the per-protocol sections render correctly: the default,
 * REALITY, is VLESS-only, and without this Trojan (must be TLS) or VMess (no REALITY) would keep
 * 'reality' and hide BOTH the TLS and the REALITY fields, leaving the inbound with no security at all.
 * The ALPN default is retargeted for the new protocol (Hysteria 2 and TUIC: h3; TCP TLS: h2,http/1.1),
 * but only while the field still holds a default, so an ALPN the admin typed is kept.
 */
export function applyProtocolChange(prev: InboundFormState, p: CreateProtocol): InboundFormState {
  let security: VlessSecurity = prev.vless_security
  if (p === 'trojan' || SUI_ONLY_PROTOCOLS.has(p)) security = 'tls'
  else if (p === 'vmess' && security === 'reality') security = 'tls'
  let alpn = prev.tls_alpn_text
  if (alpn === '' || alpn === ALPN_HY2 || alpn === ALPN_TLS) alpn = defaultAlpn(p)
  return { ...prev, protocol: p, vless_security: security, tls_alpn_text: alpn }
}

/** A security change moves the flow with it: REALITY wants Vision, and Vision is meaningless without REALITY. */
export function applySecurityChange(prev: InboundFormState, next: VlessSecurity): InboundFormState {
  let flow = prev.vless_flow
  if (next === 'reality' && !flow) flow = 'xtls-rprx-vision'
  else if (next !== 'reality' && flow === 'xtls-rprx-vision') flow = ''
  return { ...prev, vless_security: next, vless_flow: flow }
}

/**
 * Picking another server on create. The name, region and address defaults follow it, but only while the
 * field is still untouched (empty, or equal to what the previous server gave): a manual edit wins. The
 * options the new server's panel does not take are reset, so nothing is carried over that it would not apply.
 */
export function applyServerChange(prev: InboundFormState, servers: Server[], next: Server): InboundFormState {
  const previous = servers.find(s => s.id === prev.panel_id)
  const previousHost = serverHost(previous)
  const out: InboundFormState = { ...prev, panel_id: next.id }
  if (!prev.display_name || prev.display_name === previous?.name) out.display_name = next.name
  if (!prev.region || prev.region === previous?.region) out.region = next.region ?? ''
  if (next.panel_type !== '3xui') {
    if (out.vless_network === 'xhttp') out.vless_network = 'tcp'
    out.enable = true
    out.tcp_header_type = 'none'
    out.tcp_accept_proxy_protocol = false
    out.ws_accept_proxy_protocol = false
    out.httpupgrade_accept_proxy_protocol = false
    out.grpc_authority = ''
    out.grpc_multi_mode = false
    out.tls_reject_unknown_sni = false
    // S-UI builds its own TLS block and never reads cipherSuites, so a value carried over from a 3X-UI
    // server would sit in the snapshot advertising a restriction the panel does not apply.
    out.tls_cipher_suites = ''
    out.ss_iv_check = false
    out.sockopt_enabled = false
    out.sockopt_tproxy = ''
  } else {
    if (SUI_ONLY_PROTOCOLS.has(out.protocol)) {
      out.protocol = 'vless'
      out.vless_security = 'reality'
      out.tls_alpn_text = defaultAlpn('vless')
    }
    if (out.vless_network === 'http') out.vless_network = 'tcp'
  }
  if (!prev.server_address || prev.server_address === previousHost) out.server_address = serverHost(next)
  return out
}

/** The panel types that accept a server picked on create without leaving JSON mode. */
export const leavesJson = (next: Server) => next.panel_type !== '3xui'
