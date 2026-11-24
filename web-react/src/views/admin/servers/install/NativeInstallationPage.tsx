import { Link } from 'react-router'
import { useTranslation } from 'react-i18next'
import type { NativeInstallationSelection, NativeServerProvisioning, Server } from '@/api/servers'
import { ObjectPage } from '@/components/common/ObjectPage'
import { useBackTarget } from '@/components/common/objectReturn'
import { Notice } from '@/components/common/Notice'
import { Field } from '@/components/form/Field'
import { Button, buttonVariants } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { IconKey } from '@/lib/icons'
import { effectiveUpdateChannel } from '@/utils/nodeReleaseUpdate'
import { SERVERS_PATH } from '../serverRoutes'
import { AgentStatus } from './AgentStatus'
import { BusyButton } from './BusyButton'
import { InstallAdvanced } from './InstallAdvanced'
import { GeneratedMaterials, GenerateButton, GithubMaterials, ManualIdentity } from './InstallMaterials'
import { InstallMethodFields } from './InstallMethodFields'
import { DEFAULT_INSTALLATION } from './installModel'
import NodeReleaseSelector from './NodeReleaseSelector'
import { useNativeInstallation } from './useNativeInstallation'

export interface NativeInstallationPageProps {
  server: Server
  initialProvisioning: NativeServerProvisioning | null
  onRotate: (server: Server) => void
  rotating?: boolean
  /** The server was just created: the title says so and the "existing record" note is left out. */
  newServer?: boolean
  initialSelection?: NativeInstallationSelection
}

function CheckOption({ checked, disabled, onChange, label, hint }: { checked: boolean; disabled: boolean; onChange: (v: boolean) => void; label: string; hint: string }) {
  return (
    <Field orientation="horizontal" label={label} hint={hint}>
      <Checkbox checked={checked} disabled={disabled} onCheckedChange={next => onChange(next === true)} />
    </Field>
  )
}

/**
 * Installing (or reinstalling) a Passwall Node server, as one guided flow on a page: how it is installed, which
 * release, the one-time material the panel generates, and the agent's status while the install runs on the host.
 *
 * The steps run down the main column in the order they are done; the agent's status sits beside them on a wide
 * screen (and under them on a narrow one) because it is what the person watches while they run the command on the
 * host: it is polled, so "waiting" turns into "running" without a reload. The credential and identity are in the
 * Advanced fold, not on screen. `rotating` locks the controls while the caller rotates the credential, because material
 * generated before the rotation would be for a credential that no longer exists.
 */
export function NativeInstallationPage({ server, initialProvisioning, onRotate, rotating = false, newServer = false, initialSelection = DEFAULT_INSTALLATION }: NativeInstallationPageProps) {
  const { t } = useTranslation(['admin', 'common'])
  const back = useBackTarget(SERVERS_PATH)
  const inst = useNativeInstallation({ server, initialProvisioning, initialSelection, rotating })
  const { provisioning, selection, version } = inst
  const running = inst.status?.state === 'running'
  // A real link: it goes to the nodes page, so it is announced as one and opens in a new tab like one.
  const configure = <Link to="/admin/nodes" className={buttonVariants({ variant: running ? 'default' : 'outline' })}>{t('admin:servers.native.configure_nodes')}</Link>
  return (
    <ObjectPage
      back={{ to: back, label: t('admin:servers.title') }}
      title={t(newServer ? 'admin:servers.passwall_node_install.title' : 'admin:servers.install_reinstall.title', { name: server.name })}
      subtitle={newServer ? undefined : t('admin:servers.native.reinstall_safety')}
      primary={running ? configure : undefined}
      secondary={running ? undefined : configure}
    >
      <div className="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="flex min-w-0 max-w-3xl flex-col gap-6">
          <InstallMethodFields selection={selection} disabled={rotating}
            onChange={next => { inst.invalidateMaterials(); inst.setVersion(''); inst.setSelection(next) }} />
          {inst.installationLoading && <p role="status" className="text-sm text-muted-foreground">{t('admin:servers.native.installation_loading')}</p>}
          {inst.installationError && (
            <Notice tone="danger">
              <span className="flex flex-wrap items-center gap-2">{inst.installationError}
                <Button variant="outline" size="sm" onClick={inst.retryInstallation}>{t('common:actions.retry')}</Button></span>
            </Notice>
          )}
          {inst.credentialUnavailable && (
            <div className="flex flex-col gap-3">
              <Notice tone="warning">{t('admin:servers.native.legacy_credential_hint')}</Notice>
              <Field label={t('admin:servers.native.old_credential')}>
                <Input type="password" value={inst.oldCredential} autoComplete="off" disabled={inst.credentialBusy} onChange={event => inst.setOldCredential(event.target.value)} />
              </Field>
              {inst.credentialError && <Notice tone="danger">{inst.credentialError}</Notice>}
              <div className="flex flex-wrap gap-2">
                {/* The import is a write plus a re-read of the installation; its own wait is shown, not just a disabled button. */}
                <BusyButton busy={inst.credentialBusy} disabled={!inst.oldCredential.trim() || rotating} onClick={() => void inst.saveOldCredential()}>
                  {t('admin:servers.native.import_credential')}
                </BusyButton>
                <Button variant="outline" disabled={inst.credentialBusy || rotating} onClick={() => onRotate(server)}>
                  <IconKey />{t('admin:servers.action.rotate_node_credential')}
                </Button>
              </div>
            </div>
          )}
          {provisioning && (
            <>
              {selection.method !== 'github' && (
                <div className="flex flex-col gap-4">
                  <NodeReleaseSelector compact key={inst.serverID} enabled selection={selection} value={version}
                    initialChannel={effectiveUpdateChannel(server.update_channel)}
                    onChange={next => { inst.invalidateMaterials(); inst.setVersion(next) }} autoSelectLatest disabled={rotating} />
                  {selection.method === 'linux' && (
                    <div className="flex flex-col gap-3">
                      <CheckOption checked={inst.upgradeInPlace} disabled={false} onChange={inst.setUpgradeInPlace}
                        label={t('admin:servers.native.upgrade_in_place')} hint={t('admin:servers.native.upgrade_in_place_hint')} />
                      {/* THE SECOND ANSWER TO THE SAME QUESTION, and they are mutually exclusive
                          because they say opposite things about the identity: one keeps it, the
                          other replaces it. Both are off by default; the panel never picks. */}
                      <CheckOption checked={inst.replaceIdentity} disabled={false} onChange={inst.setReplaceIdentity}
                        label={t('admin:servers.native.replace_identity')} hint={t('admin:servers.native.replace_identity_hint')} />
                      {inst.replaceIdentity && <Notice tone="warning">{t('admin:servers.native.replace_identity_warning')}</Notice>}
                      {/* THE COMMAND IS REFUSED AT THE HOST, NOT HERE, when the node is on another release and neither
                          answer is chosen — and that refusal arrives as a message about identity that names nothing
                          about the version. So it is said here, before the command is generated. */}
                      {inst.reportedVersion && version.trim() && inst.reportedVersion !== version.trim() && !inst.upgradeInPlace && !inst.replaceIdentity && (
                        <Notice tone="warning">{t('admin:servers.native.upgrade_in_place_needed', { version: inst.reportedVersion })}</Notice>
                      )}
                    </div>
                  )}
                </div>
              )}
              <div className="flex flex-col gap-4">
                {provisioning.endpoint.startsWith('http://') && <Notice tone="warning">{t('admin:servers.native.http_warning')}</Notice>}
                {selection.method === 'github' ? <GithubMaterials inst={inst} /> : (
                  <>
                    {selection.method === 'manual' && <ManualIdentity inst={inst} />}
                    <GenerateButton inst={inst} rotating={rotating} />
                  </>
                )}
                <GeneratedMaterials inst={inst} />
              </div>
            </>
          )}
          <InstallAdvanced inst={inst} newServer={newServer} rotating={rotating} hasServer />
        </div>
        <div className="lg:sticky lg:top-4">
          <AgentStatus status={inst.status} error={inst.statusError} />
        </div>
      </div>
    </ObjectPage>
  )
}
