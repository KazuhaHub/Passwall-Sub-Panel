import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { Link as RouterLink } from 'react-router'
import { useTranslation } from 'react-i18next'
import { createNodeMigrationCommand, getNodeMigrationPreview, type NativeInstallationSelection, type NodeMigrationIssue, type NodeMigrationPreview, type Server } from '@/api/servers'
import NodeInstallCommand from '../install/NodeInstallCommand'
import { Button, buttonVariants } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { IconChevronRight, IconSpinner } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { useCan } from '@/utils/permissions'
import { canonicalReleaseVersion } from '@/utils/productVersion'
import NodeReleaseSelector from '../install/NodeReleaseSelector'
import { Notice } from '@/components/common/Notice'

const knownIssues = new Set([
  'missing_snapshot', 'unsupported_protocol', 'inbound_expiry', 'external_files', 'fallback_dependency',
  'credential_unconfirmed', 'credential_mismatch', 'invalid_snapshot', 'missing_lifecycle', 'legacy_ownership',
  'cross_panel_attachment', 'core_not_verified', 'core_ack_required', 'core_version_changed', 'managed_scope',
  'source_not_3xui', 'duplicate_node', 'duplicate_inbound', 'duplicate_listener_binding', 'config_not_synced', 'endpoint_not_confirmed',
  'unsupported_flow', 'snapshot_contains_clients', 'missing_client', 'duplicate_client', 'invalid_credentials',
  'duplicate_username', 'orphan_attachment', 'duplicate_attachment', 'flow_conflict',
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
  const cancelRef = useRef<HTMLButtonElement>(null)
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
  // A VERSION, IN EITHER SCHEME, by the shared rule. The regex this replaced
  // knew only the legacy form, so a product release could be picked and then
  // left the last step of the migration permanently disabled. It was also
  // LOOSER than the rule it was standing in for — it accepted `v01.0.0` and any
  // prerelease spelling — so delegating tightens it as well as widening it.
  const canGenerate = ready && canonicalReleaseVersion(version) !== undefined && managedOnly && singleInstance

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

  const closeAfterNative = () => { if (server) { closeDialog(); onNativeInstall?.(server) } }

  return <Dialog open={!!server} onOpenChange={open => { if (!open) closeDialog() }}>
    {/* Wide, and the body scrolls on its own: a preview can list dozens of issues, and the title and the Retry/Close
        row must stay in reach. Focus opens on Close, so a stray Enter cannot start anything. */}
    <DialogContent showCloseButton={false} initialFocus={cancelRef}
      className="max-h-[calc(100dvh-2rem)] grid-rows-[auto_minmax(0,1fr)_auto] sm:max-w-2xl">
      <DialogHeader>
        <DialogTitle>{t('admin:servers.install_reinstall.title', { name: server?.name ?? '' })}</DialogTitle>
        {supported && <DialogDescription>{t('admin:servers.migration.online_hint')}</DialogDescription>}
      </DialogHeader>
      <div className="-mx-4 flex min-h-0 flex-col gap-4 overflow-y-auto px-4 pb-1">
        {supported && <NodeReleaseSelector compact key={serverID} enabled={!!server} selection={installationSelection} value={version}
          onChange={next => { clearCommand(); setReleaseChoice({ serverID, version: next }) }} />}
        {loading && <div role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
          <IconSpinner aria-hidden className="size-4 animate-spin" /><span>{t('admin:servers.migration.loading')}</span>
        </div>}
        {error && <Notice tone={server?.panel_type === 'psp' && canRead ? 'info' : 'danger'}>{t(error)}</Notice>}
        {server?.panel_type === 'psp' && canRead && onNativeInstall &&
          <Button className="self-start" onClick={closeAfterNative}>{t('admin:servers.migration.continue_native_install')}</Button>}
        {error && server?.panel_type !== 'psp' && !supported && <p className="text-sm">{t('admin:servers.migration.unsupported_next')}</p>}
        {preview && canRead && <>
          <p className="text-sm">{t('admin:servers.migration.summary', {
            server: preview.server_id, nodes: preview.node_count, clients: preview.client_count, core: preview.core_version,
          })}</p>
          {preview.blockers.length > 0 && <Notice tone="danger">
            <p className="font-medium">{t('admin:servers.migration.blockers')}</p>
            <ul className="mt-1 flex flex-col gap-1">
              {preview.blockers.map((issue, index) => <li key={`${issue.code}:${index}`}>{issueMessage(issue)}</li>)}
            </ul>
          </Notice>}
          {preview.blockers.some(issue => issue.code === 'config_not_synced' || issue.code === 'missing_snapshot') &&
            // A real link (role link, openable in a new tab), styled as the outline button beside it.
            <RouterLink to="/admin/nodes" className={buttonVariants({ variant: 'outline', className: 'self-start' })} onClick={closeDialog}>
              {t('admin:servers.migration.resolve_nodes')}
            </RouterLink>}
          {preview.blockers.some(issue => issue.code === 'source_not_3xui') && <p className="text-sm">
            {t('admin:servers.migration.source_changed_next')}
          </p>}
          {criticalWarnings.length > 0 && <Notice tone="warning">
            <p className="font-medium">{t('admin:servers.migration.warnings')}</p>
            <ul className="mt-1 flex flex-col gap-1">
              {criticalWarnings.map((issue, index) => <li key={`${issue.code}:${index}`}>{issueMessage(issue)}</li>)}
            </ul>
          </Notice>}
          {preview.core_requires_ack && <CheckLine checked={acknowledged} label={t('admin:servers.migration.ack_restricted_core')}
            onChange={checked => {
              clearCommand()
              setChoice({ serverID, core: selectedCore, acknowledge: checked })
            }} />}
          {preview.recommended_core_version && coreToken(preview.recommended_core_version) && preview.recommended_core_version !== preview.core_version &&
            <Button variant="outline" className="self-start" onClick={() => {
              clearCommand()
              setChoice({ serverID, core: preview.recommended_core_version!, acknowledge: false })
            }}>{t('admin:servers.migration.use_recommended_core', { version: preview.recommended_core_version })}</Button>}
          {ready ? <>
            <Notice tone="success">{t('admin:servers.migration.ready')}</Notice>
            <p className="text-sm">{t('admin:servers.migration.deployment_safety')}</p>
            <CheckLine checked={managedOnly} label={t('admin:servers.migration.ack_managed_only')} onChange={checked => {
              clearCommand()
              setConfirmation({ serverID, managedOnly: checked, singleInstance })
            }} />
            <CheckLine checked={singleInstance} label={t('admin:servers.migration.single_instance_confirmation')} onChange={checked => {
              clearCommand()
              setConfirmation({ serverID, managedOnly, singleInstance: checked })
            }} />
          </> : preview.can_migrate && <Notice tone="danger">{t('admin:servers.migration.invalid_command')}</Notice>}
        </>}
        {supported && <>
          {/* What the button does, with the server and the release named, before it is pressed. Issuing the command
              changes nothing on the server: the conversion starts only when it is run on the node host. */}
          {canGenerate && <p className="text-sm font-medium" data-testid="migration-summary">
            {t('admin:servers.migration.generate_summary', { name: server?.name ?? '', version })}
          </p>}
          <Button className="self-start" disabled={!canGenerate || commandBusy} onClick={() => void generateCommand()}>
            {commandBusy && <IconSpinner aria-hidden className="animate-spin" />}{t('admin:servers.migration.generate_node_command')}
          </Button>
          {commandError && <Notice tone="danger">{t(commandError)}</Notice>}
          {command && <NodeInstallCommand key={command.binding} command={command.command} expiresAt={command.expires_at}
            disabled={command.expired || !canGenerate} onExpired={() => setIssuedCommand({ ...command, command: '', expired: true })} />}
          {command && !command.expired && <p className="text-sm">{t('admin:servers.migration.command_next')}</p>}
          <Fold id={detailsID} label={t('admin:servers.migration.details')}>
            <div className="flex flex-col gap-3 text-sm">
              <p>{t('admin:servers.migration.preserved')}</p>
              <p>{t('admin:servers.migration.scope')}</p>
              <p>{t('admin:servers.migration.supported_deployment_hint')}</p>
              <p>{t('admin:servers.migration.node_command_hint')}</p>
              {detailsWarnings.map((issue, index) => <p key={`${issue.code}:${index}`}>{issueMessage(issue)}</p>)}
            </div>
          </Fold>
        </>}
      </div>
      <DialogFooter>
        <Button variant="outline" onClick={() => setRetry(current => current + 1)} disabled={loading || !canRead || server?.panel_type !== '3xui'}>
          {t('common:actions.retry')}
        </Button>
        <Button ref={cancelRef} variant="outline" onClick={closeDialog}>{t('common:actions.close')}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
}

// A confirmation the operator ticks: the sentence is the control's label, wired by id so the whole line is the target.
function CheckLine({ checked, label, onChange }: { checked: boolean; label: string; onChange: (checked: boolean) => void }) {
  const id = useId()
  return <div className="flex items-start gap-2.5 text-sm">
    <Checkbox id={id} checked={checked} onCheckedChange={next => onChange(next === true)} className="mt-0.5" />
    <label htmlFor={id} className="min-w-0 flex-1 leading-snug">{label}</label>
  </div>
}

// The compatibility details are folded: they are reference text, and the blockers and warnings that need action are
// shown above it regardless. Not mounted until opened.
function Fold({ id, label, children }: { id: string; label: string; children: ReactNode }) {
  const [open, setOpen] = useState(false)
  return <div className="rounded-lg border">
    <button type="button" id={id} aria-expanded={open} aria-controls={`${id}-details`} onClick={() => setOpen(v => !v)}
      className="flex min-h-(--control-h) w-full items-center gap-1.5 rounded-lg px-3 text-left text-sm font-medium outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
      <IconChevronRight aria-hidden className={cn('size-4 shrink-0 transition-transform', open && 'rotate-90')} />
      {label}
    </button>
    {open && <div id={`${id}-details`} role="region" aria-labelledby={id} className="border-t px-3 py-2.5">{children}</div>}
  </div>
}
