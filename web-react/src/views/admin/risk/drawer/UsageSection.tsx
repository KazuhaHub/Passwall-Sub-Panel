import { useState } from 'react'
import { Alert, Box, Button, MenuItem, TextField, ToggleButton, ToggleButtonGroup, Typography, useTheme } from '@mui/material'
import { useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import type { DestUsage, DestUsageNode } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import HelpTip from '@/components/HelpTip'
import { useDestSettings, useDestUserUsage } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'

const D = 'admin:access_control.drawer.'

/** The longest window offered: a week, held to the usage retention. */
const MAX_WINDOW_DAYS = 7

/**
 * One account's per-site usage (S16, tier B). Nothing is read until the
 * administrator asks: every read is a row in the admin audit log, so a page that
 * fetched it on open would leave one for every drawer glanced at. Choosing
 * another range or node is another deliberate read. The window is 24 hours or
 * min(7, retention) days; the retention is a setting and the words interpolate it.
 *
 * `available` is whether any node the account has a client on records usage now:
 * when it is false, "no usage" would say nothing, so the section says that and
 * offers no read.
 */
export default function UsageSection({ userId, available, nodes }: {
  userId: number
  available: boolean | null
  nodes: DestUsageNode[] | null
}) {
  const { t, i18n } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const navigate = useNavigate()
  const settings = useDestSettings(scope)
  const read = useDestUserUsage(scope)
  const [range, setRange] = useState('24h')
  const [panelId, setPanelId] = useState(0)
  const [asked, setAsked] = useState(false)
  const num = new Intl.NumberFormat(i18n.language)

  // An answer the server cannot give (null) draws nothing: there is no claim to make either way.
  if (available === null) return null

  const days = Math.min(MAX_WINDOW_DAYS, settings.data?.effective.dest_usage_retention_days ?? 1)
  const ranges = ['24h', ...(days > 1 ? [`${days}d`] : [])]
  // One click is one read (one audit row): nothing starts while one is in flight.
  const run = async (r: string, p: number) => {
    if (read.isPending) return
    setAsked(true)
    try { await read.mutateAsync({ userId, range: r, panelId: p || undefined }) } catch { /* shown by read.isError */ }
  }
  const requestedDays = range === '24h' ? 1 : parseInt(range, 10)
  const answer: DestUsage | undefined = read.data

  const head = (
    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1, flexWrap: 'wrap' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
        <Typography component="h3" sx={{ fontSize: 14, fontWeight: 600 }}>{t(`${D}usage_title`)}</Typography>
        <HelpTip textKey={`admin:access_control.drawer.usage_help`} labelKey="admin:access_control.drawer.usage_help_label" />
      </Box>
    </Box>
  )

  if (available === false) {
    return (
      <Box data-testid="access-usage" sx={{ mt: 1, display: 'flex', flexDirection: 'column', gap: 1 }}>
        {head}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap', p: 1.5, borderRadius: 2, bgcolor: md.surfaceContainerHigh }}>
          <Typography variant="body2" sx={{ flex: 1, minWidth: 0 }}>{t(`${D}usage_not_recorded`)}</Typography>
          <Button size="small" onClick={() => void navigate('/admin/access-control?sheet=nodes')}>{t(`${D}usage_nodes`)}</Button>
        </Box>
      </Box>
    )
  }

  return (
    <Box data-testid="access-usage" data-user={userId} sx={{ mt: 1, display: 'flex', flexDirection: 'column', gap: 1 }}>
      {head}
      <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${D}usage_audit`)}</Typography>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
        <ToggleButtonGroup exclusive size="small" value={range} aria-label={t(`${D}usage_range_label`)}
          onChange={(_, v: string | null) => { if (v) { setRange(v); if (asked) void run(v, panelId) } }}>
          {ranges.map(r => (
            <ToggleButton key={r} value={r} disabled={read.isPending}>{r === '24h' ? t(`${D}usage_range_24h`) : t(`${D}usage_range_days`, { count: parseInt(r, 10) })}</ToggleButton>
          ))}
        </ToggleButtonGroup>
        {(nodes?.length ?? 0) > 1 && (
          <TextField select size="small" label={t(`${D}usage_node_label`)} value={panelId} sx={{ minWidth: 160 }} disabled={read.isPending}
            onChange={e => { const p = Number(e.target.value); setPanelId(p); if (asked) void run(range, p) }}>
            <MenuItem value={0}>{t(`${D}usage_node_all`)}</MenuItem>
            {nodes!.map(n => <MenuItem key={n.panel_id} value={n.panel_id}>{n.name}</MenuItem>)}
          </TextField>
        )}
        <AsyncButton size="small" variant={asked ? 'text' : 'outlined'} onClick={() => run(range, panelId)}>{t(`${D}usage_view`)}</AsyncButton>
      </Box>
      {read.isError && (
        <Alert severity="error" action={<Button color="inherit" size="small" disabled={read.isPending} onClick={() => void run(range, panelId)}>{t(`${D}retry`)}</Button>}>
          {t(`${D}usage_load_failed`)}
        </Alert>
      )}
      {answer && !read.isError && (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.75 }}>
          {answer.effective_days < requestedDays && (
            <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${D}usage_window`, { days: answer.effective_days })}</Typography>
          )}
          {answer.total === 0
            ? <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${D}usage_none`)}</Typography>
            : (
              <>
                <Box sx={{ display: 'flex', justifyContent: 'space-between', px: 1.25, fontSize: 12, color: md.onSurfaceVariant }}>
                  <span>{t(`${D}usage_col_site`)}</span><span>{t(`${D}usage_col_count`)}</span>
                </Box>
                {answer.items.map(it => (
                  <Box key={it.site} data-testid="usage-site" sx={{ display: 'flex', justifyContent: 'space-between', gap: 1, p: 1.25, borderRadius: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
                    <Box component="span" sx={{ minWidth: 0, overflowWrap: 'anywhere', fontSize: 14 }}>{it.site}</Box>
                    <Box component="span" sx={{ fontVariantNumeric: 'tabular-nums', fontWeight: 600 }}>{num.format(it.count)}</Box>
                  </Box>
                ))}
                {answer.others.sites > 0 && (
                  <Box data-testid="usage-others" sx={{ display: 'flex', justifyContent: 'space-between', gap: 1, px: 1.25, color: md.onSurfaceVariant, fontSize: 14 }}>
                    <span>{t(`${D}usage_others`, { sites: num.format(answer.others.sites) })}</span>
                    <Box component="span" sx={{ fontVariantNumeric: 'tabular-nums' }}>{num.format(answer.others.count)}</Box>
                  </Box>
                )}
                <Typography variant="body2" sx={{ px: 1.25 }}>{t(`${D}usage_total`, { n: num.format(answer.total) })}</Typography>
              </>
            )}
          {answer.nodes.length > 0 && (
            <Typography variant="caption" sx={{ color: md.onSurfaceVariant }}>{t(`${D}usage_from`, { nodes: answer.nodes.map(n => n.name).join(t(`${D}hits_list_separator`)) })}</Typography>
          )}
          {answer.dropped_in_range > 0 && (
            <Typography variant="body2" sx={{ color: md.onSurface }}>{t(`${D}usage_dropped`, { n: num.format(answer.dropped_in_range) })}</Typography>
          )}
        </Box>
      )}
    </Box>
  )
}
