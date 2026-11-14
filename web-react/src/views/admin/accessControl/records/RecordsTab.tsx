import { Fragment, useEffect, useMemo, useState } from 'react'
import {
  Alert, Badge, Box, Button, ButtonBase, Divider, IconButton, LinearProgress, Link, ListItemText, ListSubheader, Menu, MenuItem,
  Popover, Skeleton, Table, TableBody, TableCell, TableContainer, TableHead, TableRow, TextField, ToggleButton, ToggleButtonGroup,
  Tooltip, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import FilterListIcon from '@mui/icons-material/FilterList'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import { useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'

import { destErrorOf, isDestUnavailable, type DestHitItem, type DestHits } from '@/api/accessControl'
import KpiTile, { KpiGrid } from '@/components/KpiTile'
import { PagedTableFooter } from '@/components/PagedTableFooter'
import ToneBadge from '@/components/ToneBadge'
import UserAutocomplete from '@/components/UserAutocomplete'
import { useDestHits, useDestPolicies, useDestSettings, useDestStatus } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { accessTone, collectingNodeCount, enabledPolicies, isNativeKind } from '@/utils/accessControl'
import AddExemptionDialog from '../sheets/AddExemptionDialog'
import type { SettingsFocus } from '../accessParams'
import { destErrorText } from '../destErrors'
import AddExceptionDialog, { type ExceptionTarget } from './AddExceptionDialog'
import { dayKey, dayRelation, formatClock, formatHourRange } from './hitTime'
import {
  parseRecordsParams, recordsApiParams, recordsFilterCount, recordsSearch, REC_DEFAULT_SIZE, REC_DEFAULT_SINCE, REC_MAX_RANGE_DAYS,
  REC_PAGE_SIZES, relativeSince, type RecGroupBy, type RecordsFilter,
} from './recordsParams'

const R = 'admin:access_control.records.'
const HOUR = 3_600_000
const SEARCH_DEBOUNCE_MS = 400

export interface RecordsTabProps {
  /** Opens the account drawer (the page's `user` param). */
  onOpenUser: (userId: number) => void
  /** Opens the test sheet with this destination (carried in history state, never the URL). */
  onTestDestination: (host: string) => void
  onOpenSettings: (focus?: SettingsFocus) => void
  onOpenNodes: () => void
  onGoToPolicies: () => void
}

/** The destination as a hit names it: host and port. */
const destOf = (h: Pick<DestHitItem, 'dest' | 'port'>) => (h.port > 0 ? `${h.dest}:${h.port}` : h.dest)

/**
 * THE RECORDS TAB (S12): the hits, one row per account, destination and hour,
 * answering who, on which node, because of which rule, to what, and how many.
 * Filters, the four views and the page are in the URL (`rec_*`, recordsParams);
 * the search term is not: it is component state and a request parameter only
 * (R12). The tiles count the whole time/account/node selection whatever the
 * action filter says, so pressing one never zeroes the other.
 */
export default function RecordsTab({ onOpenUser, onTestDestination, onOpenSettings, onOpenNodes, onGoToPolicies }: RecordsTabProps) {
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const folded = useMediaQuery(theme.breakpoints.down('md'))
  const phone = useMediaQuery(theme.breakpoints.down('sm'))
  const scope = useQueryScope()
  const tz = useSiteStore(s => s.timezone)
  const [params, setParams] = useSearchParams()
  const settings = useDestSettings(scope)
  const policies = useDestPolicies(scope)
  const status = useDestStatus(scope)
  const retention = settings.data?.effective.dest_hit_retention_days

  // The search term: what the admin typed, and what was last sent (after a pause).
  const [typed, setTyped] = useState('')
  const [q, setQ] = useState('')
  useEffect(() => {
    const id = setTimeout(() => setQ(typed.trim()), SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(id)
  }, [typed])

  // One request per filter set: "now" is read when the filters change, never on a render, or the key would move every time.
  const recKey = [...params].filter(([k]) => k.startsWith('rec_')).map(([k, v]) => `${k}=${v}`).join('&')
  // The hour ticks the request forward: an open page keeps seeing new hour buckets.
  const [hourTick, setHourTick] = useState(() => Math.floor(Date.now() / 3_600_000))
  useEffect(() => {
    const id = setInterval(() => setHourTick(Math.floor(Date.now() / 3_600_000)), 60_000)
    return () => clearInterval(id)
  }, [])
  const { filter, request } = useMemo(() => {
    const f = parseRecordsParams(new URLSearchParams(recKey))
    return { filter: f, request: recordsApiParams(f, { now: Date.now(), retentionDays: retention, q }) }
  }, [recKey, retention, q, hourTick])
  const hits = useDestHits(scope, request)

  const patch = (next: Partial<RecordsFilter>) => setParams(prev => recordsSearch(prev, next), { replace: true })
  const clear = () => { setTyped(''); setQ(''); setParams(prev => recordsSearch(prev, { user_id: undefined, panel_id: undefined, source: undefined, action: undefined }), { replace: true }) }

  const [menu, setMenu] = useState<{ el: HTMLElement; hit: DestHitItem } | null>(null)
  const [exception, setException] = useState<ExceptionTarget | null>(null)
  const [exempting, setExempting] = useState<number | null>(null)
  const [more, setMore] = useState<HTMLElement | null>(null)

  const num = new Intl.NumberFormat(i18n.language)
  const data: DestHits | undefined = hits.data
  // One instant per read: the day headings and "today" are measured from it, not from each render.
  const readAt = hits.dataUpdatedAt
  const now = useMemo(() => Date.now(), [readAt]) // eslint-disable-line react-hooks/exhaustive-deps
  const filterCount = recordsFilterCount(filter) + (q ? 1 : 0)

  // ---- labels ----
  const ruleName = (h: Pick<DestHitItem, 'source' | 'source_name' | 'source_kind'>) => {
    if (h.source_name === null) return null
    return h.source_kind === 'source' ? t(`${R}rule.source_rule`, { name: h.source_name }) : h.source_name
  }
  const deletedRule = (source: string) => t(`${R}rule.${source.startsWith('s') ? 'deleted_source_rule' : 'deleted_policy'}`)
  const userName = (h: Pick<DestHitItem, 'user_id' | 'upn'>) => h.upn ?? t(`${R}rule.deleted_user`, { id: h.user_id })
  const days7 = relativeSince(7, retention)

  // ---- the rule filter's options: policies, source rules and the deleted, from what is there and what the range saw ----
  const ruleOptions = useMemo(() => {
    const byId = new Map<string, string>()
    for (const p of [...(policies.data?.allow ?? []), ...(policies.data?.block ?? [])]) {
      if (p.action === 'block') byId.set(`p${p.id}`, p.name)
    }
    const rules: { source: string; label: string }[] = []
    const deleted: { source: string }[] = []
    for (const s of data?.sources ?? []) {
      if (s.source.startsWith('g')) continue
      if (s.name === null) deleted.push({ source: s.source })
      else if (s.source.startsWith('s')) rules.push({ source: s.source, label: t(`${R}rule.source_rule`, { name: s.name }) })
      else byId.set(s.source, s.name)
    }
    return { policies: [...byId].map(([source, label]) => ({ source, label })), rules, deleted }
  }, [policies.data, data?.sources, t])
  const known = new Set([...ruleOptions.policies, ...ruleOptions.rules, ...ruleOptions.deleted].map(o => o.source))
  const panels = (status.data?.nodes ?? []).filter(n => isNativeKind(n.kind) && n.state !== 'unsupported_kind')

  // ---- state of the page ----
  const deployed = enabledPolicies(policies.data).length
  const recording = collectingNodeCount(status.data)
  const rangeDays = /^(\d+)d$/.exec(filter.since)?.[1]
  const olderThanRetention = !!retention && !!rangeDays && Number(rangeDays) > retention

  const tiles = data && (
    <Box data-testid="hits-tiles">
      <KpiGrid>
        {(['block', 'observe'] as const).map(a => (
          <KpiTile key={a} label={t(`${R}tiles.${a}`)} value={num.format(a === 'block' ? data.summary.block : data.summary.observe)}
            pressed={filter.action === a} onToggle={() => patch({ action: filter.action === a ? undefined : a })} />
        ))}
        <KpiTile label={t(`${R}tiles.users`)} value={num.format(data.summary.users)} />
      </KpiGrid>
    </Box>
  )

  // ---- the controls ----
  const rangeOptions: { value: string; label: string }[] = [
    { value: '1h', label: t(`${R}range.h1`) },
    { value: '24h', label: t(`${R}range.h24`) },
    ...(!retention || retention > 1 ? [{ value: days7, label: t(`${R}range.days`, { days: days7.replace('d', '') }) }] : []),
  ]
  const custom = /^\d{4}-/.test(filter.since)
  // A link may carry another window ("14d"): it is an option for as long as it is the current one.
  if (!custom && !rangeOptions.some(o => o.value === filter.since)) {
    rangeOptions.push({ value: filter.since, label: t(`${R}range.days`, { days: String(rangeDays ?? '') }) })
  }
  const rangeValue = custom ? 'custom' : filter.since
  const setRange = (v: string) => {
    if (v === 'custom') {
      // Start from the span of the current range, in browser time, as datetime-local text.
      const end = Date.now(); const start = end - 24 * HOUR
      const local = (ms: number) => new Date(ms - new Date(ms).getTimezoneOffset() * 60_000).toISOString().slice(0, 16)
      patch({ since: local(start), until: local(end) })
    } else patch({ since: v, until: undefined })
  }

  const accountField = <UserAutocomplete value={filter.user_id ?? null} onChange={id => patch({ user_id: id ?? undefined })}
    label={t(`${R}filters.user`)} width={240} fullWidth={phone} />
  const ruleField = (
    <TextField select size="small" label={t(`${R}filters.source`)} value={filter.source && known.has(filter.source) ? filter.source : filter.source ? filter.source : ''}
      onChange={e => patch({ source: e.target.value || undefined })} sx={{ minWidth: 200 }} fullWidth={phone}
      slotProps={{ inputLabel: { shrink: true }, select: { displayEmpty: true, renderValue: (v: unknown) => {
        if (!v) return t(`${R}filters.source_all`)
        const all = [...ruleOptions.policies, ...ruleOptions.rules]
        return all.find(o => o.source === v)?.label ?? deletedRule(String(v))
      } } }}>
      <MenuItem value="">{t(`${R}filters.source_all`)}</MenuItem>
      {ruleOptions.policies.length > 0 && <ListSubheader>{t(`${R}filters.group_policies`)}</ListSubheader>}
      {ruleOptions.policies.map(o => <MenuItem key={o.source} value={o.source}>{o.label}</MenuItem>)}
      {ruleOptions.rules.length > 0 && <ListSubheader>{t(`${R}filters.group_rules`)}</ListSubheader>}
      {ruleOptions.rules.map(o => <MenuItem key={o.source} value={o.source}>{o.label}</MenuItem>)}
      {ruleOptions.deleted.length > 0 && <ListSubheader>{t(`${R}filters.group_deleted`)}</ListSubheader>}
      {ruleOptions.deleted.map(o => <MenuItem key={o.source} value={o.source}>{deletedRule(o.source)}</MenuItem>)}
    </TextField>
  )
  const panelField = (
    <TextField select size="small" label={t(`${R}filters.panel`)} value={filter.panel_id ?? ''} onChange={e => patch({ panel_id: Number(e.target.value) || undefined })}
      sx={{ minWidth: 160 }} fullWidth={phone} slotProps={{ inputLabel: { shrink: true }, select: { displayEmpty: true, renderValue: (v: unknown) => (!v ? t(`${R}filters.panel_all`) : panels.find(n => n.panel_id === v)?.panel_name ?? `#${String(v)}`) } }}>
      <MenuItem value="">{t(`${R}filters.panel_all`)}</MenuItem>
      {panels.map(n => <MenuItem key={n.panel_id} value={n.panel_id}>{n.panel_name}</MenuItem>)}
    </TextField>
  )
  const rangeField = (
    <TextField select size="small" label={t(`${R}filters.range`)} value={rangeValue} onChange={e => setRange(e.target.value)}
      sx={{ minWidth: 160 }} fullWidth={phone}>
      {rangeOptions.map(o => <MenuItem key={o.value} value={o.value}>{o.label}</MenuItem>)}
      <MenuItem value="custom">{t(`${R}range.custom`)}</MenuItem>
    </TextField>
  )
  const searchField = (
    <TextField size="small" type="search" label={t(`${R}filters.search`)} value={typed} onChange={e => setTyped(e.target.value)}
      sx={{ minWidth: 220 }} fullWidth={phone} slotProps={{ htmlInput: { autoComplete: 'off', spellCheck: false } }} />
  )

  const views: RecGroupBy[] = ['none', 'site', 'user', 'policy']

  // ---- the list ----
  const grouped = filter.group_by !== 'none'
  const refusalCode = hits.isError ? destErrorOf(hits.error)?.code : undefined
  const refusal = refusalCode === 'dest_hits_range' || refusalCode === 'dest_hits_invalid'
  let body: React.ReactNode
  if (hits.isPending) {
    body = <Box sx={{ display: 'grid', gap: 1 }}>{[0, 1, 2, 3, 4].map(i => <Skeleton key={i} variant="rounded" height={48} />)}</Box>
  } else if (hits.isError && (!data || refusal)) {
    // A refused range or filter is the admin's to fix, and the page kept from the last read would hide it: say it either way.
    body = isDestUnavailable(hits.error)
      ? <Alert severity="info">{t('admin:access_control.unwired')}</Alert>
      : refusal
        ? <Alert severity="error">{destErrorText(t, hits.error)}</Alert>
        : <Alert severity="error" action={<Button color="inherit" size="small" onClick={() => void hits.refetch()}>{t('admin:access_control.retry')}</Button>}>{t(`${R}read_failed`)}</Alert>
  } else if (data && data.items.length === 0) {
    const empty = (text: string, action?: React.ReactNode) => (
      <Box sx={{ p: 2, borderRadius: 2, bgcolor: md.surfaceContainerHigh, display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
        <Typography variant="body2" sx={{ flex: 1, minWidth: 0 }}>{text}</Typography>
        {action}
      </Box>
    )
    if (deployed === 0 && filterCount === 0) body = empty(t(`${R}empty.no_policies`), <Button size="small" onClick={onGoToPolicies}>{t(`${R}empty.to_policies`)}</Button>)
    else if (recording === 0 && status.data) body = empty(t(`${R}empty.no_collect`), <Button size="small" onClick={onOpenNodes}>{t(`${R}empty.nodes`)}</Button>)
    else if (filterCount > 0) body = empty(t(`${R}empty.filtered`), <Button size="small" onClick={clear}>{t(`${R}empty.clear`)}</Button>)
    else if (olderThanRetention) body = empty(t(`${R}empty.older`, { days: retention }))
    else body = empty(t(`${R}empty.none`))
  } else if (data) {
    body = grouped
      ? <GroupList data={data} view={filter.group_by} folded={folded} num={num} userName={userName} ruleName={ruleName} onOpenUser={onOpenUser}
        onPickSource={s => patch({ source: s })} />
      : <HitList data={data} folded={folded} tz={tz} now={now} num={num} userName={userName} ruleName={ruleName}
        onOpenUser={onOpenUser} onPickSource={s => patch({ source: s })} onMenu={(el, hit) => setMenu({ el, hit })} />
  }

  const hideTiles = !data || (data.items.length === 0 && (deployed === 0 || (recording === 0 && !!status.data)))
  return (
    <Box>
      {hits.isPending ? <Skeleton variant="rounded" height={72} sx={{ mb: 1.5 }} /> : !hideTiles && tiles}

      <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 1, mb: 1 }}>
        <Box sx={{ flex: phone ? '1 1 100%' : '0 0 auto' }}>{accountField}</Box>
        <Box sx={{ flex: phone ? '1 1 100%' : '0 0 auto' }}>{rangeField}</Box>
        {phone
          ? (
            <>
              <Badge color="primary" badgeContent={filterCount} invisible={filterCount === 0}>
                <Button size="small" variant="outlined" startIcon={<FilterListIcon />} onClick={e => setMore(e.currentTarget)}>{t(`${R}filters.more`)}</Button>
              </Badge>
              <Popover open={more !== null} anchorEl={more} onClose={() => setMore(null)} slotProps={{ paper: { sx: { p: 2, display: 'grid', gap: 1.5, width: 300, maxWidth: '90vw' } } }}>
                {ruleField}{panelField}{searchField}
              </Popover>
            </>
          )
          : <>{ruleField}{panelField}{searchField}</>}
      </Box>
      {custom && (
        <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1, mb: 1, alignItems: 'center' }}>
          <TextField size="small" type="datetime-local" label={t(`${R}range.since`)} value={filter.since.slice(0, 16)}
            onChange={e => patch({ since: e.target.value || REC_DEFAULT_SINCE, until: filter.until })} slotProps={{ inputLabel: { shrink: true } }} />
          <TextField size="small" type="datetime-local" label={t(`${R}range.until`)} value={(filter.until ?? '').slice(0, 16)}
            onChange={e => patch({ since: filter.since, until: e.target.value || undefined })} slotProps={{ inputLabel: { shrink: true } }} />
          <Typography variant="caption" sx={{ color: md.onSurfaceVariant }}>{t(`${R}range.custom_note`, { days: REC_MAX_RANGE_DAYS })}</Typography>
        </Box>
      )}

      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 1, mb: 1 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
          <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${R}view.label`)}</Typography>
          <ToggleButtonGroup exclusive size="small" value={filter.group_by} aria-label={t(`${R}view.label`)} onChange={(_, v: RecGroupBy | null) => { if (v) patch({ group_by: v }) }}>
            {views.map(v => <ToggleButton key={v} value={v}>{t(`${R}view.${v}`)}</ToggleButton>)}
          </ToggleButtonGroup>
        </Box>
        {data && data.items.length > 0 && (
          <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>
            {grouped ? t(`${R}count.grouped`, { n: num.format(data.total) }) : t(`${R}count.detail`, { n: num.format(data.total) })}
          </Typography>
        )}
      </Box>
      {data && data.dropped_in_range > 0 && (
        <Box data-testid="hits-dropped" sx={{ display: 'flex', alignItems: 'center', gap: 0.75, mb: 1 }}>
          <WarningAmberIcon aria-hidden sx={{ fontSize: 16, color: theme.palette.mode === 'dark' ? theme.palette.warning.light : theme.palette.warning.dark }} />
          <Typography variant="body2" sx={{ color: md.onSurface }}>{t(`${R}dropped`, { n: num.format(data.dropped_in_range) })}</Typography>
        </Box>
      )}

      {/* A fixed slot, so a re-read does not move the table. */}
      <Box sx={{ height: 4, mb: 0.5 }}>{hits.isFetching && !hits.isPending && <LinearProgress />}</Box>
      {body}

      {data && data.items.length > 0 && (
        <PagedTableFooter total={data.total} page={filter.page} pageSize={filter.page_size} rowsPerPageOptions={[...REC_PAGE_SIZES]}
          onPageChange={n => patch({ page: n })} onPageSizeChange={n => patch({ page_size: n === REC_DEFAULT_SIZE ? REC_DEFAULT_SIZE : n })} />
      )}
      <Typography variant="body2" sx={{ mt: 1, color: md.onSurfaceVariant }}>
        {retention ? t(`${R}retention`, { days: retention }) : ''}{' '}
        <Link component="button" type="button" underline="always" onClick={() => onOpenSettings('retention')} sx={{ font: 'inherit', verticalAlign: 'baseline' }}>
          {t(`${R}change`)}
        </Link>
      </Typography>

      <Menu anchorEl={menu?.el} open={menu !== null} onClose={() => setMenu(null)}>
        <MenuItem onClick={() => { const h = menu!.hit; setMenu(null); onTestDestination(h.dest) }}><ListItemText>{t(`${R}menu.test`)}</ListItemText></MenuItem>
        <MenuItem onClick={() => { const h = menu!.hit; setMenu(null); setException({ host: h.dest, user: h.upn ? { id: h.user_id, upn: h.upn } : null }) }}>
          <ListItemText>{t(`${R}menu.exception`)}</ListItemText>
        </MenuItem>
        <Divider />
        <MenuItem disabled={!menu?.hit.upn} onClick={() => { const h = menu!.hit; setMenu(null); onOpenUser(h.user_id) }}><ListItemText>{t(`${R}menu.open_user`)}</ListItemText></MenuItem>
      </Menu>
      <AddExceptionDialog target={exception} onClose={() => setException(null)} onExempt={id => setExempting(id)} />
      <AddExemptionDialog open={exempting !== null} onClose={() => setExempting(null)} lockedUserId={exempting ?? undefined} />
    </Box>
  )
}

type Namers = {
  num: Intl.NumberFormat
  userName: (h: Pick<DestHitItem, 'user_id' | 'upn'>) => string
  ruleName: (h: Pick<DestHitItem, 'source' | 'source_name' | 'source_kind'>) => string | null
  onOpenUser: (id: number) => void
  onPickSource: (source: string) => void
}

/** The action a hit records, in the closed table's words: a block, or what a report-only rule would have blocked. */
function ActionBadge({ action }: { action: DestHitItem['action'] }) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  return action === 'observe'
    ? <ToneBadge tone={accessTone(theme, 'would_deny')} label={t(`${R}rule.would_block`)} testId="hit-action" data="observe" />
    : <ToneBadge tone={accessTone(theme, 'block')} label={t(`${R}rule.blocked`)} testId="hit-action" data="block" />
}

function RuleLink({ hit, ruleName, onPickSource }: { hit: DestHitItem } & Pick<Namers, 'ruleName' | 'onPickSource'>) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const name = ruleName(hit)
  if (name === null) return <Typography component="span" sx={{ fontSize: 13, fontStyle: 'italic', color: md.onSurfaceVariant }}>{t(`${R}rule.${hit.source.startsWith('s') ? 'deleted_source_rule' : 'deleted_policy'}`)}</Typography>
  return (
    <ButtonBase onClick={() => onPickSource(hit.source)} aria-label={t(`${R}rule.filter_by`, { name })}
      sx={{ fontSize: 13, textAlign: 'start', borderRadius: 1, overflowWrap: 'anywhere', '&:hover': { textDecoration: 'underline' }, '&:focus-visible': { outline: `2px solid ${md.primary}`, outlineOffset: 2 } }}>
      <span>{name}</span>
    </ButtonBase>
  )
}

function UserCell({ hit, userName, onOpenUser }: { hit: Pick<DestHitItem, 'user_id' | 'upn'> } & Pick<Namers, 'userName' | 'onOpenUser'>) {
  const md = useTheme().palette.md
  if (!hit.upn) return <Typography component="span" sx={{ fontSize: 13, fontStyle: 'italic', color: md.onSurfaceVariant }}>{userName(hit)}</Typography>
  return (
    <ButtonBase onClick={() => onOpenUser(hit.user_id)} aria-label={hit.upn}
      sx={{ fontSize: 13, textAlign: 'start', borderRadius: 1, overflowWrap: 'anywhere', '&:hover': { textDecoration: 'underline' }, '&:focus-visible': { outline: `2px solid ${md.primary}`, outlineOffset: 2 } }}>
      <span>{hit.upn}</span>
    </ButtonBase>
  )
}

function Dest({ hit }: { hit: DestHitItem }) {
  const md = useTheme().palette.md
  const text = destOf(hit)
  return (
    <Tooltip title={text}>
      <Typography data-testid="hit-dest" component="span" noWrap sx={{ display: 'block', fontFamily: 'ui-monospace, monospace', fontSize: 13, color: md.onSurface, minWidth: 0 }}>{text}</Typography>
    </Tooltip>
  )
}

function HitList({ data, folded, tz, now, num, userName, ruleName, onOpenUser, onPickSource, onMenu }: Namers & {
  data: DestHits
  folded: boolean
  tz: string
  now: number
  onMenu: (el: HTMLElement, hit: DestHitItem) => void
}) {
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  // Consecutive rows of one panel-zone day sit under one heading.
  const groups = useMemo(() => {
    const out: { day: string; ms: number; rows: DestHitItem[] }[] = []
    for (const h of data.items) {
      const day = dayKey(h.hour_ms, tz)
      const last = out.at(-1)
      if (last && last.day === day) last.rows.push(h)
      else out.push({ day, ms: h.hour_ms, rows: [h] })
    }
    return out
  }, [data.items, tz])
  const dayLabel = (ms: number) => {
    const rel = dayRelation(ms, now, tz)
    if (rel !== 'date') return t(`${R}day.${rel}`)
    try {
      return new Intl.DateTimeFormat(i18n.language, { timeZone: tz || undefined, year: 'numeric', month: 'long', day: 'numeric' }).format(new Date(ms))
    } catch {
      return dayKey(ms, tz)
    }
  }
  const hover = (h: DestHitItem) => t(`${R}hover.first_last`, { first: formatClock(h.first_ms, tz), last: formatClock(h.last_ms, tz) })
  const menuButton = (h: DestHitItem) => (
    <IconButton size="small" aria-label={t(`${R}menu.label`)} aria-haspopup="menu" onClick={e => onMenu(e.currentTarget, h)} sx={{ width: 44, height: 44 }}>
      <MoreVertIcon />
    </IconButton>
  )
  const hidden = { position: 'absolute', width: 1, height: 1, overflow: 'hidden', clip: 'rect(0 0 0 0)', whiteSpace: 'nowrap' } as const

  if (folded) {
    return (
      <Box sx={{ display: 'grid', gap: 1 }}>
        {groups.map(g => (
          <Fragment key={g.day + g.ms}>
            <Typography component="h3" sx={{ fontSize: 13, fontWeight: 600, color: md.onSurfaceVariant, position: 'sticky', top: 0, bgcolor: md.surface, py: 0.5 }}>{dayLabel(g.ms)}</Typography>
            {g.rows.map((h, i) => (
              <Box key={i} data-testid="hit-row" sx={{ p: 1.5, borderRadius: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}`, minWidth: 0 }}>
                <Box data-line sx={{ display: 'flex', alignItems: 'center', gap: 1, minWidth: 0 }}>
                  <Box sx={{ flex: 1, minWidth: 0 }}><Box component="span" sx={hidden}>{t(`${R}cols.user`)}</Box><UserCell hit={h} userName={userName} onOpenUser={onOpenUser} /></Box>
                  <Typography component="span" sx={{ fontVariantNumeric: 'tabular-nums', fontWeight: 600 }}><Box component="span" sx={hidden}>{t(`${R}cols.count`)}</Box>{num.format(h.count)}</Typography>
                  {menuButton(h)}
                </Box>
                <Box data-line sx={{ display: 'flex', alignItems: 'center', gap: 1, minWidth: 0, flexWrap: 'wrap' }}>
                  <ActionBadge action={h.action} />
                  <RuleLink hit={h} ruleName={ruleName} onPickSource={onPickSource} />
                  <Box sx={{ flex: '1 1 160px', minWidth: 0 }}><Dest hit={h} /></Box>
                </Box>
                <Typography data-line variant="body2" data-testid="hit-time" title={hover(h)} sx={{ color: md.onSurfaceVariant }}>
                  {formatHourRange(h.hour_ms, tz)} · {h.panel_name ?? '—'}
                </Typography>
              </Box>
            ))}
          </Fragment>
        ))}
      </Box>
    )
  }

  return (
    <TableContainer>
      <Table size="small" aria-label={t(`${R}title`)} sx={{ tableLayout: 'fixed', minWidth: 720 }}>
        <TableHead>
          <TableRow>
            {(['hour', 'user', 'rule', 'dest', 'panel'] as const).map(c => (
              <TableCell key={c} sx={{ width: { hour: 120, user: 170, rule: 230, dest: 'auto', panel: 110 }[c] }}>{t(`${R}cols.${c}`)}</TableCell>
            ))}
            <TableCell align="right" sx={{ width: 80 }}>{t(`${R}cols.count`)}</TableCell>
            <TableCell sx={{ width: 52 }}><Box component="span" sx={hidden}>{t(`${R}menu.label`)}</Box></TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {groups.map(g => (
            <Fragment key={g.day + g.ms}>
              <TableRow>
                <TableCell colSpan={7} sx={{ position: 'sticky', top: 0, bgcolor: md.surface, py: 0.5 }}>
                  <Typography component="h3" sx={{ fontSize: 13, fontWeight: 600, color: md.onSurfaceVariant }}>{dayLabel(g.ms)}</Typography>
                </TableCell>
              </TableRow>
              {g.rows.map((h, i) => (
                <TableRow key={i} data-testid="hit-row" hover>
                  <TableCell sx={{ fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' }}>
                    <span data-testid="hit-time" title={hover(h)}>{formatHourRange(h.hour_ms, tz)}</span>
                  </TableCell>
                  <TableCell sx={{ overflowWrap: 'anywhere' }}><UserCell hit={h} userName={userName} onOpenUser={onOpenUser} /></TableCell>
                  <TableCell>
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75, flexWrap: 'wrap' }}>
                      <ActionBadge action={h.action} />
                      <RuleLink hit={h} ruleName={ruleName} onPickSource={onPickSource} />
                    </Box>
                  </TableCell>
                  <TableCell sx={{ minWidth: 0 }}><Dest hit={h} /></TableCell>
                  <TableCell sx={{ overflowWrap: 'anywhere' }}>{h.panel_name ?? '—'}</TableCell>
                  <TableCell align="right" sx={{ fontVariantNumeric: 'tabular-nums' }}>{num.format(h.count)}</TableCell>
                  <TableCell padding="none">{menuButton(h)}</TableCell>
                </TableRow>
              ))}
            </Fragment>
          ))}
        </TableBody>
      </Table>
    </TableContainer>
  )
}

/**
 * The folded views: one row per destination, account or policy with its count
 * as a bar (the longest is the whole width), and its last hit. The API gives
 * the sum and the span of the fold, not how many accounts or policies are
 * behind it, so there is no such column.
 */
function GroupList({ data, view, folded, num, userName, ruleName, onOpenUser, onPickSource }: Namers & { data: DestHits; view: RecGroupBy; folded: boolean }) {
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const tz = useSiteStore(s => s.timezone)
  const max = Math.max(1, ...data.items.map(i => i.count))
  const last = (ms: number) => {
    try {
      return new Intl.DateTimeFormat(i18n.language, { timeZone: tz || undefined, month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(new Date(ms))
    } catch { return '' }
  }
  const objectCell = (h: DestHitItem) => {
    if (view === 'site') return <Typography component="span" noWrap sx={{ fontFamily: 'ui-monospace, monospace', fontSize: 13 }}>{h.dest}</Typography>
    if (view === 'user') return <UserCell hit={h} userName={userName} onOpenUser={onOpenUser} />
    return <RuleLink hit={h} ruleName={ruleName} onPickSource={onPickSource} />
  }
  return (
    <Box role="table" aria-label={t(`${R}title`)} sx={{ display: 'grid' }}>
      {!folded && (
        <Box role="row" sx={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1.4fr) minmax(0, 2fr) 120px', gap: 1.5, px: 1.5, py: 1, borderBottom: `1px solid ${md.outlineVariant}`, color: md.onSurfaceVariant, fontSize: 13 }}>
          <span role="columnheader">{t(`${R}cols.object`)}</span><span role="columnheader">{t(`${R}cols.count`)}</span><span role="columnheader">{t(`${R}cols.last`)}</span>
        </Box>
      )}
      {data.items.map((h, i) => (
        <Box key={i} role="row" data-testid="hit-group-row" sx={{
          display: 'grid', gridTemplateColumns: folded ? 'minmax(0, 1fr)' : 'minmax(0, 1.4fr) minmax(0, 2fr) 120px', gap: folded ? 0.5 : 1.5, alignItems: 'center',
          px: 1.5, py: 1, borderBottom: `1px solid ${md.outlineVariant}`, minWidth: 0,
        }}>
          <Box role="cell" sx={{ display: 'flex', alignItems: 'center', gap: 0.75, minWidth: 0 }}>
            {objectCell(h)}
            <ActionBadge action={h.action} />
          </Box>
          <Box role="cell" sx={{ display: 'flex', alignItems: 'center', gap: 1, minWidth: 0 }}>
            <Box data-testid="hit-bar" aria-hidden sx={{ height: 8, borderRadius: 4, bgcolor: md.primary, width: `${Math.max(2, (h.count / max) * 60)}%`, flex: '0 0 auto' }} />
            <Typography component="span" sx={{ fontVariantNumeric: 'tabular-nums', fontSize: 13, fontWeight: 600 }}>{num.format(h.count)}</Typography>
          </Box>
          <Typography role="cell" variant="body2" sx={{ color: md.onSurfaceVariant }}>{last(h.last_ms)}</Typography>
        </Box>
      ))}
    </Box>
  )
}
