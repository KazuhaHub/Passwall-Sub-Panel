import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { PanelType, Server } from '@/api/servers'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { NativeSelect } from '@/components/ui/native-select'
import { IconExternalLink } from '@/lib/icons'
import { useCan } from '@/utils/permissions'
import { Notice } from '@/components/common/Notice'

const BACKENDS = ['psp', '3xui', 'sui'] as const
const LABELS: Record<PanelType, string> = { psp: 'Passwall Node', '3xui': '3X-UI', sui: 'S-UI' }
const MANUAL_GUIDES = {
  '3xui': 'https://github.com/MHSanaei/3x-ui',
  sui: 'https://github.com/alireza0/s-ui',
} as const

interface ReinstallBackendDialogProps {
  server: Server | null
  onClose: () => void
  onNativeInstall: (server: Server) => void
  onNativeMigration: (server: Server) => void
  onConfigure: (server: Server) => void
}

/**
 * Install / Reinstall for one server record. It only chooses the way forward and hands over to the page: it makes no
 * request of its own. Which targets are offered follows what is verified: the same backend (a native reinstall, or
 * manual guidance for a third-party panel) and 3X-UI to Passwall Node (the checked conversion). Every other target
 * says it is unavailable and leaves the button off, so nothing here can change an adapter by accident.
 */
export function ReinstallBackendDialog({ server, onClose, onNativeInstall, onNativeMigration, onConfigure }: ReinstallBackendDialogProps) {
  const { t } = useTranslation(['admin', 'common'])
  const canConfigure = useCan('config.write')
  // The parent keys this small dialog by server ID. An existing record always
  // defaults to its original backend; opening it must not change its adapter.
  const original = server?.panel_type ?? '3xui'
  const [target, setTarget] = useState<PanelType>(original)
  const sameBackend = target === original
  const nativeReinstall = sameBackend && target === 'psp'
  const nativeMigration = !sameBackend && original === '3xui' && target === 'psp'
  const manualReinstall = sameBackend && target !== 'psp'

  return <Dialog open={!!server} onOpenChange={open => { if (!open) onClose() }}>
    <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
      <DialogHeader>
        <DialogTitle>{t('admin:servers.install_reinstall.title', { name: server?.name ?? '' })}</DialogTitle>
        {canConfigure && <DialogDescription>{t('admin:servers.install_reinstall.original', { id: server?.id, backend: LABELS[original] })}</DialogDescription>}
      </DialogHeader>
      {!canConfigure ? <Notice tone="danger">{t('admin:servers.install_reinstall.forbidden')}</Notice> : <div className="flex flex-col gap-4">
        <Field label={t('admin:servers.install_reinstall.backend')}>
          <NativeSelect value={target} onChange={event => setTarget(event.target.value as PanelType)}>
            {BACKENDS.map(backend => <option key={backend} value={backend}>{LABELS[backend]}</option>)}
          </NativeSelect>
        </Field>
        {sameBackend
          ? <Notice tone="info">{t(nativeReinstall
            ? 'admin:servers.install_reinstall.native_same'
            : 'admin:servers.install_reinstall.manual_same')}</Notice>
          : <Notice tone="warning">{t('admin:servers.install_reinstall.switch_warning', {
            original: LABELS[original], target: LABELS[target],
          })}</Notice>}
        {nativeMigration && <p>{t('admin:servers.install_reinstall.switch_precheck')}</p>}
        {manualReinstall && <>
          <Field label={t('admin:servers.install_reinstall.method')}>
            {/* One real choice: the automatic method is listed so the person sees it exists, but it is not verified. */}
            <NativeSelect defaultValue="manual">
              <option value="manual">{t('admin:servers.install_reinstall.manual_method')}</option>
              <option value="automatic" disabled>{t('admin:servers.install_reinstall.automatic_unavailable')}</option>
            </NativeSelect>
          </Field>
          <Notice tone="warning">{t('admin:servers.install_reinstall.manual_unverified')}</Notice>
          <ol className="list-decimal space-y-1.5 pl-5">
            <li>{t('admin:servers.install_reinstall.manual_backup')}</li>
            <li>{t('admin:servers.install_reinstall.manual_install', { backend: LABELS[target] })}</li>
            <li>{t('admin:servers.install_reinstall.manual_restore')}</li>
            <li>{t('admin:servers.install_reinstall.manual_configure')}</li>
          </ol>
          <a className="inline-flex w-fit items-center gap-1 text-primary underline underline-offset-2" href={MANUAL_GUIDES[target as '3xui' | 'sui']} target="_blank" rel="noopener noreferrer">
            {t('admin:servers.install_reinstall.official_guide', { backend: LABELS[target] })}
            <IconExternalLink aria-hidden className="size-3.5" />
          </a>
        </>}
        {!sameBackend && !nativeMigration && <Notice tone="danger">
          {t('admin:servers.install_reinstall.switch_unavailable')}
        </Notice>}
      </div>}
      <DialogFooter>
        <Button variant="outline" onClick={onClose}>{t('common:actions.close')}</Button>
        {manualReinstall && canConfigure && <Button variant="outline" onClick={() => server && onConfigure(server)}>
          {t('admin:servers.install_reinstall.configure_original')}
        </Button>}
        {!manualReinstall && <Button disabled={!canConfigure || (!nativeReinstall && !nativeMigration)}
          onClick={() => {
            if (!server || !canConfigure) return
            if (nativeReinstall) onNativeInstall(server)
            else if (nativeMigration) onNativeMigration(server)
          }}>
          {t(nativeMigration ? 'admin:servers.install_reinstall.precheck' : 'admin:servers.install_reinstall.continue')}
        </Button>}
      </DialogFooter>
    </DialogContent>
  </Dialog>
}
