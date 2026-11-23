import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { listCoreReleases, listXrayVersions, selectCore, upgradeXray, type CoreRelease, type NativeCoreEngine, type Server } from '@/api/servers'
import { AsyncButton } from '@/components/common/AsyncButton'
import { confirm } from '@/components/ConfirmHost'
import { Field } from '@/components/form/Field'
import { pushSnack } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { NativeSelect } from '@/components/ui/native-select'
import { DialogNotice } from './DialogNotice'

/**
 * The core of one server. A 3X-UI server upgrades its Xray in place: a version list read from the panel, or "latest".
 * A Passwall Node SELECTS an engine and an exact version from the audited catalog and converges asynchronously, so the
 * catalog is required and the dialog fails closed without it (installing an unknown version is blocked).
 *
 * The dialog says what will happen first; a release that narrows REALITY client compatibility asks a second time,
 * naming the version, because it silently changes what subscriptions contain.
 */
export function CoreDialog({ server, onClose, onSelected, onUpgraded, onBusy }: {
  server: Server
  onClose: () => void
  /** A Passwall Node's desired core was saved. */
  onSelected: (server: Server, engine: NativeCoreEngine, version: string) => void
  /** A 3X-UI core was upgraded; the caller re-probes the server. */
  onUpgraded: (server: Server) => void
  /** The row's menu stays disabled while a core request is in flight (as the old page's upgrading flag did). */
  onBusy?: (id: number | null) => void
}) {
  const { t, i18n } = useTranslation(['admin', 'common'])
  const native = server.panel_type === 'psp'
  const [versionPick, setVersionPick] = useState('')
  const [legacyVersions, setLegacyVersions] = useState<string[]>([])
  const [releases, setReleases] = useState<CoreRelease[]>([])
  const [enginePick, setEnginePick] = useState<NativeCoreEngine>('xray')
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)

  // The list is read the moment the dialog opens. If the server cannot answer the dialog still opens, with only the
  // "latest" option for 3X-UI, so the admin can still upgrade; a native node has nothing to offer without its catalog.
  useEffect(() => {
    let live = true
    void (async () => {
      try {
        if (native) {
          const list = await listCoreReleases(server.id)
          if (!live) return
          setReleases(list)
          const engine = server.desired_core_engine ?? server.core_engine ?? 'xray'
          setEnginePick(engine)
          const selected = list.find(r => r.engine === engine && r.version === server.desired_core_version)
            ?? list.find(r => r.engine === engine && r.tier === 'recommended')
            ?? list.find(r => r.engine === engine)
          setVersionPick(selected?.version ?? '')
        } else {
          const result = await listXrayVersions(server.id)
          if (!live) return
          setLegacyVersions(result.versions)
          setReleases(result.releases)
        }
      } catch {
        if (live) { setLegacyVersions([]); setReleases([]) }
      } finally {
        if (live) setLoading(false)
      }
    })()
    return () => { live = false }
    // One read per opened server.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server.id])

  const selectedRelease = releases.find(r => r.engine === enginePick && r.version === versionPick)
  const versions = native ? releases.filter(r => r.engine === enginePick).map(r => r.version) : legacyVersions

  function changeEngine(engine: NativeCoreEngine) {
    setEnginePick(engine)
    const release = releases.find(r => r.engine === engine && r.tier === 'recommended') ?? releases.find(r => r.engine === engine)
    setVersionPick(release?.version ?? '')
  }

  async function submit() {
    setBusy(true)
    onBusy?.(server.id)
    try {
      let confirmRestricted = false
      if (native) {
        if (!versionPick) return
        if (selectedRelease?.requires_confirmation) {
          const accepted = await confirm({
            title: t('admin:servers.confirm.restricted_core_title'),
            message: t('admin:servers.confirm.restricted_core_message', {
              version: selectedRelease.version,
              summary: i18n.language.startsWith('zh') ? selectedRelease.summary.zh_cn : selectedRelease.summary.en,
            }),
            confirmText: t('admin:servers.confirm.restricted_core_confirm'),
            initialFocus: 'cancel',
          })
          if (!accepted) return
          confirmRestricted = true
        }
      }
      const r = native
        ? await selectCore(server.id, enginePick, versionPick, { confirmRestricted })
        : await upgradeXray(server.id, versionPick || undefined, { confirmRestricted })
      if (native) onSelected(server, enginePick, versionPick)
      else onUpgraded(server)
      pushSnack(native
        ? t('admin:servers.toast.core_intent_saved', { engine: enginePick === 'sing-box' ? 'sing-box' : 'Xray', version: r.version })
        : t('admin:servers.toast.upgrade_xray_ok', { version: r.version ?? 'latest' }), 'success')
      onClose()
    } catch (err) {
      pushSnack((err as { response?: { data?: { error?: string } }; message?: string }).response?.data?.error
        ?? (err as { message?: string }).message ?? 'unknown', 'error')
    } finally {
      setBusy(false)
      onBusy?.(null)
    }
  }

  const summary = selectedRelease && (i18n.language.startsWith('zh') ? selectedRelease.summary.zh_cn : selectedRelease.summary.en)
  return (
    <Dialog open onOpenChange={open => { if (!open && !busy) onClose() }}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t(native ? 'admin:servers.confirm.select_core_title' : 'admin:servers.confirm.upgrade_xray_title')}</DialogTitle>
          <DialogDescription>{t(native ? 'admin:servers.confirm.select_core_message' : 'admin:servers.confirm.upgrade_xray_message', { name: server.name })}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          {native && (
            <Field label={t('admin:servers.field.core_engine')}>
              <NativeSelect value={enginePick} disabled={loading || busy} onChange={e => changeEngine(e.target.value as NativeCoreEngine)}>
                <option value="xray">Xray</option>
                <option value="sing-box">sing-box</option>
              </NativeSelect>
            </Field>
          )}
          <Field label={t('admin:servers.field.xray_version')} hint={loading ? t('admin:servers.field.xray_version_loading') : undefined}>
            <NativeSelect value={versionPick} disabled={loading || busy} onChange={e => setVersionPick(e.target.value)}>
              {!native && <option value="">{t('admin:servers.field.xray_version_latest')}</option>}
              {versions.map(v => {
                const release = releases.find(r => r.engine === enginePick && r.version === v)
                return <option key={v} value={v}>{v}{release ? ` · ${t(`admin:servers.core_tier.${release.tier}`)}` : ''}</option>
              })}
            </NativeSelect>
          </Field>
          {native && !loading && releases.length === 0 && <DialogNotice tone="danger">{t('admin:servers.field.core_catalog_unavailable')}</DialogNotice>}
          {native && selectedRelease && (
            <div className="flex flex-col gap-1 rounded-lg border bg-muted/40 p-3 text-sm">
              <p className="font-medium">{t(`admin:servers.core_tier.${selectedRelease.tier}`)}</p>
              <p>{summary}</p>
              <p className="text-xs text-muted-foreground">{t('admin:servers.field.core_reality_matrix', {
                xray: t(`admin:servers.core_support.${selectedRelease.reality.xray}`),
                mihomo: t(`admin:servers.core_support.${selectedRelease.reality.mihomo}`),
                singbox: t(`admin:servers.core_support.${selectedRelease.reality.sing_box}`),
                uri: t(`admin:servers.core_support.${selectedRelease.reality.uri_list}`),
              })}</p>
              <p className="text-xs text-muted-foreground">{t('admin:servers.field.core_evidence', {
                config: selectedRelease.evidence.config_tested ? t('admin:servers.field.core_evidence_yes') : t('admin:servers.field.core_evidence_no'),
                handshake: selectedRelease.evidence.handshake_tested ? t('admin:servers.field.core_evidence_yes') : t('admin:servers.field.core_evidence_no'),
              })}</p>
              {selectedRelease.evidence.handshakes?.length ? (
                <p className="text-xs text-muted-foreground">
                  {t('admin:servers.field.core_handshake_title')}{' '}
                  {selectedRelease.evidence.handshakes.map((h, index) => (
                    <span key={`${h.client}-${h.version}-${h.profile}`} title={`${h.profile}; ${h.platform}`}>
                      {index > 0 ? ' · ' : ''}{t(`admin:servers.core_client.${h.client}`)} {h.version} {t(`admin:servers.core_handshake_result.${h.result}`)}
                    </span>
                  ))}
                </p>
              ) : null}
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>{t('common:actions.cancel')}</Button>
          <AsyncButton pending={busy} disabled={native && (loading || !versionPick)} onClick={() => submit()}>
            {native ? t('admin:servers.action.select_core') : t('admin:servers.action.upgrade')}
          </AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
