import { isAxiosError } from 'axios'

import { AsyncButton } from '@/components/common/AsyncButton'
import { pushSnack } from '@/components/feedback/toasts'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { DataTablePagination } from '@/components/data/DataTablePagination'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { NativeSelect } from '@/components/ui/native-select'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { IconRefresh } from '@/lib/icons'
import { useSearchParams } from 'react-router'
import {
  EXCLUSION_EXCLUDED, EXCLUSION_KEPT, EXCLUSION_REASONS, LIVE_CONN_MAX_PER_USER,
  type LiveParams, type LiveView, type PanelRef, type RefreshThrottled,
} from '@/api/riskCenter'
import { useLiveConnections, useRefreshLiveConnections } from '@/query/riskCenter'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatDualTz } from '@/utils/datetime'
import { agoText } from '@/utils/riskCenter'
import LiveConnectionList from './LiveConnectionList'
import { liveSearch, parseLiveParams } from './riskParams'
import { UserPicker } from './UserPicker'
import { useRiskT } from './useRiskT'

// A server deleted since the snapshot is named by its id.
const names = (refs: PanelRef[]) => refs.map(r => r.name || `#${r.id}`).join(', ')

/** One caveat on the snapshot: a notice, with the reason the list may be short. */
function Note({ children }: { children: string }) {
  return <Alert><AlertDescription>{children}</AlertDescription></Alert>
}

/**
 * The live snapshot's line and every caveat on it: when it was taken and by what (a poll or a refresh),
 * and each reason the list may be short — stale, servers that failed, servers that cannot tell (S-UI,
 * info not failure), nodes with no reference yet (taken as still scanning), the per-account cap, and
 * fetches that could not be read for devices. Shared with the drawer's connections tab.
 */
export function LiveSnapshotHeader({ view }: { view: LiveView }) {
  const { t } = useRiskT()
  const panelTz = useSiteStore(st => st.timezone)
  const s = view.snapshot
  if (!s.taken_at) {
    return <p className="text-[13px] text-muted-foreground">{t('admin:risk_center.live.snapshot_none')}</p>
  }
  // The panel's timezone, like every other time in the admin; the relative age beside it is what says
  // how fresh the list is.
  const args = { time: formatDualTz(s.taken_at, panelTz), ago: agoText(s.age_seconds, t) }
  return (
    <div className="flex flex-col gap-2">
      <p className="text-[13px] text-muted-foreground">
        {s.source === 'refresh'
          ? t('admin:risk_center.live.snapshot_refresh', args)
          : t('admin:risk_center.live.snapshot_poll', args)}
      </p>
      {s.stale && <Note>{t('admin:risk_center.live.stale', { minutes: Math.round(s.stale_after_seconds / 60) })}</Note>}
      {s.panels_unread.length > 0 && (
        <Note>{t('admin:risk_center.live.unread', { count: s.panels_unread.length, names: names(s.panels_unread) })}</Note>
      )}
      {/* A server with no live read (S-UI) is a fact about it, not a failure: shown as a warning it would
          sit there forever and teach the admin to skip the warning that matters. */}
      {s.panels_unsupported.length > 0 && (
        <Note>{t('admin:risk_center.live.unsupported', {
          count: s.panels_unsupported.length, names: names(s.panels_unsupported),
        })}</Note>
      )}
      {s.unreferenced_nodes > 0 && <Note>{t('admin:risk_center.live.unreferenced', { count: s.unreferenced_nodes })}</Note>}
      {s.truncated > 0 && <Note>{t('admin:risk_center.live.truncated', { count: s.truncated, max: LIVE_CONN_MAX_PER_USER })}</Note>}
      {view.devices_unavailable && <Note>{t('admin:risk_center.live.devices_unavailable')}</Note>}
    </div>
  )
}

/**
 * What an EMPTY page of the live view says, claiming no more than the page
 * knows. Only for a snapshot that exists (the header says when there is
 * none).
 *
 * - `narrowed` (a filter is set, or the page is past the first): no match,
 *   not "nobody" — the snapshot may list many connections the filter
 *   excludes, and a page past the end of a shrunk snapshot is empty too.
 * - A panel unread: the connections on it are unknown, not absent. The live
 *   view holds a row only for an account with a connection, so an account
 *   whose only panel failed has no row and no count of its own; the fleet's
 *   unread panels are all the page can qualify its "none" with.
 */
function LiveEmpty({ view, narrowed = false }: { view: LiveView; narrowed?: boolean }) {
  const { t } = useRiskT()
  const unread = view.snapshot.panels_unread.length
  const text = narrowed
    ? t('admin:risk_center.live.empty_filtered')
    : (unread > 0 ? t('admin:risk_center.live.empty_unread', { count: unread }) : t('admin:risk_center.live.empty'))
  return <EmptyState compact title={text} />
}

/**
 * "Refresh now": one live read per server, rationed for the whole fleet by the server. Every outcome is
 * said here — the global toast is off for this request — because each needs its own words: refreshed,
 * answered by the poll that just ran, refused with the seconds to wait, or already running.
 */
export function LiveRefreshButton() {
  const { t } = useRiskT()
  const scope = useQueryScope()
  const refresh = useRefreshLiveConnections(scope)

  const run = async () => {
    try {
      const res = await refresh.mutateAsync()
      if (!res.refreshed && res.reason === 'just_polled') {
        pushSnack(t('admin:risk_center.live.just_polled'), 'info')
        return
      }
      pushSnack(t('admin:risk_center.live.refreshed', { panels: res.panels_asked, connections: res.connections }), 'success')
    } catch (err) {
      const body = isAxiosError(err) ? err.response?.data as Partial<RefreshThrottled> | undefined : undefined
      if (isAxiosError(err) && err.response?.status === 429 && body?.error === 'refresh_throttled') {
        if (body.reason === 'in_progress') {
          pushSnack(t('admin:risk_center.live.in_progress'), 'info')
        } else {
          pushSnack(t('admin:risk_center.live.cooldown', { seconds: body.retry_after_seconds ?? 0 }), 'warning')
        }
        return
      }
      pushSnack(isAxiosError(err) ? String((err.response?.data as { error?: string } | undefined)?.error ?? err.message) : String(err), 'error')
    }
  }

  return (
    <Tooltip>
      <TooltipTrigger render={<span />}>
        <AsyncButton variant="outline" onClick={run} pending={refresh.isPending} icon={<IconRefresh />}>
          {t('admin:risk_center.live.refresh')}
        </AsyncButton>
      </TooltipTrigger>
      <TooltipContent>{t('admin:risk_center.live.refresh_hint')}</TooltipContent>
    </Tooltip>
  )
}

/**
 * The Live tab: who is connected, from the last snapshot (a poll's, or an
 * admin's refresh), one row per account, most connections first.
 *
 * Only a snapshot: nothing here is a detector sample, and nothing here is
 * written anywhere. Filters are the server's (user, panel, source judgement),
 * so a page is always a page of the filtered accounts.
 *
 * The filters and the page live in the URL (`live_*`, riskParams), so opening
 * an account and coming Back, a reload or a copied link shows the same
 * filtered page. Written by REPLACE: a filter is a view of the same tab, and
 * Back should leave the page rather than undo filters one by one.
 */
export default function LiveConnectionsTab({ onOpenUser }: { onOpenUser?: (userId: number) => void }) {
  const { t } = useRiskT()
  const scope = useQueryScope()
  const [urlParams, setUrlParams] = useSearchParams()
  const params: LiveParams = parseLiveParams(urlParams)
  const { page = 1, page_size: pageSize = 25 } = params
  const { data, isPending, error, refetch } = useLiveConnections(scope, params)
  // Read off what was actually asked for, so "no match" and the request cannot disagree about whether a
  // filter was set.
  const narrowed = params.user_id !== undefined || params.panel_id !== undefined
    || params.exclusion !== undefined || page > 1

  // liveSearch starts any filter change again at the first page: page 3 of one filter is not a page of
  // another.
  const update = (patch: Partial<LiveParams>) => setUrlParams(prev => liveSearch(prev, patch), { replace: true })

  // A server the URL names but the snapshot no longer lists (deleted since the link was copied) stays
  // selectable by its id, so the select never reads "all" while the list is still filtered by it.
  const panels = [...(data?.panels ?? [])]
  if (params.panel_id && !panels.some(p => p.id === params.panel_id)) panels.push({ id: params.panel_id, name: '' })

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <UserPicker value={params.user_id ?? null} onChange={id => update({ user_id: id ?? undefined })}
          aria-label={t('admin:risk_center.live.filter_user')} placeholder={t('admin:risk_center.live.filter_user')} />
        <div className="w-full sm:w-44">
          <NativeSelect aria-label={t('admin:risk_center.live.filter_panel')} value={params.panel_id ? String(params.panel_id) : ''}
            onChange={e => update({ panel_id: e.target.value === '' ? undefined : Number(e.target.value) })}>
            <option value="">{`${t('admin:risk_center.live.filter_panel')}: ${t('admin:risk_center.live.opt_all')}`}</option>
            {panels.map(p => <option key={p.id} value={String(p.id)}>{p.name || `#${p.id}`}</option>)}
          </NativeSelect>
        </div>
        <div className="w-full sm:w-44">
          <NativeSelect aria-label={t('admin:risk_center.live.filter_sources')} value={params.exclusion ?? ''}
            onChange={e => update({ exclusion: e.target.value || undefined })}>
            <option value="">{`${t('admin:risk_center.live.filter_sources')}: ${t('admin:risk_center.live.opt_all')}`}</option>
            <option value={EXCLUSION_KEPT}>{t('admin:risk_center.live.opt_kept')}</option>
            <option value={EXCLUSION_EXCLUDED}>{t('admin:risk_center.live.opt_excluded')}</option>
            {EXCLUSION_REASONS.map(r => <option key={r} value={r}>{t(`admin:risk_center.live.exclusion.${r}`)}</option>)}
          </NativeSelect>
        </div>
        <div className="ml-auto"><LiveRefreshButton /></div>
      </div>

      {isPending && <CardSkeleton lines={4} />}
      {error && !data && <ErrorState compact reason={error} retry={() => refetch()} />}
      {data && (
        <>
          <LiveSnapshotHeader view={data} />
          {/* An empty page is said only once a snapshot exists to say it of, and in words that match why
              it is empty. */}
          {data.items.length === 0 && data.snapshot.taken_at && <LiveEmpty view={data} narrowed={narrowed} />}
          {data.items.length > 0 && (
            <LiveConnectionList users={data.items} deviceWindowHours={data.device_window_hours}
              devicesUnavailable={data.devices_unavailable} onOpenUser={onOpenUser} />
          )}
          <DataTablePagination total={data.total} page={page} pageSize={pageSize} shown={data.items.length}
            pageSizeOptions={[10, 25, 50, 100]} className="rounded-lg border"
            onPageChange={p => update({ page: p })} onPageSizeChange={n => update({ page_size: n })} />
        </>
      )}
    </div>
  )
}
