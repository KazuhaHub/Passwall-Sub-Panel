import { useEffect, useState } from 'react'
import { Alert, Box, Button, Checkbox, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, Stack, TextField, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { getNodeMigrationPreview, type NodeMigrationIssue, type NodeMigrationPreview, type Server } from '@/api/servers'
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

function migrationArguments(preview: NodeMigrationPreview, serverID: number, acknowledged: boolean): string | undefined {
  // The dialog is informational, but copied commands must still be safe in
  // any shell. Only bounded numeric IDs/version tokens/SHA-256 are inserted.
  if (!preview.can_migrate || preview.blockers.length !== 0 || preview.server_id !== serverID ||
    !Number.isSafeInteger(serverID) || serverID <= 0 || !coreToken(preview.core_version) ||
    !/^[a-f0-9]{64}$/.test(preview.fingerprint) ||
    (preview.core_requires_ack && (!acknowledged || !preview.allow_restricted_reality))) return undefined
  return `migrate-server --server-id ${serverID} --core-version ${preview.core_version} --expected-fingerprint ${preview.fingerprint}` +
    (preview.core_requires_ack && acknowledged ? ' --allow-restricted-reality' : '') +
    ' --all-psp-stopped --old-xray-stopped --managed-only --apply'
}

export function NodeMigrationPreviewDialog({ server, onClose }: { server: Server | null; onClose: () => void }) {
  const { t } = useTranslation(['admin', 'common'])
  const canRead = useCan('config.write')
  const serverID = server?.id
  const [preview, setPreview] = useState<NodeMigrationPreview | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  const [choice, setChoice] = useState<{ serverID?: number; core: string; acknowledge: boolean }>({ core: '', acknowledge: false })
  const selectedCore = choice.serverID === serverID ? choice.core : ''
  const acknowledged = choice.serverID === serverID && choice.acknowledge

  useEffect(() => {
    const controller = new AbortController()
    setPreview(null)
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
      if (result.server_id !== serverID || !Array.isArray(result.blockers) || !Array.isArray(result.warnings)) {
        throw new Error('Invalid migration preview')
      }
      setPreview(result)
    }).catch((reason: unknown) => {
      if (controller.signal.aborted) return
      const status = (reason as { response?: { status?: number } })?.response?.status
      setError(status === 403 ? 'admin:servers.migration.forbidden' : 'admin:servers.migration.failed')
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false)
    })
    return () => controller.abort()
  }, [serverID, server?.panel_type, canRead, selectedCore, acknowledged, retry])

  const args = canRead && server?.panel_type === '3xui' && preview && serverID ? migrationArguments(preview, serverID, acknowledged) : undefined
  const docker = args ? `docker compose stop YOUR_PSP_SERVICE &&\ndocker compose run --rm --no-deps YOUR_PSP_SERVICE ${args} &&\ndocker compose start YOUR_PSP_SERVICE` : ''

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

  return <Dialog open={!!server} onClose={onClose} fullWidth maxWidth="md">
    <DialogTitle>{t('admin:servers.migration.title', { name: server?.name ?? '' })}</DialogTitle>
    <DialogContent>
      <Stack spacing={2} sx={{ pt: 1 }}>
        <Alert severity="info">{t('admin:servers.migration.preview_only')}</Alert>
        <Typography variant="body2">{t('admin:servers.migration.preserved')}</Typography>
        <Typography variant="body2">{t('admin:servers.migration.scope')}</Typography>
        <Alert severity="warning">{t('admin:servers.migration.maintenance')}</Alert>
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
              setChoice({ serverID, core: selectedCore, acknowledge: checked })
            }} />}
            label={t('admin:servers.migration.ack_restricted_core')} />}
          {preview.recommended_core_version && coreToken(preview.recommended_core_version) && preview.recommended_core_version !== preview.core_version &&
            <Button variant="outlined" onClick={() => {
              setChoice({ serverID, core: preview.recommended_core_version!, acknowledge: false })
            }}>{t('admin:servers.migration.use_recommended_core', { version: preview.recommended_core_version })}</Button>}
          {args ? <>
            <Alert severity="success">{t('admin:servers.migration.ready')}</Alert>
            <Typography variant="body2">{t('admin:servers.migration.cli_instructions')}</Typography>
            <TextField fullWidth multiline label={t('admin:servers.migration.cli_command')} value={`psp ${args}`}
              slotProps={{ input: { readOnly: true } }} sx={{ '& textarea': { fontFamily: 'monospace', fontSize: 13 } }} />
            <Typography variant="body2">{t('admin:servers.migration.docker_instructions')}</Typography>
            <TextField fullWidth multiline label={t('admin:servers.migration.docker_commands')} value={docker}
              slotProps={{ input: { readOnly: true } }} sx={{ '& textarea': { fontFamily: 'monospace', fontSize: 13 } }} />
            <Typography variant="body2">{t('admin:servers.migration.after_restart')}</Typography>
          </> : preview.can_migrate && <Alert severity="error">{t('admin:servers.migration.invalid_command')}</Alert>}
        </>}
      </Stack>
    </DialogContent>
    <DialogActions>
      <Button onClick={() => setRetry(current => current + 1)} disabled={loading || !canRead || server?.panel_type !== '3xui'}>
        {t('common:actions.retry')}
      </Button>
      <Button onClick={onClose}>{t('common:actions.close')}</Button>
    </DialogActions>
  </Dialog>
}
