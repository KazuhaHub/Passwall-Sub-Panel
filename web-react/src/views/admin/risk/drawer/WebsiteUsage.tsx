import { useEffect, useRef, useState } from 'react'
import { Alert, Box, Button, Skeleton, Stack, ToggleButton, ToggleButtonGroup, Typography, useTheme } from '@mui/material'
import { getDestinationUsage, type DestinationUsagePage } from '@/api/accessControl'
import HelpTip from '@/components/HelpTip'
import HitLossNotice from '../../accessControl/HitLossNotice'
import { useAccessTranslation } from '../../accessControl/useAccessTranslation'

const P = 'admin:access_control.account.'
export default function WebsiteUsage({ userId, available, nodes, retention, disabled }: {
  userId: number; available: boolean; nodes: Array<{ panel_id: number; name: string }>; retention: number; disabled: boolean
}) {
  const { t, number } = useAccessTranslation(['admin']), theme = useTheme()
  const [range, setRange] = useState('24h'), [page, setPage] = useState<DestinationUsagePage | null>(null)
  const [pending, setPending] = useState(false), [failed, setFailed] = useState(false)
  const active = useRef<AbortController | null>(null)
  useEffect(() => () => active.current?.abort(), [])
  const days = Math.min(7, retention), ranges = days === 1 ? ['24h'] : ['24h', `${days}d`]
  const load = async () => {
    if (active.current || disabled || !available) return
    const controller = new AbortController(); active.current = controller
    setPending(true); setFailed(false); setPage(null)
    try {
      const result = await getDestinationUsage(userId, range, controller.signal)
      if (!controller.signal.aborted) setPage(result)
    } catch { if (!controller.signal.aborted) setFailed(true) }
    finally { if (!controller.signal.aborted) { active.current = null; setPending(false) } }
  }
  if (!available) return <Typography variant="body2">{t(`${P}usage_unavailable`)}</Typography>
  const maximum = Math.max(1, ...(page?.items.map(row => row.count) ?? []))
  return <Stack spacing={1.25}>
    <Stack direction="row" sx={{ alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
      <Typography variant="subtitle2">{t(`${P}usage_title`)}</Typography><HelpTip textKey="admin:access_control.account.usage_ip_help" />
      <ToggleButtonGroup exclusive value={range} disabled={disabled || pending} aria-label={t(`${P}usage_range`)} onChange={(_, value: string | null) => { if (value) { setRange(value); setPage(null); setFailed(false) } }}>
        {ranges.map(value => <ToggleButton key={value} value={value} sx={{ minHeight: 44, minWidth: 44 }}>{value === '24h' ? t('admin:access_control.records.day') : t('admin:access_control.records.days', { days })}</ToggleButton>)}
      </ToggleButtonGroup>
    </Stack>
    <Stack direction="row" sx={{ alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
      <Button disabled={disabled || pending} onClick={() => void load()} sx={{ minHeight: 44, minWidth: 44, alignSelf: 'flex-start' }}>{t(`${P}usage_show`)}</Button>
      <Typography variant="caption">{t(`${P}usage_audited`)}</Typography>
    </Stack>
    {pending && <Skeleton variant="rounded" height={80} aria-label={t(`${P}usage_loading`)} />}
    {failed && <Alert severity="error">{t(`${P}usage_failed`)}</Alert>}
    {page && <>
      {page.items.length === 0 && <Typography variant="body2">{t(`${P}usage_empty`)}</Typography>}
      {page.items.map(row => <Box key={row.site} sx={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) auto', gap: 1, alignItems: 'center' }}>
        <Box sx={{ minWidth: 0 }}><Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{row.site}</Typography>
          <Box sx={{ width: `${60 * row.count / maximum}%`, height: 6, borderRadius: 1, bgcolor: theme.palette.primary.main }} />
        </Box><Typography variant="body2" sx={{ fontVariantNumeric: 'tabular-nums' }}>{number(row.count)}</Typography>
      </Box>)}
      <HitLossNotice losses={page.losses} />
    </>}
    <Typography variant="caption" sx={{ overflowWrap: 'anywhere' }}>{t(`${P}usage_detail`, { days: retention, nodes: nodes.map(node => node.name).join(', ') })}</Typography>
  </Stack>
}
