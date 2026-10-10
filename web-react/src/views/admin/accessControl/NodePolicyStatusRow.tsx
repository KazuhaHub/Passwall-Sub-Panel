import { useState } from 'react'
import { Alert, Box, Button, ButtonBase, IconButton, Menu, MenuItem, Stack, Typography, useTheme } from '@mui/material'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import PauseCircleOutlineIcon from '@mui/icons-material/PauseCircleOutlined'
import RemoveCircleOutlineIcon from '@mui/icons-material/RemoveCircleOutlineOutlined'
import { Link as RouterLink } from 'react-router'
import type { DestinationNodeStatus } from '@/api/accessControl'
import { ToneBadge, stateTone } from '@/components/ToneBadge'
import { AsyncButton } from '@/components/AsyncButton'
import KpiTile, { KpiGrid } from '@/components/KpiTile'
import { fallbackState, nodeAccessTone, nodeFilter } from '@/utils/accessControl'
import { useCan } from '@/utils/permissions'
import { useQueryScope } from '@/query/useQueryScope'
import { scopeKey } from '@/query/session'
import AuditCollectDialog from './AuditCollectDialog'
import { useAccessTranslation } from './useAccessTranslation'

const P = 'admin:access_control.coverage.'
const quotas = new Set(['rules', 'domains', 'regexps', 'cidrs', 'subjects', 'bytes'])
export default function NodePolicyStatusRow({ node, now, variant = 'block', onLists, onRetry, retryPending = false, onOpen, auditCards = false }: {
  node: DestinationNodeStatus; now: number; variant?: 'row' | 'block'
  onLists?: () => void; onRetry?: (agentId: string) => Promise<void>; retryPending?: boolean
  onOpen?: () => void
  auditCards?: boolean
}) {
  const { t, dateTime, number } = useAccessTranslation(['admin', 'common'])
  const theme = useTheme()
  const [menu, setMenu] = useState<HTMLElement | null>(null)
  const [collectOwner, setCollectOwner] = useState<string | null>(null)
  const owner = scopeKey(useQueryScope())
  const canWrite = useCan('config.write')
  const colors = stateTone(theme, nodeAccessTone(node, now))
  const tone = node.state === 'paused' ? { ...colors, Icon: PauseCircleOutlineIcon }
    : node.state === 'unsupported_kind' ? { ...colors, Icon: RemoveCircleOutlineIcon } : colors
  const fallback = fallbackState(node)
  const prolonged = node.state === 'pending' && nodeFilter(node, now) === 'problem'
  const label = prolonged ? t(`${P}state_pending_long`, { minutes: Math.floor((now - node.pending_since!) / 60000) })
    : node.state === 'applied' && node.engine === 'sing-box' ? t(`${P}state_applied_execute_only`) : t(`${P}state_${node.state}`)
  const serverLink = `/admin/servers?${new URLSearchParams({ q: node.panel_name })}`
  const issueLink = node.agent_id ? `/admin/node-issues?${new URLSearchParams({ agent: node.agent_id })}` : null
  const needsIssues = prolonged || node.state === 'rejected' || fallback === 'exhausted' || fallback === 'stopping'
  const confirmed = node.minted_at !== null && node.applied_at !== null && node.pending_since === null
  const rules = fallback === 'exhausted' ? 0 : confirmed && (node.minted_kind === 'desired' || node.minted_kind === 'fallback') ? node.applied_rules : null
  const auditAvailable = node.kind === 'psp' && node.engine === 'xray' && node.supports.hits
  const collection = node.collect === 'off' ? 'off' : !node.collecting ? 'unconfirmed'
    : node.collect_effective === 'hits_and_usage' ? 'usage' : 'hits'
  const loss = node.losses
  const linkStyle = { minHeight: 44 }
  if (variant === 'row') return <ButtonBase onClick={onOpen} sx={{ minHeight: 44, minWidth: 44, px: 0.5, gap: 0.5,
    justifyContent: 'flex-start', textAlign: 'left', whiteSpace: 'normal', color: tone.fg, borderRadius: 1,
    '&.Mui-focusVisible': { outline: '2px solid', outlineColor: theme.palette.md.primary, outlineOffset: 2 } }}>
    <tone.Icon sx={{ fontSize: 12, flexShrink: 0, color: tone.iconColor ?? tone.fg }} />
    <Typography component="span" sx={{ fontSize: 12, overflowWrap: 'anywhere' }}>{t('admin:access_control.server.line', { state: label })}</Typography>
  </ButtonBase>
  return <Box data-node-panel={node.panel_id} sx={{ ...(variant === 'block' ? { border: `1px solid ${theme.palette.md.outlineVariant}`, p: 2, borderRadius: 2 } : {}), minWidth: 0, overflowWrap: 'anywhere' }}>
    <Stack direction="row" sx={{ flexWrap: 'wrap', alignItems: 'center', gap: 1 }}>
      {variant === 'block' && <Typography sx={{ flex: 1, minWidth: 0, fontWeight: 600 }}>{node.panel_name}</Typography>}
      <ToneBadge tone={tone} label={label} />
      <IconButton aria-label={t(`${P}more`, { name: node.panel_name })} onClick={event => setMenu(event.currentTarget)} sx={{ width: 44, height: 44 }}><MoreVertIcon /></IconButton>
    </Stack>
    {variant === 'block' && <Typography variant="caption" color="text.secondary">{t(`${P}kind_${['psp', '3xui', 'sui'].includes(node.kind) ? node.kind : 'other'}`)} · {node.engine ?? '—'} · {node.agent_version ?? '—'}</Typography>}
    {node.state !== 'none' && <Typography variant="body2" color="text.secondary">{t(`${P}description_${prolonged ? 'pending_long' : node.state}`)}</Typography>}
    {auditAvailable && (auditCards ? <KpiGrid>
      <KpiTile label={t('admin:access_control.collect.mode')} value={t(`${P}collection_${collection}`)}
        caption={canWrite ? <Button onClick={() => setCollectOwner(owner)} sx={linkStyle}>{t('admin:access_control.collect.modify')}</Button> : undefined} />
      <KpiTile label={t('admin:access_control.collect.hits_24h')} value={node.hits_24h === null ? '—' : number(node.hits_24h)}
        caption={<Button component={RouterLink} to={`/admin/access-control?tab=records&rec_panel=${node.panel_id}`} sx={linkStyle}>{t('admin:access_control.collect.view_records')}</Button>} />
    </KpiGrid> : <>
      <Typography variant="body2" color="text.secondary" data-testid={`coverage-collection-${node.panel_id}`}>{t(`${P}collection_${collection}`)}</Typography>
      {node.hits_24h !== null && <Typography variant="body2" data-testid={`coverage-hits-${node.panel_id}`}>{t(`${P}hits_window`, { hours: 24, n: number(node.hits_24h) })}</Typography>}
    </>)}
    {auditAvailable && loss && (loss.rows > 0 || loss.events > 0 || loss.unmatched > 0) && <Alert severity="warning" sx={{ mt: 1 }} data-testid={`coverage-losses-${node.panel_id}`}>
      <Typography variant="body2">{t(`${P}loss_incomplete`)}</Typography>
      {loss.rows > 0 && <Typography variant="body2">{t(`${P}loss_rows`, { n: number(loss.rows) })}</Typography>}
      {loss.events > 0 && <Typography variant="body2">{t(`${P}loss_events`, { n: number(loss.events) })}</Typography>}
      {loss.unmatched > 0 && <Typography variant="body2">{t(`${P}loss_unmatched`, { n: number(loss.unmatched) })}</Typography>}
    </Alert>}
    {node.last_report_at !== null && <Typography variant="body2" color="text.secondary">{t(`${P}reported_at`, { time: dateTime(node.last_report_at) })}</Typography>}
    {variant === 'block' && node.applied_at !== null && <Typography variant="body2">{t(`${P}applied_at`, { time: dateTime(node.applied_at) })}</Typography>}
    {node.pending_since !== null && <Typography variant="body2">{t(`${P}pending_since`, { time: dateTime(node.pending_since) })}</Typography>}
    {rules !== null && <Typography variant="body2" data-testid={`coverage-rules-${node.panel_id}`}>{t(`${P}executing_rules`)} {number(rules)}</Typography>}
    {node.over_limit && <Typography color="error">{t(`${P}over_limit`, { kind: quotas.has(node.over_limit.kind) ? t(`admin:access_control.quota.${node.over_limit.kind}`) : t(`${P}quota_unknown`), used: node.over_limit.used ?? '—', limit: node.over_limit.limit ?? '—' })}</Typography>}
    {node.sniffing_insufficient.map(listener => <Stack key={listener.listener} direction="row" sx={{ flexWrap: 'wrap', alignItems: 'center', gap: 1 }}>
      <Box sx={{ flex: '1 1 200px' }}><Typography variant="body2" color="error">{t(`${P}sniffing`, { listener: listener.label })}</Typography>
        <Typography variant="body2" color="text.secondary">{t(`${P}sniffing_impact`)}</Typography></Box>
      {listener.node_id !== null && Number.isSafeInteger(listener.node_id) && listener.node_id > 0 && <Button component={RouterLink} to={`/admin/nodes?inbound=${listener.node_id}`} sx={linkStyle}>{t(`${P}edit_inbound`)}</Button>}
    </Stack>)}
    {fallback !== 'none' && <Alert sx={{ mt: 1 }} severity={fallback === 'exhausted' || fallback === 'stopping' ? 'error' : 'warning'}>{t(`${P}fallback_${fallback}`)}
      {fallback === 'applied' && <Typography variant="body2">{t(`${P}fallback_members`)}</Typography>}
    </Alert>}
    <Stack direction="row" sx={{ flexWrap: 'wrap', gap: 1 }}>
      {needsIssues && issueLink && <Button component={RouterLink} to={issueLink} sx={linkStyle}>{t(`${P}issues`)}</Button>}
      {node.state === 'unsupported_version' && <Button component={RouterLink} to={serverLink} sx={linkStyle}>{t(`${P}upgrade`)}</Button>}
      {node.state === 'over_limit' && (onLists ? <Button onClick={onLists} sx={linkStyle}>{t(`${P}view_lists`)}</Button> : <Button component={RouterLink} to="/admin/access-control?tab=lists" sx={linkStyle}>{t(`${P}view_lists`)}</Button>)}
      {onRetry && node.agent_id && node.kind === 'psp' && node.supports.policy && ['pending', 'rejected', 'over_limit', 'sniffing'].includes(node.state) && <AsyncButton pending={retryPending} onClick={() => onRetry(node.agent_id!)} sx={linkStyle}>{t(`${P}retry`)}</AsyncButton>}
    </Stack>
    <Menu anchorEl={menu} open={!!menu} onClose={() => setMenu(null)}>
      {node.kind === 'psp' && canWrite && <MenuItem onClick={() => { setMenu(null); setCollectOwner(owner) }} sx={linkStyle}>{t('admin:access_control.collect.open')}</MenuItem>}
      <MenuItem component={RouterLink} to={serverLink} onClick={() => setMenu(null)} sx={linkStyle}>{t(`${P}open_server`)}</MenuItem>
      {issueLink && <MenuItem component={RouterLink} to={issueLink} onClick={() => setMenu(null)} sx={linkStyle}>{t(`${P}issues`)}</MenuItem>}
    </Menu>
    {collectOwner === owner && canWrite && <AuditCollectDialog node={node} onClose={() => setCollectOwner(null)} />}
  </Box>
}
