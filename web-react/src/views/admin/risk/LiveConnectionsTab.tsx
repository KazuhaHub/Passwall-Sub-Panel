import { useState } from 'react'
import {
  Alert, Box, Button, CircularProgress, MenuItem, TextField, Tooltip, Typography, useTheme,
} from '@mui/material'
import RefreshIcon from '@mui/icons-material/Refresh'
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
import { agoText } from '@/utils/riskCenter'
import LiveConnectionList from './LiveConnectionList'

// A panel deleted since the snapshot is named by its id.
const names = (refs: PanelRef[]) => refs.map(r => r.name || `#${r.id}`).join(', ')

/**
 * The live snapshot's line and every caveat on it: when it was taken and by
 * what (a poll or a refresh), and each reason the list may be short — stale,
 * panels that failed, panels that cannot tell (S-UI, info not failure),
 * nodes whose whole window was trusted once, the per-account cap, and fetches
 * that could not be read for devices. Shared with the user lookup.
 */
export function LiveSnapshotHeader({ view }: { view: LiveView }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const s = view.snapshot
  if (!s.taken_at) {
    return (
      <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
        {t('admin:risk_center.live.snapshot_none')}
      </Typography>
    )
  }
  const args = { time: new Date(s.taken_at).toLocaleString(), ago: agoText(s.age_seconds, t) }
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
 */
export default function LiveConnectionsTab({ onOpenUser }: { onOpenUser?: (userId: number) => void }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(25)
  const [userId, setUserId] = useState<number | null>(null)
  const [panelId, setPanelId] = useState<number | ''>('')
  const [exclusion, setExclusion] = useState('')

  const params: LiveParams = {
    page, page_size: pageSize,
    ...(userId ? { user_id: userId } : {}),
    ...(panelId !== '' ? { panel_id: panelId } : {}),
    ...(exclusion ? { exclusion } : {}),
  }
  const { data, isPending, error } = useLiveConnections(scope, params)

  // Any filter change starts again at the first page: page 3 of one filter
  // is not a page of another.
  const filter = <T,>(set: (v: T) => void) => (v: T) => { set(v); setPage(1) }

  const err = error
    ? (isAxiosError(error) ? String(error.response?.data?.error ?? error.message) : String(error))
    : ''

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'center' }}>
        <UserAutocomplete value={userId} onChange={filter(setUserId)}
          label={t('admin:risk_center.live.filter_user')} width={240} />
        <TextField select size="small" label={t('admin:risk_center.live.filter_panel')} sx={{ width: 180 }}
          value={panelId === '' ? '' : String(panelId)}
          onChange={e => filter(setPanelId)(e.target.value === '' ? '' : Number(e.target.value))}>
          <MenuItem value="">{t('admin:risk_center.live.opt_all')}</MenuItem>
          {(data?.panels ?? []).map(p => <MenuItem key={p.id} value={String(p.id)}>{p.name || `#${p.id}`}</MenuItem>)}
        </TextField>
        <TextField select size="small" label={t('admin:risk_center.live.filter_sources')} sx={{ width: 180 }}
          value={exclusion} onChange={e => filter(setExclusion)(e.target.value)}>
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
          {/* "Nobody is connected" only once a snapshot exists to say so. */}
          {data.items.length === 0 && data.snapshot.taken_at && (
            <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{t('admin:risk_center.live.empty')}</Typography>
          )}
          {data.items.length > 0 && (
            <LiveConnectionList users={data.items} deviceWindowHours={data.device_window_hours}
              devicesUnavailable={data.devices_unavailable} onOpenUser={onOpenUser} />
          )}
          <PagedTableFooter total={data.total} page={page} pageSize={pageSize}
            onPageChange={setPage} onPageSizeChange={n => { setPageSize(n); setPage(1) }} />
        </>
      )}
    </Box>
  )
}
