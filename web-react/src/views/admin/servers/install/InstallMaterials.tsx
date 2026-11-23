import { useTranslation } from 'react-i18next'
import { IconCopy, IconDownload } from '@/lib/icons'
import { DialogNotice } from '../dialogs/DialogNotice'
import { BusyButton } from './BusyButton'
import { Button, buttonVariants } from '@/components/ui/button'
import { copyToClipboard } from '@/utils/clipboard'
import { downloadInstallationFile } from './installModel'
import NodeInstallCommand from './NodeInstallCommand'
import { ReadonlySecret, ReadonlyText, ReadonlyValue } from './ReadonlyValue'
import type { NativeInstallation } from './useNativeInstallation'

/** The static GitHub workflow: the public installer commands (no secret in them) and the three values the node is connected with. */
export function GithubMaterials({ inst }: { inst: NativeInstallation }) {
  const { t } = useTranslation('admin')
  const { provisioning, publicInstallCommands } = inst
  if (!provisioning) return null
  return (
    <div className="flex flex-col gap-4">
      <DialogNotice>{t('admin:servers.native.github_manual_summary')}</DialogNotice>
      <ReadonlyText label={t('admin:servers.native.github_install_command')} value={publicInstallCommands.install} rows={3} />
      <Button variant="default" className="self-start" onClick={() => void copyToClipboard(publicInstallCommands.install)}><IconCopy />{t('admin:servers.native.copy_github_command')}</Button>
      <ReadonlyText label={t('admin:servers.native.github_install_only_command')} value={publicInstallCommands.installOnly} rows={3} />
      <Button variant="outline" className="self-start" onClick={() => void copyToClipboard(publicInstallCommands.installOnly)}><IconCopy />{t('admin:servers.native.copy_github_install_only_command')}</Button>
      <h3 className="text-sm font-medium">{t('admin:servers.native.github_values_title')}</h3>
      <ReadonlyValue label={t('admin:servers.native.endpoint')} value={provisioning.endpoint} copy={false} />
      <ReadonlyValue label={t('admin:servers.native.agent_id')} value={provisioning.agent_id} copy={false} />
      <ReadonlySecret label={t('admin:servers.native.credential')} value={provisioning.credential} />
      <div className="flex flex-wrap gap-2">
        <Button variant="ghost" onClick={() => void copyToClipboard(provisioning.endpoint)}><IconCopy />{t('admin:servers.native.copy_endpoint')}</Button>
        <Button variant="ghost" onClick={() => void copyToClipboard(provisioning.agent_id)}><IconCopy />{t('admin:servers.native.copy_agent_id')}</Button>
        <Button variant="ghost" onClick={() => void copyToClipboard(provisioning.credential)}><IconCopy />{t('admin:servers.native.copy_credential')}</Button>
      </div>
      <p className="text-sm text-muted-foreground">{t('admin:servers.native.github_connect_hint')}</p>
    </div>
  )
}

/** The offline install: the node's identity values, shown beside the button that generates its files. */
export function ManualIdentity({ inst }: { inst: NativeInstallation }) {
  const { t } = useTranslation('admin')
  const { provisioning } = inst
  if (!provisioning) return null
  return (
    <div className="flex flex-col gap-3">
      <DialogNotice>{t('admin:servers.native.offline_summary')}</DialogNotice>
      <ReadonlyValue label={t('admin:servers.native.endpoint')} value={provisioning.endpoint} />
      <ReadonlyValue label={t('admin:servers.native.agent_id')} value={provisioning.agent_id} />
      <ReadonlySecret label={t('admin:servers.native.credential')} value={provisioning.credential} />
    </div>
  )
}

/** What the generate button produced: the one-time command (Linux) or the downloadable files (Docker, offline). */
export function GeneratedMaterials({ inst }: { inst: NativeInstallation }) {
  const { t } = useTranslation('admin')
  const { selection, installCommand, commandExpired, files } = inst
  return (
    <>
      {inst.commandError && <DialogNotice tone="danger">{inst.commandError}</DialogNotice>}
      {inst.filesError && <DialogNotice tone="danger">{inst.filesError}</DialogNotice>}
      {selection.method === 'linux' && installCommand && (
        <NodeInstallCommand command={installCommand.command} expiresAt={installCommand.expires_at} disabled={commandExpired} onExpired={inst.expireCommand} />
      )}
      {(selection.method === 'docker' || selection.method === 'manual') && files && (
        <div className="flex flex-col gap-2">
          <p className="text-sm">{t('admin:servers.native.files_ready')}</p>
          <p className="text-xs text-muted-foreground">{t('admin:servers.native.files_advanced_hint')}</p>
          <div className="flex flex-wrap gap-2">
            {files.files.map(file => (
              <Button key={file.name} variant="outline" onClick={() => downloadInstallationFile(file.name, file.content)}>
                <IconDownload />{t('admin:servers.native.download_file', { name: file.destination ?? file.name })}
              </Button>
            ))}
            {files.downloads?.map(download => (
              // A real link: it leaves for the project's release page, so it is announced and opens like one.
              <a key={download.name} href={download.url} target="_blank" rel="noopener noreferrer" className={buttonVariants({ variant: 'outline' })}>
                <IconDownload />{t('admin:servers.native.download_release_file', { name: download.name })}
              </a>
            ))}
          </div>
        </div>
      )}
    </>
  )
}

/** The generate button: its label and its off state depend on the method (the command needs an admin and a matching identity). */
export function GenerateButton({ inst, rotating }: { inst: NativeInstallation; rotating: boolean }) {
  const { t } = useTranslation('admin')
  const { selection, provisioning, serverID, versionValid, commandBusy, filesBusy, canConfigure } = inst
  const linux = selection.method === 'linux'
  const off = linux
    ? !canConfigure || provisioning?.server.id !== serverID || !versionValid || commandBusy || rotating
    : !versionValid || filesBusy || rotating
  return (
    <BusyButton className="self-start" disabled={off} busy={commandBusy || filesBusy} icon={<IconDownload />}
      onClick={() => void (linux ? inst.generateInstallCommand() : inst.generateFiles())}>
      {t(linux ? 'admin:servers.native.generate_command' : 'admin:servers.native.generate_files')}
    </BusyButton>
  )
}
