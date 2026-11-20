// AUTOIMPORT-BEGIN
import { SetupEntries, type SetupRelay, SetupReview } from '../../NodeSetupFields'
import RoutesSection from '../../routes/RoutesSection'
import { type DraftRoute, draftIssues, inputOf } from '../../routes/routeDraft'
import { EMPTY_INBOUND, InboundFormState, SS2022_METHODS, newRelayKey, randomBase64, serverHost, splitList } from '../inbound/inboundModel'
import { buildSniffing, buildStreamSettings, canonicalProtocolName, settingsBuilderFor, validateInboundForm } from '../inbound/inboundSettings'
import { type CreateInboundRequest, type NodeSetupOptions, type NodeSetupPlan, createInbound, generateRealityKeypair, previewNodeSetup } from '@/api/nodes'
import { PanelCapability, Server } from '@/api/servers'
import { pushSnack } from '@/components/SnackbarHost'
import { randomRealitySite } from '@/utils/xrayReality'
import { Alert, Box, Button, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, MenuItem, Step, StepLabel, Stepper, TextField, useTheme } from '@mui/material'
import { type FormEvent, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { InboundFormFields } from './InboundFormFields'
// AUTOIMPORT-END
// The legacy create-node wizard (inbound, entries, exit, review), moved out of the old NodesView.tsx with its
// behaviour unchanged. It stays on MUI until N3 replaces it with the create page. The host (LegacyEditors) mounts
// it only when a writable server exists, so the landing server it prefills from is always there.
// AUTOIMPORT-BEGIN
// AUTOIMPORT-END

export interface CreateNodeProps {
  servers: Server[]
  allTags: string[]
  onClose: () => void
  onCreated: () => void
}

export default function CreateNodeDialog({ servers, allTags, onClose, onCreated }: CreateNodeProps) {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])
  const writableServers = useMemo(
    () => servers.filter(s => s.capabilities?.includes('inbound.create') && s.capabilities?.includes('inbound.delete')),
    [servers],
  )
  // Entry servers a managed relay line may name: every Passwall Node server (the landing is excluded where used).
  const relayEntryServers = useMemo(() => servers.filter(s => s.panel_type === 'psp'), [servers])
  const panelSupports = (panelID: number, capability: PanelCapability) =>
    !!servers.find(s => s.id === panelID)?.capabilities?.includes(capability)
  const [createBusy, setCreateBusy] = useState(false)
  const [createForm, setCreateForm] = useState<InboundFormState>(() => {
    const landing = writableServers[0]
    const site = randomRealitySite()
    return {
      ...EMPTY_INBOUND, panel_id: landing.id, server_address: serverHost(landing), display_name: landing.name,
      region: landing.region ?? '', reality_dest: `${site}:443`, reality_server_names_text: site,
    }
  })
  const [genKeysBusy, setGenKeysBusy] = useState(false)
  const createKeyIntent = useRef(0)
  const [setupStep, setSetupStep] = useState(0)
  const [setupEntriesBusy, setSetupEntriesBusy] = useState(false)
  const [setupDirect, setSetupDirect] = useState(true)
  const [setupEntries, setSetupEntries] = useState<SetupRelay[]>([])
  const [setupEgress, setSetupEgress] = useState(0)
  const [setupPlan, setSetupPlan] = useState<NodeSetupPlan | null>(null)
  const [setupLoopbacks, setSetupLoopbacks] = useState<Record<number, number>>({})
  const [setupLinkPorts, setSetupLinkPorts] = useState<Record<string, number>>({})
  const setupPortsChanged = Object.keys(setupLoopbacks).length > 0 || Object.keys(setupLinkPorts).length > 0
  const [setupError, setSetupError] = useState('')
  const [setupRoutes, setSetupRoutes] = useState<DraftRoute[]>([])
  const [recentSetupEntries, setRecentSetupEntries] = useState<number[]>([])
  const [createAdvanced, setCreateAdvanced] = useState(false)
  function randomizeLandingSite() {
    setCreateForm(previous => {
      const site = randomRealitySite(splitList(previous.reality_server_names_text))
      return { ...previous, reality_dest: `${site}:443`, reality_server_names_text: site }
    })
  }
  function closeCreate() {
    createKeyIntent.current += 1
    setGenKeysBusy(false)
    onClose()
  }

  async function genKeys() {
    const intent = ++createKeyIntent.current
    setGenKeysBusy(true)
    try {
      const kp = await generateRealityKeypair()
      if (intent !== createKeyIntent.current) return
      setCreateForm(f => ({
        ...f,
        private_key: kp.private_key,
        public_key: kp.public_key,
        short_ids_text: kp.short_id,
      }))
    } catch {
      // The request layer already surfaced the failure.
    } finally {
      if (intent === createKeyIntent.current) setGenKeysBusy(false)
    }
  }

  function genSSPasswordCreate() {
    const method = SS2022_METHODS.find(m => m.value === createForm.ss_method)
    setCreateForm(f => ({ ...f, ss_password: randomBase64(method?.bytes ?? 32) }))
  }

  function validateSetupLanding() {
    const f = createForm
    const panelType = servers.find(s => s.id === f.panel_id)?.panel_type ?? '3xui'
    if (createAdvanced) {
      // Advanced mode skips the structured validator (which checks
      // fields like reality_dest that the raw JSON path doesn't care
      // about). We only require the metadata + JSON parses below.
      if (!f.display_name || !f.server_address || !f.region) {
        pushSnack(t('admin:nodes.create_dialog.validate_required'), 'warning'); return false
      }
      for (const [label, raw] of [['settings', f.raw_settings], ['streamSettings', f.raw_stream_settings], ['sniffing', f.raw_sniffing]] as const) {
        try { JSON.parse(raw || '{}') }
        catch { pushSnack(t('admin:nodes.create_dialog.advanced_invalid_json', { field: label, defaultValue: `${label} 不是合法的 JSON` }), 'warning'); return false }
      }
    } else {
      const err = validateInboundForm(f, panelType, t)
      if (err) { pushSnack(err, 'warning'); return false }
    }

    return true
  }

  function setupRequest(options: NodeSetupOptions): CreateInboundRequest {
    const f = createForm
    const panelType = servers.find(s => s.id === f.panel_id)?.panel_type ?? '3xui'
    const settings = createAdvanced
      ? (f.raw_settings || '{}')
      : JSON.stringify(settingsBuilderFor(f.protocol)(f))
    const streamSettings = createAdvanced
      ? (f.raw_stream_settings || '{}')
      : JSON.stringify(buildStreamSettings(f))
    const sniffing = createAdvanced
      ? (f.raw_sniffing || '{}')
      : panelType === '3xui' ? JSON.stringify(buildSniffing(f)) : '{}'

    return {
      panel_id: f.panel_id,
      display_name: f.display_name,
      server_address: f.server_address,
      flow: f.protocol === 'vless' ? f.vless_flow.trim() : '',
      region: f.region,
      tags: f.tags_text ? f.tags_text.split(',').map(s => s.trim()).filter(Boolean) : [],
      sort_order: f.sort_order,
      cert_source: f.tls_cert_mode === 'psp_managed' && f.cert_id > 0 ? 'psp_managed' : undefined,
      cert_id: f.tls_cert_mode === 'psp_managed' && f.cert_id > 0 ? f.cert_id : undefined,
      relays: setupEntries.map(({ line }) => ({
        ...line, name: line.name.trim(), address: line.address.trim(), domain: line.domain?.trim(),
      })),
      hide_direct: !setupDirect,
      egress_panel_id: setupEgress,
      setup: options,
      inbound: {
        remark: f.display_name,
        enable: panelType === '3xui' ? f.enable : true,
        listen: f.listen,
        port: f.port,
        protocol: canonicalProtocolName(f.protocol),
        settings,
        stream_settings: streamSettings,
        sniffing,
        allocate: '',
        expiry_time: 0,
      },
    }
  }

  async function submitCreate(e: FormEvent) {
    e.preventDefault()
    if (createBusy || genKeysBusy || setupEntriesBusy) return
    setSetupError('')
    if (setupStep === 0) {
      if (validateSetupLanding()) {
        setSetupEntries(entries => entries.filter(entry => entry.line.entry_panel_id !== createForm.panel_id))
        if (setupEgress === createForm.panel_id || servers.find(s => s.id === createForm.panel_id)?.panel_type !== 'psp') {
          setSetupEgress(0)
        }
        if (servers.find(s => s.id === createForm.panel_id)?.panel_type !== 'psp') setSetupRoutes([])
        setSetupStep(1)
      }
      return
    }
    if (setupStep === 1) {
      if (!setupDirect && setupEntries.length === 0) {
        setSetupError(t('admin:nodes.setup.needs_entry'))
        return
      }
      if (servers.find(s => s.id === createForm.panel_id)?.panel_type !== 'psp' && setupEntries.some(e => e.line.mode === 'tunnel')) {
        setSetupError(t('admin:nodes.setup.tunnel_landing'))
        return
      }
      setSetupStep(2)
      return
    }
    const tunnels = Object.fromEntries(setupEntries.flatMap(e => e.line.mode === 'tunnel' && e.line.entry_panel_id && e.tunnel
      ? [[e.line.entry_panel_id, e.tunnel]] : []))
    const shown = Object.fromEntries((setupPlan?.bindings ?? []).flatMap(b => b.loopback ? [[b.panel_id, b.loopback]] : []))
    const loopbacks = { ...shown, ...setupLoopbacks }
    const shownLinkPorts = Object.fromEntries((setupPlan?.bindings ?? []).flatMap(b => b.kind === 'link' && !b.reused
      ? [[`${b.peer_id}/${b.panel_id}`, b.port]] : []))
    const link_ports = { ...shownLinkPorts, ...setupLinkPorts }
    if (draftIssues(setupRoutes).length > 0) {
      setSetupError(t('admin:nodes.setup.invalid_routes'))
      return
    }
    const options = { loopbacks, tunnels, link_ports, routes: inputOf(setupRoutes) }
    setCreateBusy(true)
    try {
      if (setupStep === 2 || !setupPlan || setupPortsChanged) {
        const plan = await previewNodeSetup(setupRequest(options))
        setSetupPlan(plan)
        setSetupLoopbacks({})
        setSetupLinkPorts({})
        setSetupStep(3)
        return
      }
      await createInbound(setupRequest({ ...options, confirm: setupPlan.token }))
      const selected = setupEntries.flatMap(e => e.line.entry_panel_id ? [e.line.entry_panel_id] : [])
      setRecentSetupEntries(previous => [...new Set([...selected, ...previous])])
      pushSnack(t('admin:nodes.create_dialog.created'), 'success')
      closeCreate()
      onCreated()
    } catch (error) {
      const response = (error as { response?: { status?: number; data?: { error?: string } } }).response
      setSetupError(response?.data?.error ?? t('admin:nodes.setup.request_failed'))
      if (response?.status === 409 && setupStep === 3) {
        setSetupPlan(null)
        setSetupLoopbacks({})
        setSetupLinkPorts({})
      }
    } finally { setCreateBusy(false) }
  }

  // The wizard opens with a fresh key pair, as it did when it was a dialog on the list.
  useEffect(() => { void genKeys() }, []) // eslint-disable-line react-hooks/exhaustive-deps

  return (
      <Dialog open onClose={() => !createBusy && !setupEntriesBusy && closeCreate()}
        slotProps={{ paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 880, maxWidth: '95vw' } } }}>
        <DialogTitle>{t('admin:nodes.create')}</DialogTitle>
        <DialogContent dividers>
          <Stepper activeStep={setupStep} alternativeLabel sx={{ mb: 2 }}>
            {['landing', 'entries', 'exit', 'review'].map(step => (
              <Step key={step}><StepLabel>{t(`admin:nodes.setup.${step}`)}</StepLabel></Step>
            ))}
          </Stepper>
          {setupError && <Alert severity="error" sx={{ mb: 2 }}>{setupError}</Alert>}
          <Box component="form" id="create-form" onSubmit={submitCreate}>
            {setupStep === 0 && <InboundFormFields form={createForm} setForm={setCreateForm}
              showMetadata servers={writableServers}
              onGenKeys={genKeys} onGenSSPassword={genSSPasswordCreate} genKeysBusy={genKeysBusy}
              onRandomRealitySite={randomizeLandingSite}
              advanced={createAdvanced} onSetAdvanced={setCreateAdvanced} allTags={allTags}
              panelType={servers.find(s => s.id === createForm.panel_id)?.panel_type}
              allowRealityScan={panelSupports(createForm.panel_id, 'reality.scan')} />}
            {setupStep === 1 && <SetupEntries
              servers={relayEntryServers.filter(s => s.id !== createForm.panel_id)} recent={recentSetupEntries}
              canTunnel={servers.find(s => s.id === createForm.panel_id)?.panel_type === 'psp'}
              entries={setupEntries} onChange={setSetupEntries} useDirect={setupDirect} onDirectChange={setSetupDirect}
              hostOf={serverHost} newKey={newRelayKey} randomSite={randomRealitySite} onBusyChange={setSetupEntriesBusy}
              tcpTLS={createForm.vless_network === 'tcp' && createForm.vless_security !== 'none'} />}
            {setupStep === 2 && (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
                <TextField select fullWidth label={t('admin:nodes.setup.exit')} value={setupEgress}
                  disabled={servers.find(s => s.id === createForm.panel_id)?.panel_type !== 'psp'}
                  onChange={e => setSetupEgress(Number(e.target.value))}>
                  <MenuItem value={0}>{t('admin:nodes.setup.exit_local')}</MenuItem>
                  {relayEntryServers.filter(s => s.id !== createForm.panel_id).map(s => <MenuItem key={s.id} value={s.id}>{s.name}</MenuItem>)}
                </TextField>
                {servers.find(s => s.id === createForm.panel_id)?.panel_type === 'psp' &&
                  <Box component="details" sx={{ py: 1 }}>
                    <Box component="summary" sx={{ cursor: 'pointer', mb: 1 }}>{t('admin:nodes.routes.title')}</Box>
                    <RoutesSection nodeId={0} entryPanelId={createForm.panel_id} pendingExit={setupEgress}
                      setup={{ rows: setupRoutes, setRows: setSetupRoutes }}
                      servers={relayEntryServers.filter(s => s.id !== createForm.panel_id).map(s => ({ id: s.id, name: s.name }))} />
                  </Box>}
              </Box>
            )}
            {setupStep === 3 && setupPlan && <SetupReview plan={setupPlan} entries={setupEntries} direct={setupDirect}
              routeCount={setupRoutes.length} peers={servers} landingPort={createForm.port} editingLoopbacks={setupLoopbacks}
              changed={setupPortsChanged} editingLinkPorts={setupLinkPorts}
              onLinkPort={(link, port) => setSetupLinkPorts(previous => ({ ...previous, [link]: port }))}
              onLoopback={(server, port) => setSetupLoopbacks(previous => ({ ...previous, [server]: port }))} />}
          </Box>
        </DialogContent>
        <DialogActions>
            <Button onClick={closeCreate} disabled={createBusy || setupEntriesBusy}>{t('common:actions.cancel')}</Button>
            {setupStep > 0 && <Button onClick={() => {
              setSetupStep(step => step - 1)
              setSetupPlan(null)
              setSetupLoopbacks({})
              setSetupLinkPorts({})
              setSetupError('')
            }} disabled={createBusy || setupEntriesBusy}>{t('admin:nodes.setup.back')}</Button>}
            <Button type="submit" form="create-form" variant="contained" disabled={createBusy || genKeysBusy || setupEntriesBusy}
              startIcon={createBusy ? <CircularProgress size={16} color="inherit" /> : null}>
              {t(setupStep < 2 ? 'admin:nodes.setup.next'
                : setupStep === 2 || !setupPlan || setupPortsChanged
                  ? 'admin:nodes.setup.review' : 'admin:nodes.setup.confirm')}
            </Button>
        </DialogActions>
      </Dialog>
  )
}
