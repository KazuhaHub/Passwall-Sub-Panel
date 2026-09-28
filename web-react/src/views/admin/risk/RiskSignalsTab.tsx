import { useMemo, useState } from 'react'
import { Box, Chip, CircularProgress, FormControlLabel, IconButton, Switch, Table, TableBody, TableCell,
  TableContainer, TableHead, TableRow, Tooltip, Typography, useTheme,
} from '@mui/material'
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown'
import KeyboardArrowUpIcon from '@mui/icons-material/KeyboardArrowUp'
import PersonSearchOutlinedIcon from '@mui/icons-material/PersonSearchOutlined'
import { useTranslation } from 'react-i18next'
import { isAxiosError } from 'axios'

import { RISK_KINDS, type RiskKind, type RiskUserRow } from '@/api/riskSignals'
import { useRiskSignals } from '@/query/riskSignals'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import { needsAttention, oldestUpdate, riskCodeText, sortRiskRows } from '@/utils/riskSignals'
import { DetectorStateChip } from './evidence/DetectorStateChip'
import { RiskKindChip, RiskKindEvidence } from './evidence/RiskEvidence'
import { stateColor, stateLabelKey } from './evidence/state'

const KIND_DEFAULT: Record<RiskKind, string> = {
  sub_spread: '订阅多地', devices: '设备数', usage_shift: '用量变化', login_country: '登录国家',
}
// The table's columns: user, the four kinds, concurrent locations, last
// computed, and the expand button.
const COLS = 8

/**
 * The observe-only risk signals, one row per account, beside the
 * concurrent-location verdict.
 *
 * Every signal is its own column with its own state: there is no score and no
 * combined verdict, because four "suspect"s are four things to read, not one
 * finding. Colours and words come from the shared state vocabulary
 * (evidence/state), so "cannot tell" is never drawn as clean on either tab.
 * Nothing here acts on an account — the server computes these hourly and
 * enforces none of them.
 */
export default function RiskSignalsTab({ onOpenGeo, onOpenUser }: {
  onOpenGeo: () => void
  /** Opens the account in the risk center's lookup; no button without it. */
  onOpenUser?: (userId: number) => void
}) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const scope = useQueryScope()
  const { data, isPending, error } = useRiskSignals(scope)
  // Off by default: on a fleet where no client sends x-hwid, every row reads
  // devices: unknown, and the table would be the whole fleet. The switch is
  // the way to the denominator — a signal that stopped judging shows there.
  const [showAll, setShowAll] = useState(false)
  const [open, setOpen] = useState<ReadonlySet<number>>(() => new Set())
  // The server answers by user id; the admin needs what to look at first. A
  // sorted copy, so the cached array every other reader shares stays intact.
  const sorted = useMemo(() => sortRiskRows(data ?? []), [data])
  const rows = useMemo(() => (showAll ? sorted : sorted.filter(needsAttention)), [sorted, showAll])

  if (isPending) return <Box sx={{ p: 3 }}><CircularProgress size={24} /></Box>

  // 503 means this build does not compute the signals. Reporting that as
  // "nothing computed yet" would describe a watched fleet when nothing is
  // being watched, so it surfaces as its own message.
  const err = (() => {
    if (!error) return ''
    if (isAxiosError(error) && error.response?.status === 503) {
      return t('admin:risk_signals.unwired', { defaultValue: '本部署未启用风险信号。' })
    }
    return isAxiosError(error)
      ? String(error.response?.data?.error ?? error)
      : String(error)
  })()

  const toggle = (id: number) => setOpen(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  })

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
        {t('admin:risk_signals.intro', {
          defaultValue: '除「异地并发」外的四种只提示信号，定期（默认每小时）按账号重新计算：订阅多地（跨天、按客户端合并的省份）、设备数（客户端声明的 x-hwid）、用量变化（与自己基线期（默认 4 周）相比）和登录国家（面板登录出现新国家）。它们都不会暂停或修改任何账号；「无法判断」「无数据」不等于「正常」。默认只列出需要关注的账号。',
        })}
      </Typography>
      <FormControlLabel
        label={t('admin:risk_signals.show_all', { defaultValue: '显示全部账号' })}
        control={<Switch checked={showAll} onChange={(_, c) => setShowAll(c)} />}
        sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1, fontSize: 14 } }} />
      {err && <Typography color="error" sx={{ fontSize: 13 }}>{err}</Typography>}

      <TableContainer>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell>{t('admin:risk_signals.col_user', { defaultValue: '用户' })}</TableCell>
              {RISK_KINDS.map(k => (
                <TableCell key={k}>{t(`admin:risk_signals.kind.${k}`, { defaultValue: KIND_DEFAULT[k] })}</TableCell>
              ))}
              <TableCell>{t('admin:risk_signals.col_geo', { defaultValue: '异地并发' })}</TableCell>
              <TableCell>{t('admin:risk_signals.col_updated', { defaultValue: '最后计算' })}</TableCell>
              <TableCell />
            </TableRow>
          </TableHead>
          <TableBody>
            {rows.length === 0 && !err && (
              <TableRow><TableCell colSpan={COLS}>
                <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
                  {/* Two different facts: nothing has been computed, or it
                      has and nothing needs a look. The second must not read
                      as the first, or a working worker looks broken. */}
                  {(data?.length ?? 0) === 0
                    ? t('admin:risk_signals.empty', { defaultValue: '还没有计算结果——风险信号按设定的间隔（默认每小时）计算。' })
                    : t('admin:risk_signals.empty_attention', { defaultValue: '目前没有需要关注的账号。' })}
                </Typography>
              </TableCell></TableRow>
            )}
            {rows.map(r => (
              <RiskRow key={r.user_id} row={r} open={open.has(r.user_id)}
                onToggle={() => toggle(r.user_id)} onOpenGeo={onOpenGeo} onOpenUser={onOpenUser} />
            ))}
          </TableBody>
        </Table>
      </TableContainer>
    </Box>
  )
}

function RiskRow({ row, open, onToggle, onOpenGeo, onOpenUser }: {
  row: RiskUserRow
  open: boolean
  onToggle: () => void
  onOpenGeo: () => void
  onOpenUser?: (userId: number) => void
}) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const panelTz = useSiteStore(s => s.timezone)
  const geo = row.geo
  return (
    <>
      <TableRow hover>
        <TableCell>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
            <span>{row.upn || row.display_name || `#${row.user_id}`}</span>
            {onOpenUser && (
              <Tooltip title={t('admin:risk_center.open_user', { defaultValue: '查看用户' })}>
                <IconButton size="small" onClick={() => onOpenUser(row.user_id)}>
                  <PersonSearchOutlinedIcon fontSize="inherit" />
                </IconButton>
              </Tooltip>
            )}
          </Box>
        </TableCell>
        {/* By RISK_KINDS, never by the row's own list: a kind this build does
            not know has no column, and drawing it in another kind's cell
            would put a verdict under the wrong heading. */}
        {RISK_KINDS.map(k => (
          <TableCell key={k}><RiskKindChip sig={row.signals.find(s => s.kind === k)} /></TableCell>
        ))}
        <TableCell>
          {geo ? (
            <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5, alignItems: 'center' }}>
              <Tooltip title={t('admin:risk_signals.geo_open', { defaultValue: '在「异地并发」中查看' })}>
                <Chip size="small" clickable onClick={onOpenGeo} color={stateColor(geo.state)}
                  label={t(`admin:${stateLabelKey(geo.state)}`)} />
              </Tooltip>
              {/* The latch outlives the state, as on the Geo tab: a flagged
                  account that went idle is still flagged. */}
              {geo.flagged && geo.state !== 'flagged' && (
                <Chip size="small" variant="outlined" color="error"
                  label={t('admin:geo_anomalies.latched', { defaultValue: '仍在标记中' })} />
              )}
            </Box>
          ) : '—'}
        </TableCell>
        {/* The OLDEST kind's time, so one skipped for days shows its age;
            each kind's own time is in its chip's tooltip. */}
        <TableCell sx={{ fontSize: 12, whiteSpace: 'nowrap' }}>{formatMsDualTz(oldestUpdate(row), panelTz)}</TableCell>
        <TableCell align="right">
          <IconButton size="small" onClick={onToggle}
            aria-label={open
              ? t('admin:risk_signals.collapse', { defaultValue: '收起' })
              : t('admin:risk_signals.expand', { defaultValue: '查看证据' })}>
            {open ? <KeyboardArrowUpIcon fontSize="small" /> : <KeyboardArrowDownIcon fontSize="small" />}
          </IconButton>
        </TableCell>
      </TableRow>
      {open && (
        <TableRow>
          <TableCell colSpan={COLS} sx={{ bgcolor: md.surfaceContainerLow }}>
            <RiskEvidencePanel row={row} />
          </TableCell>
        </TableRow>
      )}
    </>
  )
}

/** Every shown kind that carries evidence, in column order. Exported for the
 *  risk center's lookup, so one account's evidence reads the same there. */
export function RiskEvidencePanel({ row }: { row: RiskUserRow }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const shown = RISK_KINDS.flatMap(k => {
    const s = row.signals.find(x => x.kind === k)
    return s && s.evidence && typeof s.evidence === 'object' ? [s] : []
  })
  if (shown.length === 0) {
    return (
      <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
        {t('admin:risk_signals.no_evidence', { defaultValue: '没有可显示的证据' })}
      </Typography>
    )
  }
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, py: 1 }}>
      {shown.map(s => (
        <Box key={s.kind} sx={{ display: 'flex', flexDirection: 'column', gap: 0.75 }}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
            <Typography sx={{ fontWeight: 600, fontSize: 13, color: md.onSurface }}>
              {t(`admin:risk_signals.kind.${s.kind}`, { defaultValue: KIND_DEFAULT[s.kind] })}
            </Typography>
            <DetectorStateChip state={s.state} code={s.code} />
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{riskCodeText(s, t)}</Typography>
          </Box>
          <RiskKindEvidence kind={s.kind} evidence={s.evidence} />
        </Box>
      ))}
    </Box>
  )
}
