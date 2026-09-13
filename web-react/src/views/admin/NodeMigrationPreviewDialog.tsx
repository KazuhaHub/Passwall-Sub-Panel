import { useEffect, useRef, useState } from 'react'
import { Alert, Box, Button, Checkbox, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, Stack, TextField, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { createNodeMigrationCommand, getNodeMigrationPreview, type NativeInstallationSelection, type NodeMigrationIssue, type NodeMigrationPreview, type Server } from '@/api/servers'
import NodeReleaseSelector from '@/components/NodeReleaseSelector'
import { copyToClipboard } from '@/utils/clipboard'
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

export function NodeMigrationPreviewDialog({ server, onClose }: { server: Server | null; onClose: () => void }) {
  const { t } = useTranslation(['admin', 'common'])
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
      setError('admin:servers.migration.unsupported_server')
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

  return <Dialog open={!!server} onClose={closeDialog} fullWidth maxWidth="md">
    <DialogTitle>{t('admin:servers.install_reinstall.title', { name: server?.name ?? '' })}</DialogTitle>
    <DialogContent>
      <Stack spacing={2} sx={{ pt: 1 }}>
        <Alert severity="info">{t('admin:servers.migration.online_hint')}</Alert>
        <Typography variant="body2">{t('admin:servers.migration.preserved')}</Typography>
        <Typography variant="body2">{t('admin:servers.migration.scope')}</Typography>
        <Alert severity="warning">{t('admin:servers.migration.supported_deployment_hint')}</Alert>
        {supported && <NodeReleaseSelector key={serverID} enabled={!!server} selection={installationSelection} value={version}
          onChange={next => { clearCommand(); setReleaseChoice({ serverID, version: next }) }} />}
        {loading && <Box role="status" sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <CircularProgress size={18} /><Typography variant="body2">{t('admin:servers.migration.loading')}</Typography>
        </Box>}
        {error && <Alert severity="error">{t(error)}</Alert>}
        {preview && canRead && <>
          <Typography variant="body2">{t('admin:servers.migration.summary', {
            server: preview.server_id, nodes: preview.node_count, clients: preview.client_count, core: preview.core_version,
          })}</Typography>
          {preview.blockers.length > 0 && <Alert severity="error">
            <Typography variant="subtitle2">{t('admin:servers.migration.blockers')}</Typography>
            {preview.blockers.map((issue, index) => <Typography variant="body2" key={`${issue.code}:${index}`}>{issueMessage(issue)}</Typography>)}
          </Alert>}
          {preview.warnings.length > 0 && <Alert severity="warning">
            <Typography variant="subtitle2">{t('admin:servers.migration.warnings')}</Typography>
            {preview.warnings.map((issue, index) => <Typography variant="body2" key={`${issue.code}:${index}`}>{issueMessage(issue)}</Typography>)}
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
            <Alert severity="success">{t('admin:servers.migration.ready')}</Alert>
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
          <Typography variant="body2">{t('admin:servers.migration.node_command_hint')}</Typography>
          <Alert severity="warning">{t('admin:servers.native.private_warning')}</Alert>
          <Button variant="contained" sx={{ alignSelf: 'flex-start' }} disabled={!canGenerate || commandBusy}
            startIcon={commandBusy ? <CircularProgress size={16} color="inherit" /> : undefined}
            onClick={() => void generateCommand()}>{t('admin:servers.migration.generate_node_command')}</Button>
          {commandError && <Alert severity="error">{t(commandError)}</Alert>}
          {command && <>
            {!command.expired && <TextField fullWidth multiline label={t('admin:servers.native.install_command')} value={command.command}
              autoComplete="off" slotProps={{ input: { readOnly: true } }} sx={{ '& textarea': { fontFamily: 'monospace', fontSize: 13 } }} />}
            <Alert severity={command.expired ? 'warning' : 'info'}>{t(command.expired
              ? 'admin:servers.native.command_expired' : 'admin:servers.native.command_expires', { time: command.expires_at })}</Alert>
            <Button variant="outlined" sx={{ alignSelf: 'flex-start' }} disabled={command.expired || !canGenerate}
              onClick={() => {
                if (!canGenerate || activeBinding.current !== command.binding || command.expired) return
                if (Date.parse(command.expires_at) <= Date.now()) {
                  setIssuedCommand({ ...command, command: '', expired: true })
                  return
                }
                void copyToClipboard(command.command)
              }}>{t('admin:servers.native.copy_command')}</Button>
          </>}
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
