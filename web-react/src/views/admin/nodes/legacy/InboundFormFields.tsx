// AUTOIMPORT-BEGIN
import RealityTargetScannerDialog from '../../RealityTargetScannerDialog'
import TagsAutocomplete from '../../TagsAutocomplete'
import { ALPN_HY2, ALPN_TLS, CreateProtocol, InboundFormState, PROTOCOL_OPTIONS, SUI_ONLY_PROTOCOLS, defaultAlpn, serverHost } from '../inbound/inboundModel'
import { buildSniffing, buildStreamSettings, settingsBuilderFor } from '../inbound/inboundSettings'
import { type Cert, fetchPanelWebCert, listCerts } from '@/api/certs'
import { PanelType, Server } from '@/api/servers'
import { pushSnack } from '@/components/SnackbarHost'
import { xrayRequiresMLKEMFirst } from '@/utils/xrayReality'
import { Autocomplete, Box, Button, CircularProgress, FormControlLabel, MenuItem, Select, Switch, TextField, Typography, useTheme } from '@mui/material'
import { type Dispatch, type SetStateAction, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { SectionCtx } from './inboundSectionCtx'
import { streamSections, protocolSections, trailingSections } from './inboundSections'
// AUTOIMPORT-END
interface FieldsProps {
  form: InboundFormState
  setForm: Dispatch<SetStateAction<InboundFormState>>
  showMetadata: boolean
  servers?: Server[]
  server?: Server
  onGenKeys: () => void | Promise<void>
  onRandomRealitySite?: () => void
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
  // S-UI accepts the structured canonical subset only; raw JSON is a
  // 3X-UI escape hatch and must not be offered where it would be lossy.
  panelType?: PanelType
  allowRealityScan?: boolean
}

export function InboundFormFields({ form, setForm, showMetadata, servers, server, onGenKeys, onRandomRealitySite, onGenSSPassword, genKeysBusy, protocolReadonly, advanced, onSetAdvanced, allTags, scanSourceName, panelType, allowRealityScan }: FieldsProps) {
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
  const selectedServer = servers?.find(s => s.id === form.panel_id) ?? server
  const effectivePanelType = panelType ?? selectedServer?.panel_type ?? '3xui'
  const selectedXrayVersion = effectivePanelType === '3xui'
    ? selectedServer?.xray_version
    : effectivePanelType === 'psp' && selectedServer?.core_engine === 'xray'
      ? selectedServer.core_version ?? selectedServer.xray_version
      : undefined
  const realityFingerprintLocked = xrayRequiresMLKEMFirst(selectedXrayVersion)
  const protocolOptions = effectivePanelType === 'sui'
    ? PROTOCOL_OPTIONS
    : PROTOCOL_OPTIONS.filter(option => !SUI_ONLY_PROTOCOLS.has(option.value))
  const canUseAdvanced = effectivePanelType === '3xui'
  const canScanReality = allowRealityScan ?? selectedServer?.capabilities?.includes('reality.scan') ?? false
  useEffect(() => {
    listCerts().then(setManagedCerts).catch(() => {})
  }, [])
  useEffect(() => {
    if (!realityFingerprintLocked) return
    setForm(prev => prev.reality_fingerprint === 'chrome' ? prev : { ...prev, reality_fingerprint: 'chrome' })
  }, [realityFingerprintLocked, setForm])

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
  const switchControl = (
    label: string,
    checked: boolean,
    onChange: (c: boolean) => void,
    dangerous = false,
  ) => (
    <FormControlLabel
      label={label}
      control={<Switch size="small" checked={checked} onChange={(_, c) => onChange(c)} />}
      sx={{
        ml: 0,
        '& .MuiFormControlLabel-label': {
          ml: 1, fontSize: 13,
          color: dangerous ? md.error : undefined,
        },
      }}
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

  const ctx: SectionCtx = {
    form, update, setForm, advanced, t, md, sectionTitle, fieldLabel, switchControl, tlsCertFields, protocolLabel,
    effectivePanelType, realityFingerprintLocked, canScanReality, setRealityScannerOpen, onGenKeys, onRandomRealitySite,
    onGenSSPassword, genKeysBusy, fetchingPanelCert, fetchFromPanel, protocolReadonly, panelType,
  }

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
          {onSetAdvanced && canUseAdvanced && (
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
                  if (v.panel_type !== '3xui') onSetAdvanced?.(false)
                  // Switch panel and re-derive the Address default — but only
                  // when the field is still untouched (empty, or equal to the
                  // previously-selected server's host). A manual edit wins.
                  setForm(prev => {
                    const previousServer = servers.find(s => s.id === prev.panel_id)
                    const prevHost = serverHost(previousServer)
                    const next = { ...prev, panel_id: v.id }
                    if (!prev.display_name || prev.display_name === previousServer?.name) {
                      next.display_name = v.name
                    }
                    if (!prev.region || prev.region === previousServer?.region) next.region = v.region ?? ''
                    if (v.panel_type !== '3xui') {
                      if (next.vless_network === 'xhttp') next.vless_network = 'tcp'
                      next.enable = true
                      next.tcp_header_type = 'none'
                      next.tcp_accept_proxy_protocol = false
                      next.ws_accept_proxy_protocol = false
                      next.httpupgrade_accept_proxy_protocol = false
                      next.grpc_authority = ''
                      next.grpc_multi_mode = false
                      next.tls_reject_unknown_sni = false
                      // S-UI builds its own TLS block and never reads
                      // cipherSuites, so a value carried over from a 3X-UI
                      // server would sit in the snapshot advertising a
                      // restriction the panel does not apply.
                      next.tls_cipher_suites = ''
                      next.ss_iv_check = false
                      next.sockopt_enabled = false
                      next.sockopt_tproxy = ''
                    } else {
                      if (SUI_ONLY_PROTOCOLS.has(next.protocol)) {
                        next.protocol = 'vless'
                        next.vless_security = 'reality'
                        next.tls_alpn_text = defaultAlpn('vless')
                      }
                      if (next.vless_network === 'http') next.vless_network = 'tcp'
                    }
                    if (!prev.server_address || prev.server_address === prevHost) {
                      next.server_address = serverHost(v)
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
                  if (p === 'trojan' || SUI_ONLY_PROTOCOLS.has(p)) sec = 'tls'
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
              {protocolOptions.map(o => <MenuItem key={o.value} value={o.value}>{o.label}</MenuItem>)}
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
            {effectivePanelType === '3xui' && switchControl(t('admin:nodes.create_dialog.enable'), form.enable, c => update('enable', c))}
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

      {streamSections(ctx)}
      {protocolSections(ctx)}
      {trailingSections(ctx)}

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
