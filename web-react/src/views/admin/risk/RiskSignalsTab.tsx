import { useMemo, useState, type ReactNode } from 'react'
import { Box, Chip, CircularProgress, FormControlLabel, IconButton, Switch, Table, TableBody, TableCell,
  TableContainer, TableHead, TableRow, Tooltip, Typography, useTheme,
} from '@mui/material'
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown'
import KeyboardArrowUpIcon from '@mui/icons-material/KeyboardArrowUp'
import PersonSearchOutlinedIcon from '@mui/icons-material/PersonSearchOutlined'
import { useTranslation } from 'react-i18next'
import { isAxiosError } from 'axios'

import {
  RISK_KINDS, type DevicesEvidence, type LoginCountryEvidence, type RiskKind, type RiskSignal, type RiskUserRow,
  type SubSpreadEvidence, type UsageShiftEvidence,
} from '@/api/riskSignals'
import { useRiskSignals } from '@/query/riskSignals'
import { useQueryScope } from '@/query/useQueryScope'
import { countryFlag } from '@/utils/geo'
import { regionNamer } from '@/utils/regionName'
import {
  dayBits, dayLabels, formatGB, needsAttention, oldestUpdate, placeLabel, riskCodeText, sortRiskRows,
} from '@/utils/riskSignals'
import { stateColor } from './GeoAnomaliesTab'

const KIND_DEFAULT: Record<RiskKind, string> = {
  sub_spread: '订阅多地', devices: '设备数', usage_shift: '用量变化', login_country: '登录国家',
}
const STATE_DEFAULT: Record<string, string> = {
  flagged: '已标记', suspect: '疑似', clean: '正常', unknown: '无法判断', idle: '无数据', exempt: '已豁免', disabled: '未启用',
}
// The table's columns: user, the four kinds, concurrent locations, last
// computed, and the expand button.
const COLS = 8

const when = (ms: number) => (ms ? new Date(ms).toLocaleString() : '—')

/**
 * The observe-only risk signals, one row per account, beside the
 * concurrent-location verdict.
 *
 * Every signal is its own column with its own state: there is no score and no
 * combined verdict, because four "suspect"s are four things to read, not one
 * finding. Colours come from the Geo tab's stateColor, so "cannot tell" is
 * never drawn as clean on either tab. Nothing here acts on an account — the
 * server computes these hourly and enforces none of them.
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
                  label={t(`admin:geo_anomalies.state_${geo.state}`, { defaultValue: geo.state })} />
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
        <TableCell sx={{ fontSize: 12, whiteSpace: 'nowrap' }}>{when(oldestUpdate(row))}</TableCell>
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

/** One kind's cell: its state, explained by its code and its own time. A kind
 *  with no row is "not computed", never blank and never clean. Exported for
 *  the risk center's lookup, which shows one account's four. */
export function RiskKindChip({ sig }: { sig: RiskSignal | undefined }) {
  const { t } = useTranslation(['admin'])
  if (!sig) {
    return (
      <Tooltip title={t('admin:risk_signals.not_computed', { defaultValue: '尚未计算' })}>
        <Chip size="small" variant="outlined" label="—" />
      </Tooltip>
    )
  }
  const updated = t('admin:risk_signals.col_updated', { defaultValue: '最后计算' })
  return (
    <Tooltip title={`${riskCodeText(sig, t)} · ${updated} ${when(sig.updated_at_ms)}`}>
      <Chip size="small" color={stateColor(sig.state)}
        label={t(`admin:risk_signals.state.${sig.state}`, { defaultValue: STATE_DEFAULT[sig.state] ?? sig.state })} />
    </Tooltip>
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
            <Chip size="small" color={stateColor(s.state)}
              label={t(`admin:risk_signals.state.${s.state}`, { defaultValue: STATE_DEFAULT[s.state] ?? s.state })} />
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{riskCodeText(s, t)}</Typography>
          </Box>
          {s.kind === 'sub_spread' && <SubSpreadPanel ev={s.evidence as SubSpreadEvidence} />}
          {s.kind === 'devices' && <DevicesPanel ev={s.evidence as DevicesEvidence} />}
          {s.kind === 'usage_shift' && <UsagePanel ev={s.evidence as UsageShiftEvidence} />}
          {s.kind === 'login_country' && <LoginPanel ev={s.evidence as LoginCountryEvidence} />}
        </Box>
      ))}
    </Box>
  )
}

/** One cell per window day, oldest first, each titled with its panel-local
 *  date. A filled cell is a day the place, client or device was seen on. */
function DayStrip({ mask, labels }: { mask: number; labels: string[] }) {
  const md = useTheme().palette.md
  return (
    <Box data-testid="day-strip" sx={{ display: 'inline-flex', gap: '2px', flexShrink: 0 }}>
      {dayBits(mask, labels.length).map((on, i) => (
        <Box key={i} data-on={on ? 'true' : 'false'} title={labels[i]} sx={{
          width: 10, height: 10, borderRadius: '2px',
          bgcolor: on ? md.primary : md.surfaceContainerHighest, border: `1px solid ${md.outlineVariant}`,
        }} />
      ))}
    </Box>
  )
}

function Caption({ children }: { children: ReactNode }) {
  const md = useTheme().palette.md
  return <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{children}</Typography>
}

function Subtitle({ children }: { children: ReactNode }) {
  const md = useTheme().palette.md
  return <Typography sx={{ fontSize: 12, fontWeight: 600, color: md.onSurface, mt: 0.5 }}>{children}</Typography>
}

function Line({ children }: { children: ReactNode }) {
  return <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap', fontSize: 13 }}>{children}</Box>
}

/** A place: the flag, then the name in its own element so the name reads (and
 *  is found) alone. */
function Place({ cc, name }: { cc: string; name: string }) {
  const flag = countryFlag(cc)
  return (
    <Box component="span" sx={{ minWidth: 140, display: 'inline-flex', gap: 0.5 }}>
      {flag && <span>{flag}</span>}
      <span>{name}</span>
    </Box>
  )
}

function SubSpreadPanel({ ev }: { ev: SubSpreadEvidence }) {
  const { t, i18n } = useTranslation(['admin'])
  // Provinces only: a Chinese UI names a CN province by its ISO code, as the
  // Geo tab does. The foreign lines below stay country codes.
  const name = regionNamer(t, i18n.language)
  const labels = dayLabels(ev.window_start, ev.window_days)
  const provinces = ev.provinces ?? []
  const identities = ev.identities ?? []
  const foreign = ev.foreign ?? []
  const x = ev.excluded ?? { shared: 0, listed: 0, infra: 0, internal: 0 }
  const cov = ev.coverage ?? { sources: 0, placed: 0, region_known: 0 }
  return (
    <>
      <Caption>{t('admin:risk_signals.window_hint', {
        start: ev.window_start, days: ev.window_days,
        defaultValue: `每格一天，自 ${ev.window_start} 起共 ${ev.window_days} 天（面板时区）`,
      })}</Caption>
      {provinces.map((p, i) => (
        <Line key={`${p.cc}/${p.region}/${i}`}>
          <Place cc={p.cc} name={placeLabel(p, name)} />
          <DayStrip mask={p.days} labels={labels} />
          {p.established && (
            <Chip size="small" variant="outlined" color="primary"
              label={t('admin:risk_signals.province_recurring', { defaultValue: '常驻' })} />
          )}
          <Chip size="small" variant="outlined"
            label={t('admin:risk_signals.province_group', { n: p.group, defaultValue: `第 ${p.group} 组` })} />
        </Line>
      ))}
      {identities.length > 0 && (
        <Subtitle>{t('admin:risk_signals.identities_title', { defaultValue: '拉取订阅的客户端' })}</Subtitle>
      )}
      {identities.map((id, i) => {
        const places = (id.provinces ?? []).map(j => provinces[j]).filter(Boolean).map(p => placeLabel(p, name)).join(' · ')
        return (
          <Line key={`${id.kind}/${id.label}/${i}`}>
            <Chip size="small" variant="outlined" label={id.kind === 'hwid'
              ? t('admin:risk_signals.identity_hwid', { defaultValue: '设备' })
              : t('admin:risk_signals.identity_ua', { defaultValue: '客户端标识' })} />
            <Box component="span" sx={{ minWidth: 140 }}>{id.label || (id.hwid4 ? `#${id.hwid4}` : '—')}</Box>
            <DayStrip mask={id.days} labels={labels} />
            {places && (
              <Caption>{t('admin:risk_signals.identity_seen_at', { places, defaultValue: `去过：${places}` })}</Caption>
            )}
          </Line>
        )
      })}
      {/* Context only: sub_spread judges one country, and a landing egress
          abroad is the commonest reason a fetch shows up elsewhere. */}
      {foreign.length > 0 && (
        <Subtitle>{t('admin:risk_signals.foreign_title', { defaultValue: '其他国家的来源（仅供参考，不参与判断）' })}</Subtitle>
      )}
      {foreign.map(f => (
        <Line key={f.cc}>
          <Place cc={f.cc} name={f.cc} />
          <DayStrip mask={f.days} labels={labels} />
        </Line>
      ))}
      <Caption>{t('admin:risk_signals.coverage', {
        sources: cov.sources, placed: cov.placed, region_known: cov.region_known,
        shared: x.shared, listed: x.listed, infra: x.infra, internal: x.internal,
        defaultValue: `来源 ${cov.sources} 个，可定位 ${cov.placed} 个，定位到省 ${cov.region_known} 个；已排除：共享出口 ${x.shared}、忽略名单 ${x.listed}、本机节点 / 中转 ${x.infra}、内网 ${x.internal}`,
      })}</Caption>
    </>
  )
}

function DevicesPanel({ ev }: { ev: DevicesEvidence }) {
  const { t } = useTranslation(['admin'])
  const labels = dayLabels(ev.window_start, ev.window_days)
  const devices = ev.devices ?? []
  const clients = ev.clients ?? []
  return (
    <>
      {devices.length > 0 && (
        <Subtitle>{t('admin:risk_signals.devices_title', { defaultValue: '声明的设备' })}</Subtitle>
      )}
      {/* Only the digest's 4-character prefix is ever stored in evidence:
          enough to tell this account's devices apart, nothing more. */}
      {devices.map((d, i) => (
        <Line key={`${d.hwid4}/${i}`}>
          <Box component="span" sx={{ minWidth: 140 }}>{d.label || '—'}</Box>
          <Box component="span" sx={{ fontFamily: 'monospace', fontSize: 12 }}>{`#${d.hwid4}`}</Box>
          <DayStrip mask={d.days} labels={labels} />
          {d.recurrent && (
            <Chip size="small" variant="outlined" color="primary"
              label={t('admin:risk_signals.device_recurring', { defaultValue: '常用' })} />
          )}
          <Caption>{t('admin:risk_signals.device_last_seen', {
            time: when(d.last_ms), defaultValue: `最后一次 ${when(d.last_ms)}`,
          })}</Caption>
          {d.client && <Caption>{d.client}</Caption>}
        </Line>
      ))}
      <Caption>{t('admin:risk_signals.device_fetches', {
        with: ev.fetches_with_hwid, without: ev.fetches_without,
        defaultValue: `带设备标识的拉取 ${ev.fetches_with_hwid} 次，不带的 ${ev.fetches_without} 次`,
      })}</Caption>
      {clients.length > 0 && (
        <Subtitle>{t('admin:risk_signals.clients_title', { defaultValue: '未带设备标识的客户端' })}</Subtitle>
      )}
      {clients.map((c, i) => (
        <Line key={`${c.label}/${i}`}>
          <Box component="span" sx={{ minWidth: 140 }}>{c.label || '—'}</Box>
          <DayStrip mask={c.days} labels={labels} />
        </Line>
      ))}
    </>
  )
}

function UsagePanel({ ev }: { ev: UsageShiftEvidence }) {
  const { t } = useTranslation(['admin'])
  // The numbers exist only once the judged days were judged; a warm-up or a
  // short history carries the series alone, and a caption of zeros would
  // read as a judgement.
  const judged = (ev.thresholds ?? []).length > 0
  // Both lengths are settings, so the title counts the bars it draws and
  // the caption the days the verdict judged. A row stored before they were
  // settings carries no recent_days and was judged over the shipped seven.
  const days = (ev.series ?? []).length
  const recent = ev.recent_days ?? 7
  return (
    <>
      <Caption>{t('admin:risk_signals.usage_title', {
        days, end: ev.end_date, defaultValue: `最近 ${days} 天每日用量（截至 ${ev.end_date}）`,
      })}</Caption>
      <UsageBars ev={ev} />
      {judged && (
        <Caption>{t('admin:risk_signals.usage_caption', {
          median: formatGB(ev.median), ratio: ev.ratio, floor: formatGB(ev.floor), over: ev.over_days, recent,
          defaultValue: `基线中位数 ${formatGB(ev.median)}，倍数 ${ev.ratio}，每日下限 ${formatGB(ev.floor)}；最近 ${recent} 天超标 ${ev.over_days} 天`,
        })}</Caption>
      )}
    </>
  )
}

/**
 * The series as bars, oldest first: the baseline days, then the judged ones.
 * Each judged day carries a tick at the threshold it was held to, and its bar
 * is drawn in the error colour when it went over — the thresholds already
 * include the fleet factor and the floor, so the tick is the line the day
 * actually crossed.
 */
function UsageBars({ ev }: { ev: UsageShiftEvidence }) {
  const md = useTheme().palette.md
  const series = ev.series ?? []
  const thresholds = ev.thresholds ?? []
  const over = ev.over ?? []
  const n = series.length
  const firstJudged = n - thresholds.length
  const top = Math.max(1, ...series, ...thresholds)
  const H = 60
  const step = 8
  const endMs = Date.parse(`${ev.end_date}T00:00:00Z`)
  const start = Number.isNaN(endMs) ? '' : new Date(endMs - (n - 1) * 86_400_000).toISOString().slice(0, 10)
  const labels = dayLabels(start, n)
  const y = (b: number) => H - (b / top) * H
  return (
    <Box component="svg" viewBox={`0 0 ${Math.max(n * step, 1)} ${H}`} role="img"
      sx={{ width: '100%', maxWidth: 420, height: H, display: 'block' }}>
      {series.map((b, i) => {
        const j = i - firstJudged
        return (
          <rect key={i} x={i * step} y={y(b)} width={step - 2} height={H - y(b)}
            fill={j >= 0 && over[j] ? md.error : md.primary} opacity={j >= 0 ? 1 : 0.55}>
            <title>{`${labels[i] ? `${labels[i]}: ` : ''}${formatGB(b)}`}</title>
          </rect>
        )
      })}
      {thresholds.map((th, j) => {
        const x = (firstJudged + j) * step
        return <line key={j} x1={x - 1} x2={x + step - 1} y1={y(th)} y2={y(th)} stroke={md.onSurface} strokeWidth={1.5} />
      })}
    </Box>
  )
}

function LoginPanel({ ev }: { ev: LoginCountryEvidence }) {
  const { t } = useTranslation(['admin'])
  const known = ev.known ?? []
  const events = ev.events ?? []
  const s = ev.skipped ?? { infra: 0, internal: 0, listed: 0, node_country: 0, unplaced: 0 }
  // "Recent" is the hold, a per-group setting: the logins counted as recent
  // are the ones inside the hold this verdict was judged with.
  const hold = ev.hold_days ?? 7
  return (
    <>
      <Line>
        <Caption>{t('admin:risk_signals.login_known', { defaultValue: '已知国家' })}</Caption>
        {known.length
          ? known.map(cc => <Chip key={cc} size="small" variant="outlined" label={[countryFlag(cc), cc].filter(Boolean).join(' ')} />)
          : '—'}
      </Line>
      {events.length > 0 && (
        <Subtitle>{t('admin:risk_signals.login_events', { defaultValue: '新国家登录' })}</Subtitle>
      )}
      {events.map((e, i) => (
        <Line key={`${e.cc}/${e.at_ms}/${i}`}>
          <Place cc={e.cc} name={e.cc} />
          <Caption>{when(e.at_ms)}</Caption>
          <Caption>{e.method}</Caption>
        </Line>
      ))}
      <Caption>{t('admin:risk_signals.login_counts', {
        lookback: ev.lookback_days, logins: ev.logins, recent: ev.recent, judged: ev.judged, hold,
        infra: s.infra, internal: s.internal, listed: s.listed, node_country: s.node_country, unplaced: s.unplaced,
        defaultValue: `${ev.lookback_days} 天内登录 ${ev.logins} 次；最近 ${hold} 天 ${ev.recent} 次，其中已判断 ${ev.judged} 次；跳过：本机节点 ${s.infra}、内网 ${s.internal}、忽略名单 ${s.listed}、节点所在国家 ${s.node_country}、无法定位 ${s.unplaced}`,
      })}</Caption>
    </>
  )
}
