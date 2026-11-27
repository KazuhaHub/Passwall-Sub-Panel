import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  createNativeInstallationFiles, createNativeInstallScript, createNodeInstallCommand, getNativeAgentStatus, getNativeInstallation,
  importNativeCredential, type InstallMode, type NativeAgentStatus, type NativeInstallationFiles, type NativeInstallationSelection,
  type NativeServerProvisioning, type Server,
} from '@/api/servers'
import { copyToClipboard } from '@/utils/clipboard'
import { effectiveUpdateChannel } from '@/utils/nodeReleaseUpdate'
import { useCan } from '@/utils/permissions'
import {
  DEFAULT_INSTALLATION, downloadInstallationFile, installationErrorMessage, isCredentialUnavailable, isNodeDockerImageSelection,
  isNodeReleaseVersion, isOfficialNodeReleaseDownload, publicNodeInstallCommands,
} from './installModel'

/**
 * The installation of one Passwall Node server: its identity and credential, the chosen method and release, the
 * materials the panel generates (a one-time command, or files) and the agent's status.
 *
 * Installation secrets live only in this open flow. Reopening reads the same identity and credential; rotation is an
 * explicit, separate action (the caller's `onRotate`). EVERY REQUEST THAT RETURNS A SECRET IS ABORTABLE and is
 * aborted when the method, platform, release, options or server change, so a late response can never put material for
 * a different choice on screen.
 */
export function useNativeInstallation({ server, initialProvisioning, initialSelection = DEFAULT_INSTALLATION, rotating }: {
  server: Server | null
  initialProvisioning: NativeServerProvisioning | null
  initialSelection?: NativeInstallationSelection
  rotating: boolean
}) {
  const { t } = useTranslation(['admin', 'common'])
  const canConfigure = useCan('config.write')
  const [provisioning, setProvisioning] = useState<NativeServerProvisioning | null>(null)
  const [installationLoading, setInstallationLoading] = useState(false)
  const [installationError, setInstallationError] = useState('')
  const [credentialUnavailable, setCredentialUnavailable] = useState(false)
  const [oldCredential, setOldCredential] = useState('')
  const [credentialBusy, setCredentialBusy] = useState(false)
  const [credentialError, setCredentialError] = useState('')
  const [version, setVersion] = useState('')
  const [scriptBusy, setScriptBusy] = useState<'copy' | 'download' | ''>('')
  const [scriptError, setScriptError] = useState('')
  const [installCommand, setInstallCommand] = useState<{ command: string; expires_at: string } | null>(null)
  const [commandBusy, setCommandBusy] = useState(false)
  // REPLACING A RELEASE IS NEVER INFERRED. The node's reported version is shown
  // beside the choice so an operator can see the difference, and the warning says
  // what the installer will do about it; the panel does not decide for them.
  const [upgradeInPlace, setUpgradeInPlace] = useState(false)
  // AND NEITHER IS TAKING THE HOST OVER. This one is not about a version at all: it
  // hands the machine to this panel with a new identity, which is what a server
  // moving between installations needs. It is never inferred either — the version
  // says nothing about it — and what it costs is said beside it rather than after.
  const [replaceIdentity, setReplaceIdentity] = useState(false)
  const [commandError, setCommandError] = useState('')
  const [commandExpired, setCommandExpired] = useState(false)
  const [selection, setSelection] = useState<NativeInstallationSelection>(initialSelection)
  const [files, setFiles] = useState<NativeInstallationFiles | null>(null)
  const [filesBusy, setFilesBusy] = useState(false)
  const [filesError, setFilesError] = useState('')
  const [status, setStatus] = useState<NativeAgentStatus | null>(null)
  const [statusError, setStatusError] = useState('')
  const [installationReload, setInstallationReload] = useState(0)
  const credentialRequest = useRef<AbortController | null>(null)
  const scriptRequest = useRef<AbortController | null>(null)
  const filesRequest = useRef<AbortController | null>(null)
  const commandRequest = useRef<AbortController | null>(null)
  const serverID = server?.id
  // The node's own record of itself, first token only: the reported string may carry
  // a build stamp, and it is the release the installer compares, not the stamp.
  const reportedVersion = (server?.panel_version ?? '').split(' ')[0]
  const installMode: InstallMode = replaceIdentity ? 'replace' : upgradeInPlace ? 'upgrade' : 'install'
  const versionValid = selection.method === 'github' || (selection.method === 'docker'
    ? isNodeDockerImageSelection(version.trim())
    : isNodeReleaseVersion(version.trim()))
  const publicInstallCommands = publicNodeInstallCommands(effectiveUpdateChannel(server?.update_channel))

  useEffect(() => {
    const controller = new AbortController()
    setProvisioning(serverID === undefined ? null : initialProvisioning)
    setInstallationError('')
    setCredentialUnavailable(false)
    setOldCredential('')
    setCredentialError('')
    setCredentialBusy(false)
    setVersion('')
    setSelection(initialSelection)
    setFiles(null)
    setFilesBusy(false)
    setFilesError('')
    setScriptError('')
    setScriptBusy('')
    setInstallCommand(null)
    setCommandBusy(false)
    setCommandError('')
    setCommandExpired(false)
    setInstallationLoading(serverID !== undefined && !initialProvisioning)
    if (serverID !== undefined && !initialProvisioning) {
      void getNativeInstallation(serverID, controller.signal).then(data => {
        if (!controller.signal.aborted) setProvisioning(data)
      }).catch(error => {
        if (controller.signal.aborted) return
        if (isCredentialUnavailable(error)) setCredentialUnavailable(true)
        else setInstallationError(installationErrorMessage(error, t('admin:servers.native.installation_failed')))
      }).finally(() => {
        if (!controller.signal.aborted) setInstallationLoading(false)
      })
    }
    return () => {
      controller.abort()
      credentialRequest.current?.abort()
      scriptRequest.current?.abort()
      filesRequest.current?.abort()
      commandRequest.current?.abort()
    }
    // Request lifetime is scoped to this installation identity, not translated labels.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serverID, initialProvisioning, installationReload])

  useEffect(() => {
    if (!installCommand) return
    const remaining = Date.parse(installCommand.expires_at) - Date.now()
    setCommandExpired(remaining <= 0)
    if (remaining <= 0) return
    const timer = setTimeout(() => setCommandExpired(true), Math.min(remaining, 2_147_483_647))
    return () => clearTimeout(timer)
  }, [installCommand])

  // The agent's status, polled every four seconds while the flow is open: it is how the operator sees the
  // install they just ran on the host take effect.
  useEffect(() => {
    setStatus(null)
    setStatusError('')
    if (serverID === undefined) return
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    async function poll() {
      try {
        const next = await getNativeAgentStatus(serverID!, controller.signal)
        if (!controller.signal.aborted) {
          setStatus(next)
          setStatusError('')
        }
      } catch (error) {
        if (!controller.signal.aborted) setStatusError(installationErrorMessage(error, t('admin:servers.native.status_failed')))
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(() => void poll(), 4000)
      }
    }
    void poll()
    return () => { controller.abort(); if (timer !== undefined) clearTimeout(timer) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serverID])

  async function saveOldCredential() {
    if (!server || !oldCredential.trim() || credentialBusy) return
    const controller = new AbortController()
    credentialRequest.current = controller
    setCredentialBusy(true)
    setCredentialError('')
    try {
      await importNativeCredential(server.id, oldCredential.trim(), controller.signal)
      const data = await getNativeInstallation(server.id, controller.signal)
      if (!controller.signal.aborted) {
        setProvisioning(data)
        setCredentialUnavailable(false)
        setOldCredential('')
      }
    } catch (error) {
      if (!controller.signal.aborted) setCredentialError(installationErrorMessage(error, t('admin:servers.native.import_failed')))
    } finally {
      if (!controller.signal.aborted) setCredentialBusy(false)
    }
  }

  async function deliverScript(action: 'copy' | 'download') {
    if (!server || !provisioning || !versionValid || scriptBusy) return
    const controller = new AbortController()
    scriptRequest.current = controller
    setScriptBusy(action)
    setScriptError('')
    try {
      const script = await createNativeInstallScript(server.id, { version: version.trim(), mode: installMode }, controller.signal)
      if (controller.signal.aborted) return
      if (typeof script !== 'string' || !script.trim()) throw new Error(t('admin:servers.native.script_failed'))
      if (action === 'copy') await copyToClipboard(script)
      else downloadInstallationFile(`passwall-node-install-${provisioning.agent_id}.sh`, script, 'text/x-shellscript;charset=utf-8')
    } catch (error) {
      if (!controller.signal.aborted) setScriptError(installationErrorMessage(error, t('admin:servers.native.script_failed')))
    } finally {
      if (!controller.signal.aborted) setScriptBusy('')
    }
  }

  // A changed method/platform/version invalidates every in-flight secret
  // response. Reuse the server identity, not an old installer's returned files.
  function invalidateMaterials() {
    scriptRequest.current?.abort()
    filesRequest.current?.abort()
    commandRequest.current?.abort()
    setInstallCommand(null)
    setCommandBusy(false)
    setCommandError('')
    setCommandExpired(false)
    setScriptBusy('')
    setScriptError('')
    setFiles(null)
    setFilesBusy(false)
    setFilesError('')
  }

  async function generateInstallCommand() {
    if (!canConfigure || !server || provisioning?.server.id !== server.id || !versionValid ||
      selection.method !== 'linux' || commandBusy || rotating) return
    const controller = new AbortController()
    commandRequest.current = controller
    setCommandBusy(true)
    setCommandError('')
    setInstallCommand(null)
    setCommandExpired(false)
    try {
      const result = await createNodeInstallCommand(server.id, { version: version.trim(), mode: installMode }, controller.signal)
      if (controller.signal.aborted) return
      if (result.server_id !== server.id || typeof result.command !== 'string' || !result.command.trim() ||
        result.command.length > 8192 || /[\0\r\n]/.test(result.command) ||
        typeof result.expires_at !== 'string' || !Number.isFinite(Date.parse(result.expires_at)) ||
        Date.parse(result.expires_at) <= Date.now()) throw new Error(t('admin:servers.native.command_failed'))
      setInstallCommand({ command: result.command, expires_at: result.expires_at })
    } catch (error) {
      if (!controller.signal.aborted) setCommandError(installationErrorMessage(error, t('admin:servers.native.command_failed')))
    } finally {
      if (!controller.signal.aborted) setCommandBusy(false)
    }
  }

  async function generateFiles() {
    if (!server || !provisioning || !versionValid || !['docker', 'manual'].includes(selection.method) || filesBusy || rotating) return
    const controller = new AbortController()
    filesRequest.current = controller
    setFilesBusy(true)
    setFilesError('')
    setFiles(null)
    try {
      const result = await createNativeInstallationFiles(server.id, version.trim(), selection, controller.signal)
      if (controller.signal.aborted) return
      const expectedOS = selection.method === 'manual' ? selection.os : 'linux'
      if (result.method !== selection.method || result.os !== expectedOS ||
        !Array.isArray(result.files) || !result.files.length ||
        result.files.some(file => !/^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$/.test(file.name) ||
          (file.destination !== undefined && !/^(?:[A-Za-z0-9][A-Za-z0-9._-]{0,99}\/)?[A-Za-z0-9][A-Za-z0-9._-]{0,99}$/.test(file.destination)) ||
          typeof file.content !== 'string') ||
        !Array.isArray(result.steps) || (selection.method === 'manual' &&
          (!Array.isArray(result.downloads) || result.downloads.length !== 2 ||
            result.downloads.some(download => !isOfficialNodeReleaseDownload(download, version.trim()))))) {
        throw new Error(t('admin:servers.native.files_failed'))
      }
      setFiles(result)
    } catch (error) {
      if (!controller.signal.aborted) setFilesError(installationErrorMessage(error, t('admin:servers.native.files_failed')))
    } finally {
      if (!controller.signal.aborted) setFilesBusy(false)
    }
  }

  return {
    canConfigure, serverID, provisioning, installationLoading, installationError, retryInstallation: () => setInstallationReload(v => v + 1),
    credentialUnavailable, oldCredential, setOldCredential, credentialBusy, credentialError, saveOldCredential,
    version, setVersion, selection, setSelection, versionValid, reportedVersion, publicInstallCommands,
    upgradeInPlace, replaceIdentity, installMode,
    setUpgradeInPlace: (v: boolean) => { invalidateMaterials(); setUpgradeInPlace(v); setReplaceIdentity(false) },
    setReplaceIdentity: (v: boolean) => { invalidateMaterials(); setReplaceIdentity(v); setUpgradeInPlace(false) },
    installCommand, commandBusy, commandError, commandExpired, expireCommand: () => setCommandExpired(true), generateInstallCommand,
    files, filesBusy, filesError, generateFiles,
    scriptBusy, scriptError, deliverScript,
    status, statusError, invalidateMaterials,
  }
}

export type NativeInstallation = ReturnType<typeof useNativeInstallation>
