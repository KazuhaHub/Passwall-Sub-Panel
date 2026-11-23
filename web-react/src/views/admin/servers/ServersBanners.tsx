import { useTranslation } from 'react-i18next'
import type { CompatStatusResponse, Server } from '@/api/servers'
import { compatNotice } from '@/utils/compatNotice'
import { DialogNotice } from './dialogs/DialogNotice'

/**
 * The compatibility state worth saying before the list, split by what the operator does next. Too old means upgrade
 * the server now (it can no longer be talked to reliably); untested means PSP has not verified that version yet (it may
 * work, it may not). "Unknown" stays out: it usually means never probed or a transient failure, not a real problem. The
 * per-server banners look at the CURRENT page only — a row on another page would be misleading. The fleet-level notice
 * comes first because those are states an operator would otherwise misread as a working server; nothing is shown for a
 * merely old range, because a banner nobody needs is how banners stop being read.
 */
export function ServersBanners({ items, compat }: { items: Server[]; compat: CompatStatusResponse | null }) {
  const { t } = useTranslation('admin')
  const notice = compatNotice(compat)
  const tooOld = items.filter(s => s.compat_status === 'too_old')
  const untested = items.filter(s => s.compat_status === 'untested')
  const list = (rows: Server[]) => rows.map(s => `${s.name} (v${s.panel_version ?? '?'})`).join('、')
  const any = notice || tooOld.length > 0 || untested.length > 0 || items.some(s => s.panel_type === '3xui')
  if (!any) return null
  return (
    <div className="mb-4 flex flex-col gap-3">
      {items.some(s => s.panel_type === '3xui') && <DialogNotice>{t('admin:servers.retired_routes.upgrade_notice')}</DialogNotice>}
      {notice && <DialogNotice tone="warning">{t(`admin:servers.compat_notice.${notice.kind}`, notice.values)}</DialogNotice>}
      {tooOld.length > 0 && (
        <DialogNotice tone="danger">
          <span className="block font-medium">{t('admin:servers.compat.banner_title_too_old', { count: tooOld.length })}</span>
          <span className="block">{t('admin:servers.compat.banner_body_too_old', { panels: list(tooOld) })}</span>
        </DialogNotice>
      )}
      {untested.length > 0 && (
        <DialogNotice tone="warning">
          <span className="block font-medium">{t('admin:servers.compat.banner_title_untested', { count: untested.length })}</span>
          <span className="block">{t('admin:servers.compat.banner_body_untested', { panels: list(untested) })}</span>
        </DialogNotice>
      )}
    </div>
  )
}
