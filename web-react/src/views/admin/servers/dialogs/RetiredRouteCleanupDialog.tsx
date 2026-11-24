import { useState } from 'react'
import { isAxiosError } from 'axios'
import { useTranslation } from 'react-i18next'
import { cleanRetiredRoutes, previewRetiredRoutes, type RetiredRoutePreview, type Server } from '@/api/servers'
import { confirm } from '@/components/ConfirmHost'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Notice } from '@/components/common/Notice'

type Busy = 'preview' | 'cleanup' | null

/**
 * Cleans up the routing rules the retired 3X-UI VLESS route-line feature left in a server's template. The dialog
 * says what it will do before it does anything; the server's own dry run (GET) lists the exact rules, and only that
 * list, tied to its revision, can be removed (POST). Removing asks once more, naming the server and the count, with
 * focus on Cancel: Passwall Panel does not restart Xray, so the change is not live until the person does.
 */
export default function RetiredRouteCleanupDialog({ server, onClose }: { server: Server; onClose: () => void }) {
  const { t } = useTranslation(['admin', 'common'])
  const [preview, setPreview] = useState<RetiredRoutePreview | null>(null)
  const [removed, setRemoved] = useState<number | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState<Busy>(null)

  async function inspect() {
    setBusy('preview')
    setError('')
    setPreview(null)
    try {
      setPreview(await previewRetiredRoutes(server.id))
    } catch {
      setError(t('admin:servers.retired_routes.preview_failed'))
    } finally {
      setBusy(null)
    }
  }

  async function apply() {
    if (!preview?.rule_tags.length || busy) return
    // Busy BEFORE the confirm opens, so a second fast click cannot stack another confirm.
    setBusy('cleanup')
    const ok = await confirm({
      title: t('admin:servers.retired_routes.title', { server: server.name }),
      message: t('admin:servers.retired_routes.confirm', { count: preview.rule_tags.length }),
      confirmText: t('admin:servers.retired_routes.cleanup'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok) { setBusy(null); return }
    setError('')
    try {
      const result = await cleanRetiredRoutes(server.id, preview.revision)
      setRemoved(result.rule_tags.length)
    } catch (err) {
      setError(t(isAxiosError(err) && err.response?.status === 409
        ? 'admin:servers.retired_routes.stale' : 'admin:servers.retired_routes.cleanup_failed'))
    } finally {
      // The list was read before this attempt; whatever happened, it is not a current picture any more.
      setPreview(null)
      setBusy(null)
    }
  }

  return (
    <Dialog open onOpenChange={open => { if (!open && !busy) onClose() }}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('admin:servers.retired_routes.title', { server: server.name })}</DialogTitle>
          <DialogDescription>{t('admin:servers.retired_routes.description')}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3" aria-live="polite">
          {busy && <p role="status" className="text-muted-foreground">
            {t(busy === 'preview' ? 'admin:servers.retired_routes.previewing' : 'admin:servers.retired_routes.cleaning')}
          </p>}
          {error && <Notice tone="danger">{error}</Notice>}
          {removed !== null && <Notice tone="success">{t('admin:servers.retired_routes.removed', { count: removed })}</Notice>}
          {preview && (preview.rule_tags.length === 0
            ? <Notice tone="info">{t('admin:servers.retired_routes.empty')}</Notice>
            : <>
                <Notice tone="warning">{t('admin:servers.retired_routes.confirm', { count: preview.rule_tags.length })}</Notice>
                <ul className="m-0 flex max-h-48 list-none flex-col gap-1 overflow-y-auto rounded-lg bg-muted/50 p-2 text-xs [overflow-wrap:anywhere]">
                  {preview.rule_tags.map((tag, index) => <li key={`${tag}-${index}`}><code>{tag}</code></li>)}
                </ul>
              </>)}
        </div>
        <DialogFooter>
          <Button variant="outline" disabled={busy !== null} onClick={onClose}>{t('admin:servers.retired_routes.close')}</Button>
          {removed === null && <AsyncButton variant="outline" pending={busy === 'preview'} disabled={busy !== null} onClick={() => inspect()}>
            {t('admin:servers.retired_routes.preview')}
          </AsyncButton>}
          {!!preview?.rule_tags.length && (
            <Button variant="destructive" disabled={busy !== null} onClick={() => void apply()}>
              {t('admin:servers.retired_routes.cleanup')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
