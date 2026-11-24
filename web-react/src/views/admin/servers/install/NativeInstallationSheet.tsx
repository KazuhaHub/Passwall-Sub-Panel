import { Link } from 'react-router'
import { useTranslation } from 'react-i18next'
import type { NativeInstallationSelection, NativeServerProvisioning, Server } from '@/api/servers'
import { IconKey } from '@/lib/icons'
import { BusyButton } from './BusyButton'
import { DetailSheet } from '@/components/data/DetailSheet'
import { Field } from '@/components/form/Field'
import { Button, buttonVariants } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { effectiveUpdateChannel } from '@/utils/nodeReleaseUpdate'
import { Notice } from '@/components/common/Notice'
import { AgentStatus } from './AgentStatus'
import { InstallAdvanced } from './InstallAdvanced'
import { GeneratedMaterials, GenerateButton, GithubMaterials, ManualIdentity } from './InstallMaterials'
import { InstallMethodFields } from './InstallMethodFields'
import { DEFAULT_INSTALLATION } from './installModel'
import NodeReleaseSelector from './NodeReleaseSelector'
import { useNativeInstallation } from './useNativeInstallation'

export interface NativeInstallationSheetProps {
  server: Server | null
  initialProvisioning: NativeServerProvisioning | null
  onClose: () => void
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
 * Installing (or reinstalling) a Passwall Node server, as one guided flow in a wide sheet: how it is installed, which
 * release, the one-time material the panel generates, and the agent's status while the install runs on the host.
 *
 * It stays open while the node is coming up (the status is polled), so the operator watches "waiting" turn into
 * "running" instead of closing the page and wondering. The credential and identity are in the Advanced fold, not on
 * screen. `rotating` locks the controls while the caller rotates the credential, because material generated before the
 * rotation would be for a credential that no longer exists.
 */
export function NativeInstallationSheet({ server, initialProvisioning, onClose, onRotate, rotating = false, newServer = false, initialSelection = DEFAULT_INSTALLATION }: NativeInstallationSheetProps) {
  const { t } = useTranslation(['admin', 'common'])
  const inst = useNativeInstallation({ server, initialProvisioning, initialSelection, rotating })
  const { provisioning, selection, version } = inst
  return (
    <DetailSheet
      open={!!server}
      onClose={onClose}
      title={t(newServer ? 'admin:servers.passwall_node_install.title' : 'admin:servers.install_reinstall.title', { name: server?.name ?? '' })}
      description={server && !newServer ? t('admin:servers.native.reinstall_safety') : undefined}
      className="data-[side=right]:sm:w-[min(44rem,96vw)] data-[side=right]:sm:max-w-[min(44rem,96vw)]"
      footer={(
        <>
          <Button variant="ghost" onClick={onClose}>{t('common:actions.close')}</Button>
          {/* A real link: it goes to the nodes page, so it is announced as one and opens in a new tab like one. */}
          <Link to="/admin/nodes" onClick={onClose} className={buttonVariants({ variant: inst.status?.state === 'running' ? 'default' : 'outline' })}>{t('admin:servers.native.configure_nodes')}</Link>
        </>
      )}
    >
      <div className="flex flex-col gap-5">
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
              <Button variant="outline" disabled={inst.credentialBusy || rotating} onClick={() => server && onRotate(server)}>
                <IconKey />{t('admin:servers.action.rotate_node_credential')}
              </Button>
            </div>
          </div>
        )}
        {provisioning && (
          <>
            {selection.method !== 'github' && (
              <NodeReleaseSelector compact key={inst.serverID} enabled={!!server} selection={selection} value={version}
                initialChannel={effectiveUpdateChannel(server?.update_channel)}
                onChange={next => { inst.invalidateMaterials(); inst.setVersion(next) }} autoSelectLatest disabled={rotating} />
            )}
            {selection.method === 'linux' && (
              <div className="flex flex-col gap-3">
                <CheckOption checked={inst.upgradeInPlace} disabled={!server} onChange={inst.setUpgradeInPlace}
                  label={t('admin:servers.native.upgrade_in_place')} hint={t('admin:servers.native.upgrade_in_place_hint')} />
                {/* THE SECOND ANSWER TO THE SAME QUESTION, and they are mutually exclusive
                    because they say opposite things about the identity: one keeps it, the
                    other replaces it. Both are off by default; the panel never picks. */}
                <CheckOption checked={inst.replaceIdentity} disabled={!server} onChange={inst.setReplaceIdentity}
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
            {provisioning.endpoint.startsWith('http://') && <Notice tone="warning">{t('admin:servers.native.http_warning')}</Notice>}
            {selection.method === 'github' ? <GithubMaterials inst={inst} /> : (
              <>
                {selection.method === 'manual' && <ManualIdentity inst={inst} />}
                <GenerateButton inst={inst} rotating={rotating} />
              </>
            )}
            <GeneratedMaterials inst={inst} />
          </>
        )}
        <AgentStatus status={inst.status} error={inst.statusError} />
        <InstallAdvanced inst={inst} newServer={newServer} rotating={rotating} hasServer={!!server} />
      </div>
    </DetailSheet>
  )
}
