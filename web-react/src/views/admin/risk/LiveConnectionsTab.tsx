import {
  Alert, Box, Button, CircularProgress, MenuItem, TextField, Tooltip, Typography, useTheme,
} from '@mui/material'
import RefreshIcon from '@mui/icons-material/Refresh'
import { useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'
import { isAxiosError } from 'axios'

import { pushSnack } from '@/components/SnackbarHost'
import { PagedTableFooter } from '@/components/PagedTableFooter'
import UserAutocomplete from '@/components/UserAutocomplete'
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

// A panel deleted since the snapshot is named by its id.
const names = (refs: PanelRef[]) => refs.map(r => r.name || `#${r.id}`).join(', ')

/**
 * The live snapshot's line and every caveat on it: when it was taken and by
 * what (a poll or a refresh), and each reason the list may be short — stale,
 * panels that failed, panels that cannot tell (S-UI, info not failure),
 * nodes with no reference yet (taken as still scanning), the per-account cap,
 * and fetches that could not be read for devices. Shared with the drawer's
 * connections tab.
 */
export function LiveSnapshotHeader({ view }: { view: LiveView }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const panelTz = useSiteStore(st => st.timezone)
  const s = view.snapshot
  if (!s.taken_at) {
    return (
      <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
        {t('admin:risk_center.live.snapshot_none')}
      </Typography>
    )
  }
  // The panel's timezone, like every other time in the admin; the relative
  // age beside it is what says how fresh the list is.
  const args = { time: formatDualTz(s.taken_at, panelTz), ago: agoText(s.age_seconds, t) }
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
        {s.source === 'refresh'
          ? t('admin:risk_center.live.snapshot_refresh', args)
          : t('admin:risk_center.live.snapshot_poll', args)}
      </Typography>
      {s.stale && (
        <Alert severity="warning" sx={{ fontSize: 13 }}>
          {t('admin:risk_center.live.stale', { minutes: Math.round(s.stale_after_seconds / 60) })}
        </Alert>
      )}
      {s.panels_unread.length > 0 && (
        <Alert severity="warning" sx={{ fontSize: 13 }}>
          {t('admin:risk_center.live.unread', { count: s.panels_unread.length, names: names(s.panels_unread) })}
        </Alert>
      )}
      {/* A panel with no live read (S-UI) is a fact about the panel, not a
          failure: as a warning it would sit there forever and teach the
          admin to skip the warning that matters. */}
      {s.panels_unsupported.length > 0 && (
        <Alert severity="info" sx={{ fontSize: 13 }}>
          {t('admin:risk_center.live.unsupported', {
            count: s.panels_unsupported.length, names: names(s.panels_unsupported),
          })}
        </Alert>
      )}
      {s.unreferenced_nodes > 0 && (
        <Alert severity="info" sx={{ fontSize: 13 }}>
          {t('admin:risk_center.live.unreferenced', { count: s.unreferenced_nodes })}
        </Alert>
      )}
      {s.truncated > 0 && (
        <Alert severity="info" sx={{ fontSize: 13 }}>
          {t('admin:risk_center.live.truncated', { count: s.truncated, max: LIVE_CONN_MAX_PER_USER })}
        </Alert>
      )}
      {view.devices_unavailable && (
        <Alert severity="warning" sx={{ fontSize: 13 }}>{t('admin:risk_center.live.devices_unavailable')}</Alert>
      )}
    </Box>
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
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const unread = view.snapshot.panels_unread.length
  const text = narrowed
    ? t('admin:risk_center.live.empty_filtered')
    : (unread > 0 ? t('admin:risk_center.live.empty_unread', { count: unread }) : t('admin:risk_center.live.empty'))
  return <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{text}</Typography>
}

/**
 * "Refresh now": one live read per panel, rationed for the whole fleet by
 * the server. Every outcome is said here — the global toast is off for this
 * request — because each needs its own words: refreshed, answered by the poll
 * that just ran, refused with the seconds to wait, or already running.
 */
export function LiveRefreshButton() {
  const { t } = useTranslation(['admin'])
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
    <Tooltip title={t('admin:risk_center.live.refresh_hint')} describeChild>
      <span>
        <Button variant="outlined" size="small" onClick={run} disabled={refresh.isPending}
          startIcon={refresh.isPending ? <CircularProgress size={14} /> : <RefreshIcon fontSize="small" />}>
          {t('admin:risk_center.live.refresh')}
        </Button>
      </span>
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
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const [urlParams, setUrlParams] = useSearchParams()
  const params: LiveParams = parseLiveParams(urlParams)
  const { page = 1, page_size: pageSize = 25 } = params
  const { data, isPending, error } = useLiveConnections(scope, params)
  // Read off what was actually asked for, so "no match" and the request
  // cannot disagree about whether a filter was set.
  const narrowed = params.user_id !== undefined || params.panel_id !== undefined
    || params.exclusion !== undefined || page > 1

  // liveSearch starts any filter change again at the first page: page 3 of
  // one filter is not a page of another.
  const update = (patch: Partial<LiveParams>) => setUrlParams(prev => liveSearch(prev, patch), { replace: true })

  // A panel the URL names but the snapshot no longer lists (deleted since the
  // link was copied) stays selectable by its id, so the select never reads
  // "all" while the list is still filtered by it.
  const panels = [...(data?.panels ?? [])]
  if (params.panel_id && !panels.some(p => p.id === params.panel_id)) panels.push({ id: params.panel_id, name: '' })

  const err = error
    ? (isAxiosError(error) ? String(error.response?.data?.error ?? error.message) : String(error))
    : ''

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'center' }}>
        <UserAutocomplete value={params.user_id ?? null} onChange={id => update({ user_id: id ?? undefined })}
          label={t('admin:risk_center.live.filter_user')} width={240} />
        <TextField select size="small" label={t('admin:risk_center.live.filter_panel')} sx={{ width: 180 }}
          value={params.panel_id ? String(params.panel_id) : ''}
          onChange={e => update({ panel_id: e.target.value === '' ? undefined : Number(e.target.value) })}>
          <MenuItem value="">{t('admin:risk_center.live.opt_all')}</MenuItem>
          {panels.map(p => <MenuItem key={p.id} value={String(p.id)}>{p.name || `#${p.id}`}</MenuItem>)}
        </TextField>
        <TextField select size="small" label={t('admin:risk_center.live.filter_sources')} sx={{ width: 180 }}
          value={params.exclusion ?? ''} onChange={e => update({ exclusion: e.target.value || undefined })}>
          <MenuItem value="">{t('admin:risk_center.live.opt_all')}</MenuItem>
          <MenuItem value={EXCLUSION_KEPT}>{t('admin:risk_center.live.opt_kept')}</MenuItem>
          <MenuItem value={EXCLUSION_EXCLUDED}>{t('admin:risk_center.live.opt_excluded')}</MenuItem>
          {EXCLUSION_REASONS.map(r => (
            <MenuItem key={r} value={r}>{t(`admin:risk_center.live.exclusion.${r}`)}</MenuItem>
          ))}
        </TextField>
        <Box sx={{ flex: 1 }} />
        <LiveRefreshButton />
      </Box>

      {isPending && <Box sx={{ p: 3 }}><CircularProgress size={24} /></Box>}
      {err && <Typography color="error" sx={{ fontSize: 13 }}>{err}</Typography>}
      {data && (
        <>
          <LiveSnapshotHeader view={data} />
          {/* An empty page is said only once a snapshot exists to say it of,
              and in words that match why it is empty. */}
          {data.items.length === 0 && data.snapshot.taken_at && (
            <LiveEmpty view={data} narrowed={narrowed} />
          )}
          {data.items.length > 0 && (
            <LiveConnectionList users={data.items} deviceWindowHours={data.device_window_hours}
              devicesUnavailable={data.devices_unavailable} onOpenUser={onOpenUser} />
          )}
          <PagedTableFooter total={data.total} page={page} pageSize={pageSize}
            onPageChange={p => update({ page: p })} onPageSizeChange={n => update({ page_size: n })} />
        </>
      )}
    </Box>
  )
}
