import { Box, Button, TextField, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import type { RelayLine } from '@/api/types'
import type { CloudflareRanges, LineSourceGuard } from '@/api/sourceGuard'
import InfoTip from '@/components/InfoTip'

interface Props {
  line: RelayLine
  /** The source addresses as typed (one per line). */
  text: string
  onText: (text: string) => void
  /** What the panel says of the saved line; absent for a line that is not saved yet or while it loads. */
  guard?: LineSourceGuard
  /** Whether the landing can check sources; absent while unknown. */
  canGuard?: boolean
  /** Why it cannot: the server, or the listener's transport and settings. */
  cannotGuardReason?: 'server' | 'listener'
  cloudflare?: CloudflareRanges
  /** Adds a refused address to the saved line. */
  onAdd: (ip: string) => void
  busy?: boolean
}

/** Source addresses of a forward, external or CDN line (ADR 0044): the admin's own list, the set that is in force, and what the landing refused. */
export default function SourceAddressesField({ line, text, onText, guard, canGuard, cloudflare, onAdd, busy, cannotGuardReason }: Props) {
  const { t, i18n } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const managed = (line.entry_panel_id ?? 0) > 0
  const cdn = line.mode === 'cdn'
  const hint = cdn ? t('admin:nodes.source.hint_cdn') : managed ? t('admin:nodes.source.hint_managed') : t('admin:nodes.source.hint_external')
  const time = (iso?: string) => (iso ? new Date(iso).toLocaleString(i18n.language) : '')
  const own = text.trim() !== ''
  // A CDN line that names no addresses of its own uses Cloudflare's; otherwise
  // the set is what the panel works out for the saved line.
  const effective = cdn && !own ? (cloudflare?.cidrs ?? guard?.effective_cidrs ?? []) : (guard?.effective_cidrs ?? [])
  return (
    <Box data-testid="source-addresses" sx={{ display: 'flex', flexDirection: 'column', gap: 0.75 }}>
      <TextField size="small" multiline minRows={2} maxRows={8} label={t('admin:nodes.source.title')} value={text}
        onChange={e => onText(e.target.value)} placeholder={t('admin:nodes.source.placeholder')}
        helperText={hint}
        slotProps={{ htmlInput: { spellCheck: false, style: { fontFamily: 'monospace', fontSize: 12 } } }} />
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t('admin:nodes.source.effective')}</Typography>
        <InfoTip text={t('admin:nodes.source.hint')} />
      </Box>
      <Box data-testid="source-effective" sx={{ fontFamily: 'monospace', fontSize: 12, color: md.onSurfaceVariant, wordBreak: 'break-all' }}>
        {effective.length > 0 ? effective.join('  ') : t('admin:nodes.source.effective_unknown')}
      </Box>
      {cdn && !own && cloudflare && (
        <Typography data-testid="source-cloudflare" sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
          {cloudflare.source === 'fetched'
            ? t('admin:nodes.source.cloudflare_fetched', { time: time(cloudflare.fetched_at) })
            : t('admin:nodes.source.cloudflare_builtin', { date: cloudflare.builtin_date })}
          {!cloudflare.auto_fetch && ` · ${t('admin:nodes.source.cloudflare_auto_off')}`}
        </Typography>
      )}
      {canGuard === false && (
        <Typography data-testid="source-cannot-guard" sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t(cannotGuardReason === 'listener' ? 'admin:nodes.source.cannot_guard_listener' : 'admin:nodes.source.cannot_guard')}</Typography>
      )}
      {guard?.withheld && (
        <Typography data-testid="source-withheld" sx={{ fontSize: 12, color: md.error }}>{t(`admin:nodes.source.withheld.${guard.withheld}`)}</Typography>
      )}
      {(guard?.refused ?? []).map(r => (
        <Box key={r.ip} data-testid="source-refused" sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
          <Typography sx={{ fontSize: 12, color: md.error, flex: 1, minWidth: 200 }}>
            {t(r.kind === 'wrong_line' ? 'admin:nodes.source.refused_wrong_line' : 'admin:nodes.source.refused', { ip: r.ip, count: r.count, time: time(r.last_seen) })}
          </Typography>
          {r.kind !== 'wrong_line' && r.addable && (
            <Button size="small" variant="outlined" disabled={busy} onClick={() => onAdd(r.ip)}>{t('admin:nodes.source.add')}</Button>
          )}
        </Box>
      ))}
    </Box>
  )
}
