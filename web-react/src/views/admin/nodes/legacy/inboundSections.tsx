// AUTOIMPORT-BEGIN
import { TLSCipherSuitesSelect } from '../../TLSCipherSuitesSelect'
import { FINGERPRINTS, InboundFormState, SS2022Method, SS2022_METHODS, SUI_ONLY_PROTOCOLS, TCP_HEADER_TYPES, TLS_VERSIONS, TPROXY_MODES, VLESS_FLOWS, VLESS_NETWORKS, VLESS_SECURITIES, VlessNetwork, VlessSecurity, XHTTP_MODES, usesVlessStream } from './inboundModel'
import KeyIcon from '@mui/icons-material/VpnKey'
import { Box, Button, CircularProgress, MenuItem, Select, TextField, Typography } from '@mui/material'
// AUTOIMPORT-END
// The per-protocol sections of the legacy inbound form, moved out of InboundFormFields.tsx unchanged so
// neither file passes ~600 lines. Each takes the form's context (the state, the helpers and the
// translated labels the body of InboundFormFields computes) and returns the fragment that used to
// sit inline in its JSX. Retired with the legacy dialogs (N3).

import type { SectionCtx } from './inboundSectionCtx'

export function streamSections(c: SectionCtx) {
  const { form, update, setForm, advanced, t, md, sectionTitle, fieldLabel, switchControl, tlsCertFields, protocolLabel, effectivePanelType, realityFingerprintLocked, canScanReality, setRealityScannerOpen, onGenKeys, onRandomRealitySite, genKeysBusy } = c
  return (
    <>
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
                  {VLESS_NETWORKS
                    .filter(o => effectivePanelType === '3xui' ? o.value !== 'http' : o.value !== 'xhttp')
                    .map(o => <MenuItem key={o.value} value={o.value}>{o.label}</MenuItem>)}
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
          {form.vless_network === 'tcp' && effectivePanelType === '3xui' && (
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
                {effectivePanelType === '3xui' && switchControl(t('admin:nodes.create_dialog.accept_proxy_protocol'),
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
                {effectivePanelType === '3xui' && <TextField size="small" label={t('admin:nodes.create_dialog.grpc_authority')}
                  value={form.grpc_authority}
                  onChange={e => update('grpc_authority', e.target.value)}
                  sx={{ flex: '1 1 200px' }} />}
                {effectivePanelType === '3xui' && switchControl(t('admin:nodes.create_dialog.grpc_multi_mode'),
                  form.grpc_multi_mode,
                  c => update('grpc_multi_mode', c))}
              </Box>
            </Box>
          )}

          {(form.vless_network === 'httpupgrade' || form.vless_network === 'http') && (
            <Box>
              {sectionTitle(form.vless_network === 'http'
                ? 'HTTP'
                : t('admin:nodes.create_dialog.section_httpupgrade', { defaultValue: 'HTTPUpgrade' }))}
              <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'center' }}>
                <TextField size="small" label={t('admin:nodes.create_dialog.ws_path', { defaultValue: 'Path' })}
                  value={form.httpupgrade_path}
                  onChange={e => update('httpupgrade_path', e.target.value)}
                  sx={{ flex: '1 1 200px' }} />
                <TextField size="small" label={t('admin:nodes.create_dialog.ws_host', { defaultValue: 'Host' })}
                  value={form.httpupgrade_host}
                  onChange={e => update('httpupgrade_host', e.target.value)}
                  sx={{ flex: '1 1 200px' }} />
                {form.vless_network === 'httpupgrade' && effectivePanelType === '3xui' && switchControl(t('admin:nodes.create_dialog.accept_proxy_protocol'),
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
                {effectivePanelType === '3xui' && <TLSCipherSuitesSelect
                  label={t('admin:nodes.create_dialog.tls_cipher_suites')}
                  value={form.tls_cipher_suites}
                  onChange={value => update('tls_cipher_suites', value)}
                  helperText={t('admin:nodes.create_dialog.tls_cipher_suites_hint')} />}
                <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                  {switchControl(t('admin:nodes.create_dialog.tls_allow_insecure', { defaultValue: '允许不安全连接（危险！）' }),
                    form.tls_allow_insecure,
                    c => update('tls_allow_insecure', c),
                    true)}
                  {effectivePanelType === '3xui' && switchControl(t('admin:nodes.create_dialog.tls_reject_unknown_sni', { defaultValue: 'Reject unknown SNI' }),
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
                    {onRandomRealitySite && <Button type="button" size="small" variant="outlined"
                      onClick={onRandomRealitySite} sx={{ whiteSpace: 'nowrap' }}>
                      {t('admin:nodes.egress.random_site')}
                    </Button>}
                    <Button type="button" size="small" variant="outlined"
                      disabled={!form.panel_id || !canScanReality}
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
                    disabled={realityFingerprintLocked}
                    onChange={e => update('reality_fingerprint', e.target.value)}
                    helperText={realityFingerprintLocked ? t('admin:nodes.create_dialog.reality_fingerprint_mlkem_locked') : undefined}
                    sx={{ flex: '1 1 180px', minWidth: 140 }}>
                    {FINGERPRINTS.map(fp => <MenuItem key={fp} value={fp}>{fp}</MenuItem>)}
                  </TextField>
                  {effectivePanelType === '3xui' && <TextField size="small" label={t('admin:nodes.create_dialog.reality_spider_x')}
                    value={form.reality_spider_x}
                    onChange={e => update('reality_spider_x', e.target.value)}
                    sx={{ flex: '1 1 180px' }} />}
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
                {/* These fields gate the client's self-reported Xray version.
                    Xray 26.7.11–26.7.28 defaults empty minClientVer to 26.3.27;
                    26.9.8+ leaves the floor disabled when empty. Explicit 1.0.0
                    avoids the older cores' default floor, while REALITY's
                    ML-KEM requirements apply independently. */}
                {effectivePanelType === '3xui' && <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
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
                </Box>}
              </Box>
            </Box>
          )}
        </>
      )}

    </>
  )
}

export function protocolSections(c: SectionCtx) {
  const { form, update, advanced, t, sectionTitle, fieldLabel, switchControl, tlsCertFields, protocolLabel, effectivePanelType } = c
  return (
    <>
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
            {effectivePanelType === '3xui' && <TLSCipherSuitesSelect
              label={t('admin:nodes.create_dialog.tls_cipher_suites')}
              value={form.tls_cipher_suites}
              onChange={value => update('tls_cipher_suites', value)}
              helperText={t('admin:nodes.create_dialog.tls_cipher_suites_hint')} />}
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

      {!advanced && SUI_ONLY_PROTOCOLS.has(form.protocol) && (
        <Box>
          {sectionTitle(`${protocolLabel} · S-UI / sing-box`)}
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.25 }}>
            {form.protocol === 'anytls' && (
              <TextField size="small" fullWidth multiline minRows={5} maxRows={12}
                label="Padding scheme（每行一条）"
                value={form.anytls_padding_scheme_text}
                onChange={e => update('anytls_padding_scheme_text', e.target.value)}
                helperText="留空时使用 S-UI 的 AnyTLS 默认 padding scheme。"
                sx={{ '& textarea': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 12 } }} />
            )}
            {form.protocol === 'tuic' && (
              <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'center' }}>
                <TextField select size="small" label="Congestion control"
                  value={form.tuic_congestion_control}
                  onChange={e => update('tuic_congestion_control', e.target.value as InboundFormState['tuic_congestion_control'])}
                  sx={{ minWidth: 190 }}>
                  {['cubic', 'new_reno', 'bbr'].map(value => <MenuItem key={value} value={value}>{value}</MenuItem>)}
                </TextField>
                <TextField size="small" type="number" label="Auth timeout（秒）"
                  value={form.tuic_auth_timeout}
                  onChange={e => update('tuic_auth_timeout', Math.max(0, Number(e.target.value) || 0))}
                  sx={{ width: 170 }} />
                <TextField size="small" type="number" label="Heartbeat（秒）"
                  value={form.tuic_heartbeat}
                  onChange={e => update('tuic_heartbeat', Math.max(0, Number(e.target.value) || 0))}
                  sx={{ width: 170 }} />
                {switchControl('Zero-RTT handshake', form.tuic_zero_rtt_handshake,
                  checked => update('tuic_zero_rtt_handshake', checked))}
              </Box>
            )}
            {form.protocol === 'naive' && (
              <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
                <TextField select size="small" label="Network"
                  value={form.naive_network}
                  onChange={e => update('naive_network', e.target.value as InboundFormState['naive_network'])}
                  sx={{ minWidth: 170 }}>
                  <MenuItem value="">TCP + UDP（默认）</MenuItem>
                  <MenuItem value="tcp">TCP</MenuItem>
                  <MenuItem value="udp">UDP</MenuItem>
                </TextField>
                <TextField select size="small" label="QUIC congestion control"
                  value={form.naive_quic_congestion_control}
                  onChange={e => update('naive_quic_congestion_control', e.target.value as InboundFormState['naive_quic_congestion_control'])}
                  sx={{ minWidth: 230 }}>
                  <MenuItem value="">不启用 QUIC</MenuItem>
                  {['bbr', 'bbr_standard', 'bbr2', 'bbr2_variant', 'cubic', 'reno'].map(value =>
                    <MenuItem key={value} value={value}>{value}</MenuItem>)}
                </TextField>
              </Box>
            )}
            <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
              <TextField size="small" label="SNI" value={form.tls_server_name}
                onChange={e => update('tls_server_name', e.target.value)} sx={{ flex: '2 1 240px' }} />
              <TextField size="small" label="ALPN" value={form.tls_alpn_text}
                onChange={e => update('tls_alpn_text', e.target.value)} sx={{ flex: '1 1 180px' }} />
              <TextField select size="small" label="uTLS fingerprint" value={form.tls_fingerprint}
                onChange={e => update('tls_fingerprint', e.target.value)} sx={{ minWidth: 190 }}>
                <MenuItem value="">—</MenuItem>
                {FINGERPRINTS.map(value => <MenuItem key={value} value={value}>{value}</MenuItem>)}
              </TextField>
              {switchControl(t('admin:nodes.create_dialog.tls_skip_cert_verify', { defaultValue: '跳过证书验证（危险！）' }),
                form.tls_allow_insecure,
                checked => update('tls_allow_insecure', checked),
                true)}
            </Box>
            {tlsCertFields()}
          </Box>
        </Box>
      )}

    </>
  )
}

export function trailingSections(c: SectionCtx) {
  const { form, update, advanced, t, sectionTitle, switchControl, effectivePanelType, onGenSSPassword } = c
  return (
    <>
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
            {effectivePanelType === '3xui' && switchControl(
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
      {!advanced && effectivePanelType === '3xui' && (
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
                {switchControl(t('admin:nodes.create_dialog.sockopt_accept_proxy_protocol'),
                  form.sockopt_accept_proxy_protocol,
                  c => update('sockopt_accept_proxy_protocol', c))}
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
      {!advanced && effectivePanelType === '3xui' && (
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

    </>
  )
}
