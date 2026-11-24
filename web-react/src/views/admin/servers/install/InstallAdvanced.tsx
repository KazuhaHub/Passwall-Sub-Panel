import { useTranslation } from 'react-i18next'
import { IconCopy, IconDownload } from '@/lib/icons'
import { Notice } from '@/components/common/Notice'
import { Disclosure } from '@/components/common/Disclosure'
import { Button, buttonVariants } from '@/components/ui/button'
import { Field } from '@/components/form/Field'
import { Switch } from '@/components/ui/switch'
import { copyToClipboard } from '@/utils/clipboard'
import { BusyButton } from './BusyButton'
import { ReadonlySecret, ReadonlyText, ReadonlyValue } from './ReadonlyValue'
import type { NativeInstallation } from './useNativeInstallation'

/**
 * Everything the flow keeps out of sight until asked: the node's identity and credential, the private install script
 * (the fallback to the one-time command) and the step-by-step material of the Docker and offline methods. Not mounted
 * while folded, so a credential that is not asked for is not in the page.
 */
export function InstallAdvanced({ inst, newServer, rotating, hasServer }: { inst: NativeInstallation; newServer: boolean; rotating: boolean; hasServer: boolean }) {
  const { t } = useTranslation('admin')
  const { provisioning, selection, files, status } = inst
  if (!provisioning) return null
  const showIdentity = selection.method !== 'github' && selection.method !== 'manual'
  return (
    <Disclosure className="rounded-lg border" buttonClassName="min-h-(--control-h) px-3 text-sm font-medium" bodyClassName="flex flex-col gap-3 border-t px-3 py-3" summary={t('admin:servers.native.advanced')}>
      {hasServer && !newServer && <p className="text-sm">{t('admin:servers.passwall_node_install.existing_hint')}</p>}
      <p className="text-sm">{t('admin:servers.native.private_warning')}</p>
      {showIdentity && (
        <>
          <ReadonlyValue label={t('admin:servers.native.agent_id')} value={provisioning.agent_id} />
          <ReadonlyValue label={t('admin:servers.native.endpoint')} value={provisioning.endpoint} />
          <ReadonlySecret label={t('admin:servers.native.credential')} value={provisioning.credential} />
        </>
      )}
      {selection.method === 'docker' && (
        <Field orientation="horizontal" label={t('admin:servers.native.docker_remote_upgrade')} hint={t('admin:servers.native.docker_remote_upgrade_hint')}>
          <Switch checked={!!selection.dockerRemoteUpgrade} disabled={rotating}
            onCheckedChange={checked => {
              inst.invalidateMaterials()
              inst.setSelection(current => ({ ...current, dockerRemoteUpgrade: checked }))
            }} />
        </Field>
      )}
      <p className="whitespace-pre-line text-sm">{t(`admin:servers.native.method_hint.${selection.method}`)}</p>
      <p className="whitespace-pre-line text-sm">{t(`admin:servers.native.method_steps.${selection.method}`)}</p>
      {status?.last_seen && <p className="text-sm">{t('admin:servers.native.last_seen', { time: status.last_seen })}</p>}
      {status && <p className="text-sm">{t('admin:servers.native.configured_nodes', { count: status.configured_nodes })}</p>}
      {status?.core_state && <p className="text-sm">{t('admin:servers.native.core_state', { state: status.core_state })}</p>}
      <p className="text-sm">{t('admin:servers.native.heartbeat_hint')}</p>
      <a href="https://github.com/KazuhaHub/Passwall-Node/releases" target="_blank" rel="noopener noreferrer" className={buttonVariants({ variant: 'link', className: 'self-start px-0' })}>{t('admin:servers.native.releases')}</a>
      {selection.method === 'linux' ? (
        <>
          <h3 className="text-sm font-medium">{t('admin:servers.native.private_script_advanced')}</h3>
          {inst.scriptError && <Notice tone="danger">{inst.scriptError}</Notice>}
          <div className="flex flex-wrap gap-2">
            <BusyButton variant="outline" icon={<IconDownload />} busy={inst.scriptBusy === 'download'}
              disabled={!inst.versionValid || !!inst.scriptBusy || rotating} onClick={() => void inst.deliverScript('download')}>
              {t('admin:servers.native.download_script')}
            </BusyButton>
            <BusyButton variant="outline" icon={<IconCopy />} busy={inst.scriptBusy === 'copy'}
              disabled={!inst.versionValid || !!inst.scriptBusy || rotating} onClick={() => void inst.deliverScript('copy')}>
              {t('admin:servers.native.copy_script')}
            </BusyButton>
          </div>
          <p className="text-sm">{t('admin:servers.native.run_script_hint')}</p>
          <ReadonlyText label={t('admin:servers.native.run_script_command')} rows={2}
            value={`chmod 0600 ./passwall-node-install-${provisioning.agent_id}.sh\nsudo bash ./passwall-node-install-${provisioning.agent_id}.sh`} />
        </>
      ) : selection.method === 'docker' || selection.method === 'manual' ? (
        <>
          <p className="text-sm">{t(selection.method === 'docker' ? 'admin:servers.native.docker_hint' : 'admin:servers.native.manual_platform_hint')}</p>
          {files && (
            <>
              <p className="text-sm">{t('admin:servers.native.downloads_hint')}</p>
              {files.files.map(file => {
                const name = file.destination ?? file.name
                return (
                  <section key={file.name} aria-label={name} className="flex flex-col gap-2">
                    {/* A sensitive file is masked until revealed; its copy and download keep the original bytes (LF included). */}
                    {file.sensitive
                      ? <ReadonlySecret label={t('admin:servers.native.file_content', { name })} value={file.content} />
                      : <ReadonlyText label={t('admin:servers.native.file_content', { name })} value={file.content} rows={4} />}
                    <Button variant="ghost" className="self-start" onClick={() => void copyToClipboard(file.content)}><IconCopy />{t('admin:servers.native.copy_file', { name })}</Button>
                  </section>
                )
              })}
              {files.steps.map((step, index) => {
                const title = t(`admin:servers.native.step_title.${step.id}`, { defaultValue: step.title })
                const label = t('admin:servers.native.command_label', { number: index + 1, title })
                return (
                  <section key={step.id ?? index} aria-label={step.id ?? step.title} className="flex flex-col gap-2">
                    <h3 className="text-sm font-medium">{label}</h3>
                    {step.description && <p className="text-sm">{t(`admin:servers.native.step_description.${selection.method}.${step.id}`, { defaultValue: step.description })}</p>}
                    {step.commands?.map((command, commandIndex) => (
                      <div key={commandIndex} className="flex flex-col gap-1">
                        <ReadonlyText label={label} value={command} rows={2} />
                        <Button variant="ghost" className="self-start" onClick={() => void copyToClipboard(command)}><IconCopy />{t('admin:servers.native.copy_step')}</Button>
                      </div>
                    ))}
                  </section>
                )
              })}
            </>
          )}
        </>
      ) : <p className="text-sm">{t('admin:servers.native.github_advanced_hint')}</p>}
    </Disclosure>
  )
}
