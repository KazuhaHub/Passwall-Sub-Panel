import { useEffect, useId, useRef, useState } from 'react'
import { Accordion, AccordionDetails, AccordionSummary, Alert, Box, Button, Checkbox, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, Stack, Typography } from '@mui/material'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import { Link as RouterLink } from 'react-router'
import { useTranslation } from 'react-i18next'
import { createNodeMigrationCommand, getNodeMigrationPreview, type NativeInstallationSelection, type NodeMigrationIssue, type NodeMigrationPreview, type Server } from '@/api/servers'
import NodeReleaseSelector from '@/components/NodeReleaseSelector'
import NodeInstallCommand from '@/components/NodeInstallCommand'
import { useCan } from '@/utils/permissions'

const knownIssues = new Set([
  'missing_snapshot', 'unsupported_protocol', 'inbound_expiry', 'external_files', 'fallback_dependency',
  'credential_unconfirmed', 'credential_mismatch', 'invalid_snapshot', 'missing_lifecycle', 'legacy_ownership',
  'cross_panel_attachment', 'core_not_verified', 'core_ack_required', 'core_version_changed', 'managed_scope',
  'source_not_3xui', 'duplicate_node', 'duplicate_inbound', 'duplicate_listener_binding', 'config_not_synced', 'endpoint_not_confirmed',
  'unsupported_flow', 'snapshot_contains_clients', 'missing_client', 'duplicate_client', 'invalid_credentials',
  'duplicate_username', 'missing_attachment', 'orphan_attachment', 'duplicate_attachment', 'flow_conflict',
  'global_config_dependency', 'fallback_environment_dependency', 'socket_environment_dependency',
  'connection_limits_not_enforced', 'restricted_core', 'reality_compatibility_normalization',
  'unsafe_reality_finalmask_tcp',
])
const issueAliases: Record<string, string> = {
  legacy_ownership_pending: 'legacy_ownership', inbound_expiry_unsupported: 'inbound_expiry',
  invalid_config: 'invalid_snapshot', lifecycle_not_minted: 'missing_lifecycle',
  credential_not_confirmed: 'credential_unconfirmed', external_file_dependency: 'external_files',
  local_fallback_dependency: 'fallback_dependency',
}

function coreToken(version: string): boolean {
  return /^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$/.test(version)
}

function eligiblePreview(preview: NodeMigrationPreview, serverID: number, acknowledged: boolean): boolean {
  if (preview.can_migrate !== true || preview.blockers.length !== 0 || preview.server_id !== serverID ||
    !Number.isSafeInteger(serverID) || serverID <= 0 || !coreToken(preview.core_version) ||
    !/^[a-f0-9]{64}$/.test(preview.fingerprint) ||
    (preview.core_requires_ack && (!acknowledged || !preview.allow_restricted_reality))) return false
  return true
}

const installationSelection: NativeInstallationSelection = { method: 'linux', os: 'linux', arch: 'amd64' }
interface IssuedCommand {
  binding: string
  command: string
  expires_at: string
  expired: boolean
}

export function NodeMigrationPreviewDialog({ server, onClose, onRefreshServer, onNativeInstall }: {
  server: Server | null
  onClose: () => void
  onRefreshServer?: () => void
  onNativeInstall?: (server: Server) => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const detailsID = useId()
  const canRead = useCan('config.write')
  const serverID = server?.id
  const [loadedPreview, setLoadedPreview] = useState<{ binding: string; value: NodeMigrationPreview } | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  const [choice, setChoice] = useState<{ serverID?: number; core: string; acknowledge: boolean }>({ core: '', acknowledge: false })
  const [releaseChoice, setReleaseChoice] = useState<{ serverID?: number; version: string }>({ version: '' })
  const [confirmation, setConfirmation] = useState<{ serverID?: number; managedOnly: boolean; singleInstance: boolean }>({ managedOnly: false, singleInstance: false })
  const [issuedCommand, setIssuedCommand] = useState<IssuedCommand | null>(null)
  const [commandBusy, setCommandBusy] = useState(false)
  const [commandError, setCommandError] = useState('')
  const previewRequest = useRef<AbortController | null>(null)
  const commandRequest = useRef<AbortController | null>(null)
  const refreshServer = useRef(onRefreshServer)
  refreshServer.current = onRefreshServer
  const refreshedBinding = useRef('')
  const selectedCore = choice.serverID === serverID ? choice.core : ''
  const acknowledged = choice.serverID === serverID && choice.acknowledge
  const version = releaseChoice.serverID === serverID ? releaseChoice.version : ''
  const managedOnly = confirmation.serverID === serverID && confirmation.managedOnly
  const singleInstance = confirmation.serverID === serverID && confirmation.singleInstance
  const supported = canRead && server?.panel_type === '3xui'
  const previewBinding = `${serverID}:${server?.panel_type}:${canRead}:${selectedCore}:${acknowledged}:${retry}`
  // Bind rendered data as well as requests: a changed selection must hide old
  // previews/tickets in the render preceding effect cleanup, not one tick later.
  const preview = loadedPreview?.binding === previewBinding ? loadedPreview.value : null
  const commandBinding = `${previewBinding}:${preview?.fingerprint ?? ''}:${version}:${managedOnly}:${singleInstance}`
  const activeBinding = useRef(commandBinding)
  activeBinding.current = commandBinding
  const command = issuedCommand?.binding === commandBinding ? issuedCommand : null
  const ready = !!(supported && preview && serverID && eligiblePreview(preview, serverID, acknowledged))
  const canGenerate = ready && /^v[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$/.test(version) && managedOnly && singleInstance

  function clearCommand() {
    commandRequest.current?.abort()
    commandRequest.current = null
    setIssuedCommand(null)
    setCommandBusy(false)
    setCommandError('')
  }

  function closeDialog() {
    previewRequest.current?.abort()
    clearCommand()
    setLoadedPreview(null)
    setReleaseChoice({ version: '' })
    setConfirmation({ managedOnly: false, singleInstance: false })
    setChoice({ core: '', acknowledge: false })
    onClose()
  }

  useEffect(() => {
    setReleaseChoice({ serverID, version: '' })
    setConfirmation({ serverID, managedOnly: false, singleInstance: false })
    setChoice({ serverID, core: '', acknowledge: false })
  }, [serverID])

  useEffect(() => {
    clearCommand()
    return () => commandRequest.current?.abort()
  }, [commandBinding])

  useEffect(() => {
    if (!command || command.expired) return
    const currentCommand = command
    const remaining = Date.parse(currentCommand.expires_at) - Date.now()
    function expire() {
      // Remove the bearer-ticket material from memory as well as disabling copy.
      setIssuedCommand(current => current?.binding === currentCommand.binding ? { ...current, command: '', expired: true } : current)
    }
    if (remaining <= 0) { expire(); return }
    const timer = setTimeout(expire, Math.min(remaining, 2_147_483_647))
    return () => clearTimeout(timer)
  }, [command])

  useEffect(() => {
    const controller = new AbortController()
    previewRequest.current = controller
    setLoadedPreview(null)
    setError('')
    setLoading(false)
    if (!serverID) {
      setChoice({ core: '', acknowledge: false })
      return () => controller.abort()
    }
    if (!canRead) {
      setError('admin:servers.migration.forbidden')
      return () => controller.abort()
    }
    if (server?.panel_type !== '3xui') {
      setError(server?.panel_type === 'psp' ? 'admin:servers.migration.already_native' : 'admin:servers.migration.unsupported_server')
      return () => controller.abort()
    }
    setLoading(true)
    void getNodeMigrationPreview(serverID, controller.signal, {
      ...(selectedCore ? { core_version: selectedCore } : {}),
      ...(acknowledged ? { allow_restricted_reality: true } : {}),
    }).then(result => {
      if (controller.signal.aborted) return
      if (result.server_id !== serverID || !Array.isArray(result.blockers) || !Array.isArray(result.warnings) ||
        (selectedCore && result.core_version !== selectedCore)) {
        throw new Error('Invalid migration preview')
      }
      setLoadedPreview({ binding: previewBinding, value: result })
    }).catch((reason: unknown) => {
      if (controller.signal.aborted) return
      const status = (reason as { response?: { status?: number } })?.response?.status
      setError(status === 403 ? 'admin:servers.migration.forbidden' : 'admin:servers.migration.failed')
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false)
    })
    return () => controller.abort()
  }, [serverID, server?.panel_type, canRead, selectedCore, acknowledged, retry, previewBinding])

  useEffect(() => {
    // A previous conversion may have changed this same record while the dialog
    // was open. Refresh the live row once, never treat a stale 3X-UI snapshot as
    // authority to issue another conversion ticket.
    if (preview?.blockers.some(issue => issue.code === 'source_not_3xui') && refreshedBinding.current !== previewBinding) {
      refreshedBinding.current = previewBinding
      refreshServer.current?.()
    }
  }, [preview, previewBinding])

  async function generateCommand() {
    if (!canGenerate || !preview || !serverID || commandBusy || activeBinding.current !== commandBinding) return
    clearCommand()
    const controller = new AbortController()
    const binding = commandBinding
    commandRequest.current = controller
    setCommandBusy(true)
    try {
      const result = await createNodeMigrationCommand(serverID, {
        version, fingerprint: preview.fingerprint, core_version: preview.core_version,
        allow_restricted_reality: preview.core_requires_ack && acknowledged && preview.allow_restricted_reality,
        managed_only: true, confirm_single_instance: true,
      }, controller.signal)
      if (controller.signal.aborted || commandRequest.current !== controller || activeBinding.current !== binding) return
      if (result.server_id !== serverID || typeof result.command !== 'string' || !result.command.trim() ||
        result.command.length > 8192 || /[\0\r\n]/.test(result.command) ||
        typeof result.expires_at !== 'string' || !Number.isFinite(Date.parse(result.expires_at)) ||
        Date.parse(result.expires_at) <= Date.now()) throw new Error('Invalid migration command')
      setIssuedCommand({ binding, command: result.command, expires_at: result.expires_at, expired: false })
    } catch (reason: unknown) {
      if (controller.signal.aborted || commandRequest.current !== controller || activeBinding.current !== binding) return
      const status = (reason as { response?: { status?: number } })?.response?.status
      setCommandError(status === 403 ? 'admin:servers.migration.forbidden' : 'admin:servers.migration.command_failed')
    } finally {
      if (!controller.signal.aborted && commandRequest.current === controller) setCommandBusy(false)
    }
  }

  function issueMessage(issue: NodeMigrationIssue) {
    const code = issueAliases[issue.code] ?? issue.code
    const message = knownIssues.has(code)
      ? t(`admin:servers.migration.issue.${code}`)
      : t('admin:servers.migration.issue.unknown', { code: issue.code })
    const references = [
      issue.node_id ? t('admin:servers.migration.node_reference', { id: issue.node_id }) : '',
      issue.client_id ? t('admin:servers.migration.client_reference', { id: issue.client_id }) : '',
    ].filter(Boolean)
    return references.length ? `${message} (${references.join(', ')})` : message
  }

  const criticalWarnings = preview?.warnings.filter(issue => ['restricted_core', 'reality_compatibility_normalization',
    'core_version_changed', 'connection_limits_not_enforced'].includes(issue.code)) ?? []
  const detailsWarnings = preview?.warnings.filter(issue => !criticalWarnings.includes(issue)) ?? []

  return <Dialog open={!!server} onClose={closeDialog} fullWidth maxWidth="md">
    <DialogTitle>{t('admin:servers.install_reinstall.title', { name: server?.name ?? '' })}</DialogTitle>
    <DialogContent>
      <Stack spacing={2} sx={{ pt: 1 }}>
        {supported && <Typography variant="body2" color="text.secondary">{t('admin:servers.migration.online_hint')}</Typography>}
        {supported && <NodeReleaseSelector compact key={serverID} enabled={!!server} selection={installationSelection} value={version}
          onChange={next => { clearCommand(); setReleaseChoice({ serverID, version: next }) }} />}
        {loading && <Box role="status" sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <CircularProgress size={18} /><Typography variant="body2">{t('admin:servers.migration.loading')}</Typography>
        </Box>}
        {error && <Alert severity={server?.panel_type === 'psp' && canRead ? 'info' : 'error'}>{t(error)}</Alert>}
        {server?.panel_type === 'psp' && canRead && onNativeInstall && <Button variant="contained" sx={{ alignSelf: 'flex-start' }}
          onClick={() => { closeDialog(); onNativeInstall(server) }}>{t('admin:servers.migration.continue_native_install')}</Button>}
        {error && server?.panel_type !== 'psp' && !supported && <Typography variant="body2">{t('admin:servers.migration.unsupported_next')}</Typography>}
        {preview && canRead && <>
          <Typography variant="body2">{t('admin:servers.migration.summary', {
            server: preview.server_id, nodes: preview.node_count, clients: preview.client_count, core: preview.core_version,
          })}</Typography>
          {preview.blockers.length > 0 && <Alert severity="error">
            <Typography variant="subtitle2">{t('admin:servers.migration.blockers')}</Typography>
            {preview.blockers.map((issue, index) => <Typography variant="body2" key={`${issue.code}:${index}`}>{issueMessage(issue)}</Typography>)}
          </Alert>}
          {preview.blockers.some(issue => issue.code === 'config_not_synced' || issue.code === 'missing_snapshot') &&
            <Button component={RouterLink} to="/admin/nodes" sx={{ alignSelf: 'flex-start' }} variant="outlined" onClick={closeDialog}>
              {t('admin:servers.migration.resolve_nodes')}
            </Button>}
          {preview.blockers.some(issue => issue.code === 'source_not_3xui') && <Typography variant="body2">
            {t('admin:servers.migration.source_changed_next')}
          </Typography>}
          {criticalWarnings.length > 0 && <Alert severity="warning">
            <Typography variant="subtitle2">{t('admin:servers.migration.warnings')}</Typography>
            {criticalWarnings.map((issue, index) => <Typography variant="body2" key={`${issue.code}:${index}`}>{issueMessage(issue)}</Typography>)}
          </Alert>}
          {preview.core_requires_ack && <FormControlLabel sx={{ mx: 0 }}
            control={<Checkbox checked={acknowledged} onChange={(_event, checked) => {
              clearCommand()
              setChoice({ serverID, core: selectedCore, acknowledge: checked })
            }} />}
            label={t('admin:servers.migration.ack_restricted_core')} />}
          {preview.recommended_core_version && coreToken(preview.recommended_core_version) && preview.recommended_core_version !== preview.core_version &&
            <Button variant="outlined" onClick={() => {
              clearCommand()
              setChoice({ serverID, core: preview.recommended_core_version!, acknowledge: false })
            }}>{t('admin:servers.migration.use_recommended_core', { version: preview.recommended_core_version })}</Button>}
          {ready ? <>
            <Typography variant="body2" color="success.main">{t('admin:servers.migration.ready')}</Typography>
            <Typography variant="body2">{t('admin:servers.migration.deployment_safety')}</Typography>
            <FormControlLabel sx={{ mx: 0 }} control={<Checkbox checked={managedOnly} onChange={(_event, checked) => {
              clearCommand()
              setConfirmation({ serverID, managedOnly: checked, singleInstance })
            }} />} label={t('admin:servers.migration.ack_managed_only')} />
            <FormControlLabel sx={{ mx: 0 }} control={<Checkbox checked={singleInstance} onChange={(_event, checked) => {
              clearCommand()
              setConfirmation({ serverID, managedOnly, singleInstance: checked })
            }} />} label={t('admin:servers.migration.single_instance_confirmation')} />
          </> : preview.can_migrate && <Alert severity="error">{t('admin:servers.migration.invalid_command')}</Alert>}
        </>}
        {supported && <>
          <Button variant="contained" sx={{ alignSelf: 'flex-start' }} disabled={!canGenerate || commandBusy}
            startIcon={commandBusy ? <CircularProgress size={16} color="inherit" /> : undefined}
            onClick={() => void generateCommand()}>{t('admin:servers.migration.generate_node_command')}</Button>
          {commandError && <Alert severity="error">{t(commandError)}</Alert>}
          {command && <NodeInstallCommand key={command.binding} command={command.command} expiresAt={command.expires_at}
            disabled={command.expired || !canGenerate} onExpired={() => setIssuedCommand({ ...command, command: '', expired: true })} />}
          {command && !command.expired && <Typography variant="body2">{t('admin:servers.migration.command_next')}</Typography>}
          <Accordion disableGutters elevation={0} slotProps={{ transition: { unmountOnExit: true } }}>
            <AccordionSummary id={detailsID} aria-controls={`${detailsID}-details`} expandIcon={<ExpandMoreIcon />}>
              <Typography variant="body2">{t('admin:servers.migration.details')}</Typography>
            </AccordionSummary>
            <AccordionDetails>
              <Stack spacing={1.5}>
                <Typography variant="body2">{t('admin:servers.migration.preserved')}</Typography>
                <Typography variant="body2">{t('admin:servers.migration.scope')}</Typography>
                <Typography variant="body2">{t('admin:servers.migration.supported_deployment_hint')}</Typography>
                <Typography variant="body2">{t('admin:servers.migration.node_command_hint')}</Typography>
                {detailsWarnings.map((issue, index) => <Typography variant="body2" key={`${issue.code}:${index}`}>{issueMessage(issue)}</Typography>)}
              </Stack>
            </AccordionDetails>
          </Accordion>
        </>}
      </Stack>
    </DialogContent>
    <DialogActions>
      <Button onClick={() => setRetry(current => current + 1)} disabled={loading || !canRead || server?.panel_type !== '3xui'}>
        {t('common:actions.retry')}
      </Button>
      <Button onClick={closeDialog}>{t('common:actions.close')}</Button>
    </DialogActions>
  </Dialog>
}
