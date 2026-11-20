// @vitest-environment jsdom
import { useState } from 'react'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Server } from '@/api/servers'
import i18n from '@/test/initI18n'
import { api, installReads } from '../testHarness'
import { canUseJson, InboundForm, snapshotRaw, type InboundFormProps } from './InboundForm'
import { EMPTY_INBOUND, type InboundFormState } from './inboundModel'

// The words of the new sections ship with the locale files; the harness loads the checked-in ones.
i18n.addResourceBundle('en-US', 'admin', {
  'nodes.inbound_form.section_protocol': 'Protocol and port', 'nodes.inbound_form.section_transport': 'Transport',
  'nodes.inbound_form.section_security': 'Security (TLS / REALITY)', 'nodes.inbound_form.section_advanced': 'Advanced',
  'nodes.inbound_form.sui_section': '{{protocol}} · S-UI / sing-box',
  'nodes.create_dialog.anytls_padding': 'Padding scheme (one per line)', 'nodes.create_dialog.anytls_padding_hint': "Leave blank to use S-UI's default AnyTLS padding scheme.",
  'nodes.create_dialog.tuic_congestion_control': 'Congestion control', 'nodes.create_dialog.tuic_auth_timeout': 'Auth timeout (s)',
  'nodes.create_dialog.tuic_heartbeat': 'Heartbeat (s)', 'nodes.create_dialog.tuic_zero_rtt': 'Zero-RTT handshake',
  'nodes.create_dialog.naive_network': 'Network', 'nodes.create_dialog.naive_network_default': 'TCP + UDP (default)',
  'nodes.create_dialog.naive_quic_congestion_control': 'QUIC congestion control', 'nodes.create_dialog.naive_quic_off': 'QUIC off',
}, true, true)

const xui = (over: Partial<Server> = {}): Server => ({ id: 1, name: 'Tokyo', url: 'https://tokyo.example.test:2053', panel_type: '3xui', region: 'JP', capabilities: [], ...over } as Server)
const sui = (over: Partial<Server> = {}): Server => ({ id: 2, name: 'Osaka', url: 'https://osaka.example.test:2095', panel_type: 'sui', region: 'JP', capabilities: [], ...over } as Server)

let state: InboundFormState
function Host({ initial, ...props }: { initial?: Partial<InboundFormState> } & Partial<InboundFormProps>) {
  const [form, setForm] = useState<InboundFormState>({ ...EMPTY_INBOUND, panel_id: 1, ...initial })
  state = form
  return <InboundForm form={form} setForm={setForm} json={false} onGenKeys={vi.fn()} onGenSSPassword={vi.fn()} genKeysBusy={false} server={xui()} {...props} />
}
async function mountForm(initial?: Partial<InboundFormState>, props: Partial<InboundFormProps> = {}, certs: unknown[] = []) {
  installReads({ '/admin/certs': { certs } })
  const view = render(<Host initial={initial} {...props} />)
  await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/certs', expect.anything()))
  return view
}
const pick = (label: string, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } })
const type = (label: string, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } })
// A required field's label carries an asterisk.
const typeLike = (label: RegExp, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } })
const sections = () => screen.getAllByRole('heading', { level: 3 }).map(h => h.textContent)

beforeEach(() => { state = EMPTY_INBOUND })

describe('REALITY fingerprint', () => {
  // Xray 26.9.8+ wants X25519MLKEM768 first, which only Chrome's fingerprint offers.
  it('is forced to chrome and locked for Xray 26.9.8 or newer', async () => {
    await mountForm({ reality_fingerprint: 'firefox' }, { server: xui({ xray_version: '26.9.9' }) })
    await waitFor(() => expect(state.reality_fingerprint).toBe('chrome'))
    const select = screen.getByLabelText('Fingerprint') as HTMLSelectElement
    expect(select.disabled).toBe(true)
    expect(select.value).toBe('chrome')
    expect(screen.getByText(/fingerprint is fixed to Chrome/)).toBeTruthy()
  })

  it('stays free below that version', async () => {
    await mountForm({ reality_fingerprint: 'firefox' }, { server: xui({ xray_version: '26.9.7' }) })
    expect((screen.getByLabelText('Fingerprint') as HTMLSelectElement).disabled).toBe(false)
    expect(state.reality_fingerprint).toBe('firefox')
    pick('Fingerprint', 'safari')
    expect(state.reality_fingerprint).toBe('safari')
  })

  it('follows the core of a Passwall Node server running Xray, and ignores one running something else', async () => {
    const { unmount } = await mountForm({ reality_fingerprint: 'firefox' }, { server: xui({ panel_type: 'psp', core_engine: 'xray', core_version: '26.10.1' } as Partial<Server>) })
    await waitFor(() => expect(state.reality_fingerprint).toBe('chrome'))
    unmount()
    await mountForm({ reality_fingerprint: 'firefox' }, { server: xui({ panel_type: 'psp', core_engine: 'sing-box', core_version: '26.10.1' } as Partial<Server>) })
    expect(state.reality_fingerprint).toBe('firefox')
  })
})

describe('protocol', () => {
  it('moves security and ALPN with the protocol, keeping an ALPN the admin typed', async () => {
    await mountForm()
    expect(state.vless_security).toBe('reality')
    pick('Protocol', 'trojan')
    expect(state.vless_security).toBe('tls')
    // Trojan must run over TLS: the selector is locked.
    expect((screen.getByLabelText('Transport security') as HTMLSelectElement).disabled).toBe(true)
    pick('Protocol', 'hysteria2')
    expect(state.tls_alpn_text).toBe('h3')
    pick('Protocol', 'vmess')
    expect(state.tls_alpn_text).toBe('h2,http/1.1')
    type('TLS ALPN (comma-separated)', 'h2')
    pick('Protocol', 'hysteria2')
    expect(state.tls_alpn_text).toBe('h2')
  })

  it('takes VMess off REALITY and offers REALITY to VLESS alone', async () => {
    await mountForm()
    pick('Protocol', 'vmess')
    expect(state.vless_security).toBe('tls')
    const options = within(screen.getByLabelText('Transport security')).getAllByRole('option').map(o => (o as HTMLOptionElement).value)
    expect(options).not.toContain('reality')
  })

  it('moves the flow with the security of a VLESS inbound', async () => {
    await mountForm()
    pick('Transport security', 'tls')
    expect(state.vless_flow).toBe('')
    pick('Transport security', 'reality')
    expect(state.vless_flow).toBe('xtls-rprx-vision')
  })

  it('offers a protocol that only S-UI has to an S-UI server alone', async () => {
    const { unmount } = await mountForm()
    expect(within(screen.getByLabelText('Protocol')).queryByRole('option', { name: /AnyTLS|TUIC|Naive/i })).toBeNull()
    unmount()
    await mountForm({ panel_id: 2 }, { server: sui() })
    const names = within(screen.getByLabelText('Protocol')).getAllByRole('option').map(o => o.textContent ?? '')
    expect(names.some(n => /AnyTLS/i.test(n))).toBe(true)
    expect(names.some(n => /TUIC/i.test(n))).toBe(true)
  })

  it('locks the protocol of an existing inbound and in JSON mode', async () => {
    const { unmount } = await mountForm({}, { protocolReadonly: true })
    expect((screen.getByLabelText('Protocol') as HTMLSelectElement).disabled).toBe(true)
    unmount()
    await mountForm({}, { json: true })
    expect((screen.getByLabelText('Protocol') as HTMLSelectElement).disabled).toBe(true)
  })
})

describe('server picker', () => {
  const servers = [xui(), sui({ region: 'JP-Osaka' })]

  it('shows only when the servers are given', async () => {
    const { unmount } = await mountForm()
    expect(screen.queryByLabelText('3X-UI server')).toBeNull()
    unmount()
    await mountForm({}, { servers })
    expect(screen.getByLabelText('3X-UI server')).toBeTruthy()
  })

  it('re-derives name, region and address, and resets what an S-UI server does not take', async () => {
    const onLeaveJson = vi.fn()
    await mountForm({
      display_name: 'Tokyo', region: 'JP', server_address: 'tokyo.example.test', vless_network: 'xhttp', enable: false, tcp_header_type: 'http',
      tcp_accept_proxy_protocol: true, grpc_authority: 'a', grpc_multi_mode: true, tls_reject_unknown_sni: true, tls_cipher_suites: 'TLS_AES_128_GCM_SHA256',
      ss_iv_check: true, sockopt_enabled: true, sockopt_tproxy: 'tproxy',
    }, { servers, onLeaveJson })
    pick('3X-UI server', '2')
    expect(onLeaveJson).toHaveBeenCalledTimes(1)
    expect(state).toMatchObject({
      panel_id: 2, display_name: 'Osaka', region: 'JP-Osaka', server_address: 'osaka.example.test', vless_network: 'tcp', enable: true, tcp_header_type: 'none',
      tcp_accept_proxy_protocol: false, grpc_authority: '', grpc_multi_mode: false, tls_reject_unknown_sni: false, tls_cipher_suites: '', ss_iv_check: false,
      sockopt_enabled: false, sockopt_tproxy: '',
    })
  })

  it('keeps a name, region and address the admin changed', async () => {
    await mountForm({ display_name: 'Mine', region: 'XX', server_address: 'mine.example' }, { servers })
    pick('3X-UI server', '2')
    expect(state).toMatchObject({ panel_id: 2, display_name: 'Mine', region: 'XX', server_address: 'mine.example' })
  })

  it('puts an S-UI-only protocol back to VLESS REALITY when a 3X-UI server is picked, and does not leave JSON', async () => {
    const onLeaveJson = vi.fn()
    await mountForm({ panel_id: 2, protocol: 'tuic', vless_network: 'http', vless_security: 'tls', tls_alpn_text: 'h3' }, { servers, server: sui(), onLeaveJson })
    pick('3X-UI server', '1')
    expect(onLeaveJson).not.toHaveBeenCalled()
    expect(state).toMatchObject({ panel_id: 1, protocol: 'vless', vless_security: 'reality', tls_alpn_text: 'h2,http/1.1', vless_network: 'tcp' })
  })
})

describe('JSON mode', () => {
  it('replaces the structured sections with the three raw editors', async () => {
    await mountForm({ raw_settings: '{"a":1}', raw_stream_settings: '{}', raw_sniffing: '{}' }, { json: true })
    expect(sections()).toEqual(['Protocol and port', 'Advanced — raw JSON'])
    expect((screen.getByLabelText('settings') as HTMLTextAreaElement).value).toBe('{"a":1}')
    expect(screen.getByLabelText('streamSettings')).toBeTruthy()
    expect(screen.getByLabelText('sniffing')).toBeTruthy()
    expect(screen.getByText(/Edit 3X-UI's settings/)).toBeTruthy()
    expect(screen.queryByLabelText('Transport (network)')).toBeNull()
    expect(screen.queryByLabelText('Enable sniffing')).toBeNull()
    fireEvent.change(screen.getByLabelText('settings'), { target: { value: '{"b":2}' } })
    expect(state.raw_settings).toBe('{"b":2}')
    fireEvent.change(screen.getByLabelText('streamSettings'), { target: { value: '{"c":3}' } })
    fireEvent.change(screen.getByLabelText('sniffing'), { target: { value: '{"d":4}' } })
    expect([state.raw_stream_settings, state.raw_sniffing]).toEqual(['{"c":3}', '{"d":4}'])
  })

  it('snapshots the structured form into the raw text when switched on, and keeps text that is there', () => {
    const snap = snapshotRaw({ ...EMPTY_INBOUND, port: 8443 })
    expect(JSON.parse(snap.raw_settings!)).toBeTruthy()
    expect(JSON.parse(snap.raw_stream_settings!)).toMatchObject({ network: 'tcp', security: 'reality' })
    expect(JSON.parse(snap.raw_sniffing!)).toBeTruthy()
    expect(snapshotRaw({ ...EMPTY_INBOUND, raw_settings: 'kept' }).raw_settings).toBe('kept')
  })

  it('is offered for 3X-UI alone', () => {
    expect(canUseJson(xui())).toBe(true)
    expect(canUseJson(undefined)).toBe(true)
    expect(canUseJson(sui())).toBe(false)
    expect(canUseJson(xui({ panel_type: 'psp' }))).toBe(false)
  })
})

describe('certificate source', () => {
  const certs = [{ id: 7, name: 'wildcard', status: 'active' }, { id: 8, name: 'expired', status: 'failed' }]

  it('lists the active managed certificates and binds the picked one', async () => {
    await mountForm({ vless_security: 'tls', tls_cert_mode: 'psp_managed' }, {}, certs)
    await waitFor(() => expect(screen.getByRole('option', { name: 'wildcard' })).toBeTruthy())
    expect(screen.queryByRole('option', { name: 'expired' })).toBeNull()
    expect(screen.getByTestId('managed-cert-help').textContent).toMatch(/auto-renews/)
    pick('Select certificate', '7')
    expect(state.cert_id).toBe(7)
  })

  it('warns when no certificate is active', async () => {
    await mountForm({ vless_security: 'tls', tls_cert_mode: 'psp_managed' }, {}, [certs[1]])
    expect(screen.getByTestId('managed-cert-help').textContent).toMatch(/No active certificates/)
    expect(screen.getByTestId('managed-cert-help').className).toMatch(/destructive/)
  })

  it('shows the paths for a file source, the PEM boxes for an inline one, and nothing for none', async () => {
    await mountForm({ vless_security: 'tls' })
    expect(screen.queryByLabelText('Certificate file path')).toBeNull()
    pick('Certificate source', 'file')
    type('Certificate file path', '/c.pem')
    type('Private key file path', '/k.pem')
    expect([state.tls_cert_mode, state.tls_cert_file, state.tls_key_file]).toEqual(['file', '/c.pem', '/k.pem'])
    pick('Certificate source', 'inline')
    type('Certificate (PEM)', 'CERT')
    type('Private key (PEM)', 'KEY')
    expect([state.tls_cert_pem, state.tls_key_pem]).toEqual(['CERT', 'KEY'])
  })

  it('fills the file paths from the panel, and says so when the panel is too old', async () => {
    await mountForm({ vless_security: 'tls', tls_cert_mode: 'file' })
    api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/web-cert') ? { supported: true, cert_file: '/p/full.pem', key_file: '/p/key.pem' } : { certs: [] } }))
    fireEvent.click(screen.getByRole('button', { name: 'Fetch cert paths from panel' }))
    await waitFor(() => expect(state.tls_cert_file).toBe('/p/full.pem'))
    expect(api.get).toHaveBeenCalledWith('/admin/servers/1/web-cert')
    expect(state.tls_key_file).toBe('/p/key.pem')
  })
})

describe('REALITY fields', () => {
  it('write into the state', async () => {
    await mountForm()
    typeLike(/^Reality dest/, 'example.org:443')
    typeLike(/^Reality serverNames/, 'example.org, www.example.org')
    typeLike(/^Private Key/, 'priv')
    typeLike(/^Public Key/, 'pub')
    typeLike(/^Short IDs/, 'ab12')
    type('spiderX path', '/x')
    type('Min client version', '1.8.0')
    type('Max client version', '2.0.0')
    expect(state).toMatchObject({
      reality_dest: 'example.org:443', reality_server_names_text: 'example.org, www.example.org', private_key: 'priv', public_key: 'pub', short_ids_text: 'ab12',
      reality_spider_x: '/x', reality_min_client: '1.8.0', reality_max_client: '2.0.0',
    })
    // Keys are read character by character.
    expect(screen.getByLabelText(/^Private Key/).className).toMatch(/font-mono/)
  })

  it('call the key generator and the random site picker, which are the caller\'s', async () => {
    const onGenKeys = vi.fn()
    const onRandomRealitySite = vi.fn()
    await mountForm({}, { onGenKeys, onRandomRealitySite })
    fireEvent.click(screen.getByRole('button', { name: 'Generate keypair' }))
    fireEvent.click(screen.getByRole('button', { name: 'Random' }))
    expect(onGenKeys).toHaveBeenCalled()
    expect(onRandomRealitySite).toHaveBeenCalled()
  })

  it('shows the busy state of the key generator and leaves out Random when there is none', async () => {
    await mountForm({}, { genKeysBusy: true })
    expect((screen.getByRole('button', { name: 'Generate keypair' }) as HTMLButtonElement).disabled).toBe(true)
    expect(screen.queryByRole('button', { name: 'Random' })).toBeNull()
  })

  it('scan the decoy from the server, and take a result into the target and the server names', async () => {
    api.post.mockResolvedValue({ data: { source_panel_id: 1, source_panel_name: 'Tokyo', items: [{
      target: 'www.example.org:443', host: 'www.example.org', ip: '', port: 443, feasible: true, tls13: true, tlsVersion: 'TLS 1.3', h2: true, alpn: 'h2', x25519: true, curveID: 'X25519',
      certValid: true, certSubject: 'example.org', certIssuer: 'CA', notAfter: '', serverNames: ['www.example.org', 'example.org'], latencyMs: 12, reason: '' }] } })
    await mountForm({}, { server: xui({ capabilities: ['reality.scan'] } as Partial<Server>) })
    fireEvent.click(screen.getByRole('button', { name: 'Scan targets' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Use' }))
    await waitFor(() => expect(state.reality_dest).toBe('www.example.org:443'))
    expect(state.reality_server_names_text).toBe('www.example.org, example.org')
    expect(api.post).toHaveBeenCalledWith('/admin/nodes/reality-targets/scan', { panel_id: 1, targets: '' }, expect.anything())
  })

  it('cannot scan on a server without the capability, or before a server is picked', async () => {
    const { unmount } = await mountForm()
    expect((screen.getByRole('button', { name: 'Scan targets' }) as HTMLButtonElement).disabled).toBe(true)
    unmount()
    await mountForm({ panel_id: 0 }, { server: xui({ capabilities: ['reality.scan'] } as Partial<Server>) })
    expect((screen.getByRole('button', { name: 'Scan targets' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('leave out what an S-UI server does not take', async () => {
    await mountForm({ panel_id: 2 }, { server: sui() })
    expect(screen.queryByLabelText('spiderX path')).toBeNull()
    expect(screen.queryByLabelText('Min client version')).toBeNull()
    expect(screen.getByLabelText(/^Private Key/)).toBeTruthy()
  })
})

describe('what depends on the panel type and the network', () => {
  it('shows the enable switch and the advanced section for 3X-UI alone', async () => {
    const { unmount } = await mountForm()
    expect(screen.getByRole('switch', { name: 'Enabled' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Advanced' })).toBeTruthy()
    unmount()
    await mountForm({ panel_id: 2 }, { server: sui() })
    expect(screen.queryByRole('switch', { name: 'Enabled' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Advanced' })).toBeNull()
  })

  it('writes the listener, the port and the enable switch', async () => {
    await mountForm()
    type('Listen (blank = all)', '127.0.0.1')
    type('Port', '8443')
    fireEvent.click(screen.getByRole('switch', { name: 'Enabled' }))
    expect(state).toMatchObject({ listen: '127.0.0.1', port: 8443, enable: false })
  })

  it('shows the fields of the chosen network and the proxy-protocol switches on 3X-UI', async () => {
    await mountForm()
    expect(screen.getByLabelText('TCP header type')).toBeTruthy()
    pick('Transport (network)', 'ws')
    type('WebSocket Path', '/ws')
    type('WebSocket Host (header)', 'h.example')
    fireEvent.click(screen.getByRole('switch', { name: 'Accept Proxy Protocol' }))
    expect(state).toMatchObject({ vless_network: 'ws', ws_path: '/ws', ws_host: 'h.example', ws_accept_proxy_protocol: true })
    pick('Transport (network)', 'grpc')
    type('gRPC serviceName', 'svc')
    type('gRPC authority', 'auth')
    fireEvent.click(screen.getByRole('switch', { name: 'Multi-mode' }))
    expect(state).toMatchObject({ grpc_service_name: 'svc', grpc_authority: 'auth', grpc_multi_mode: true })
    pick('Transport (network)', 'httpupgrade')
    type('WebSocket Path', '/hu')
    expect(state.httpupgrade_path).toBe('/hu')
    pick('Transport (network)', 'xhttp')
    pick('Mode', 'stream-up')
    expect(state.xhttp_mode).toBe('stream-up')
  })

  it('offers xhttp to 3X-UI and plain http to S-UI, and the TCP header and proxy protocol only to 3X-UI', async () => {
    const { unmount } = await mountForm()
    const values = () => within(screen.getByLabelText('Transport (network)')).getAllByRole('option').map(o => (o as HTMLOptionElement).value)
    expect(values()).toContain('xhttp')
    expect(values()).not.toContain('http')
    unmount()
    await mountForm({ panel_id: 2 }, { server: sui() })
    expect(values()).toContain('http')
    expect(values()).not.toContain('xhttp')
    expect(screen.queryByLabelText('TCP header type')).toBeNull()
  })

  it('shows TLS fields, and the cipher suites and unknown-SNI switch on 3X-UI alone', async () => {
    const { unmount } = await mountForm({ vless_security: 'tls' })
    type('TLS serverName', 'a.example')
    pick('uTLS fingerprint', 'firefox')
    pick('Min version', '1.2')
    pick('Max version', '1.3')
    fireEvent.click(screen.getByRole('switch', { name: 'Allow insecure connections (Danger!)' }))
    fireEvent.click(screen.getByRole('switch', { name: 'Reject unknown SNI' }))
    expect(state).toMatchObject({ tls_server_name: 'a.example', tls_fingerprint: 'firefox', tls_min_version: '1.2', tls_max_version: '1.3', tls_allow_insecure: true, tls_reject_unknown_sni: true })
    expect(screen.getByLabelText('TLS cipher suites')).toBeTruthy()
    unmount()
    await mountForm({ vless_security: 'tls', panel_id: 2 }, { server: sui() })
    expect(screen.queryByLabelText('TLS cipher suites')).toBeNull()
    expect(screen.queryByLabelText('Reject unknown SNI')).toBeNull()
    expect(screen.getByLabelText('TLS serverName')).toBeTruthy()
  })

  it('shows no security section for plain transport', async () => {
    await mountForm({ vless_security: 'none' })
    expect(sections()).not.toContain('Security (TLS / REALITY)')
  })
})

describe('protocol sections', () => {
  it('Hysteria 2: an emptied idle timeout goes back to 60, and the content box waits for a masquerade type', async () => {
    await mountForm({ protocol: 'hysteria2', tls_alpn_text: 'h3' })
    expect(sections()).toContain('Hysteria 2')
    type('UDP idle timeout (s)', '90')
    expect(state.hy2_udp_idle_timeout).toBe(90)
    type('UDP idle timeout (s)', '')
    expect(state.hy2_udp_idle_timeout).toBe(60)
    type('Obfs (salamander) password — blank = off', 'pw')
    expect(state.hy2_obfs_password).toBe('pw')
    const content = () => screen.getByLabelText(/^(Content|Upstream URL|Directory|Response body)/) as HTMLInputElement
    expect(content().disabled).toBe(true)
    pick('Masquerade', 'proxy')
    expect(screen.getByLabelText('Upstream URL')).toBeTruthy()
    type('Upstream URL', 'https://x.example')
    expect(state).toMatchObject({ hy2_masquerade_type: 'proxy', hy2_masquerade_content: 'https://x.example' })
  })

  it('Shadowsocks 2022: method, network, PSK and the key generator', async () => {
    const onGenSSPassword = vi.fn()
    await mountForm({ protocol: 'ss2022' }, { onGenSSPassword })
    pick('Cipher', '2022-blake3-aes-128-gcm')
    type('Network', 'tcp')
    typeLike(/^Server PSK/, 'psk')
    fireEvent.click(screen.getByRole('switch', { name: 'ivCheck (reject replayed IV)' }))
    fireEvent.click(screen.getByRole('button', { name: 'Generate PSK' }))
    expect(state).toMatchObject({ ss_method: '2022-blake3-aes-128-gcm', ss_network: 'tcp', ss_password: 'psk', ss_iv_check: true })
    expect(onGenSSPassword).toHaveBeenCalled()
  })

  it('AnyTLS, TUIC and Naive: their own options and the shared TLS block', async () => {
    const { unmount } = await mountForm({ protocol: 'anytls', vless_security: 'tls', panel_id: 2 }, { server: sui() })
    type('Padding scheme (one per line)', 'stop=8')
    expect(state.anytls_padding_scheme_text).toBe('stop=8')
    type('TLS serverName', 'sni.example')
    expect(state.tls_server_name).toBe('sni.example')
    unmount()
    await mountForm({ protocol: 'tuic', vless_security: 'tls', panel_id: 2 }, { server: sui() })
    // Negative or unreadable values are held at zero.
    type('Auth timeout (s)', '-5')
    expect(state.tuic_auth_timeout).toBe(0)
    type('Heartbeat (s)', '10')
    expect(state.tuic_heartbeat).toBe(10)
    pick('Congestion control', 'bbr')
    fireEvent.click(screen.getByRole('switch', { name: 'Zero-RTT handshake' }))
    type('ALPN', 'h3')
    pick('uTLS fingerprint', 'chrome')
    fireEvent.click(screen.getByRole('switch', { name: /Skip certificate verification/ }))
    expect(state).toMatchObject({ tuic_congestion_control: 'bbr', tuic_zero_rtt_handshake: true, tls_alpn_text: 'h3', tls_fingerprint: 'chrome', tls_allow_insecure: true })
  })

  it('Naive: network and QUIC congestion control', async () => {
    await mountForm({ protocol: 'naive', vless_security: 'tls', panel_id: 2 }, { server: sui() })
    pick('Network', 'udp')
    pick('QUIC congestion control', 'bbr2')
    expect(state).toMatchObject({ naive_network: 'udp', naive_quic_congestion_control: 'bbr2' })
  })
})

describe('advanced section', () => {
  it('is collapsed, and holds the socket options and sniffing', async () => {
    await mountForm()
    const toggle = screen.getByRole('button', { name: 'Advanced' })
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    fireEvent.click(screen.getByRole('switch', { name: 'Enable sniffing', hidden: true }))
    type('destOverride (comma-separated)', 'http,tls')
    fireEvent.click(screen.getByRole('switch', { name: 'Metadata only', hidden: true }))
    fireEvent.click(screen.getByRole('switch', { name: 'Route only', hidden: true }))
    expect(state).toMatchObject({ sniffing_enabled: !EMPTY_INBOUND.sniffing_enabled, sniffing_dest_override_text: 'http,tls', sniffing_metadata_only: !EMPTY_INBOUND.sniffing_metadata_only, sniffing_route_only: !EMPTY_INBOUND.sniffing_route_only })
  })

  it('shows the socket options once they are enabled', async () => {
    await mountForm()
    expect(screen.queryByLabelText('SO_MARK')).toBeNull()
    fireEvent.click(screen.getByRole('switch', { name: 'Enable sockopt', hidden: true }))
    type('SO_MARK', '255')
    type('KeepAlive interval (s)', '30')
    type('KeepAlive idle (s)', '60')
    type('TCP user timeout (ms)', '10000')
    pick('TPROXY mode', 'tproxy')
    fireEvent.click(screen.getAllByRole('switch', { name: 'Accept Proxy Protocol', hidden: true }).at(-1)!)
    fireEvent.click(screen.getByRole('switch', { name: 'TCP Fast Open', hidden: true }))
    expect(state).toMatchObject({
      sockopt_enabled: true, sockopt_mark: 255, sockopt_tcp_keep_alive_interval: 30, sockopt_tcp_keep_alive_idle: 60, sockopt_tcp_user_timeout: 10000,
      sockopt_tproxy: 'tproxy', sockopt_accept_proxy_protocol: true, sockopt_tcp_fast_open: true,
    })
  })
})

it('does not render the General tab\'s fields', async () => {
  await mountForm()
  for (const label of ['Display name', 'Address', 'Region', 'Tags']) expect(screen.queryByLabelText(label)).toBeNull()
  act(() => {})
})
