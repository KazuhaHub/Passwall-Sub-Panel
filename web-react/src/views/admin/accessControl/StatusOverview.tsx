import { useEffect, useState } from 'react'
import { Alert, Box, Button, Skeleton, Stack, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import type { DestinationStatus } from '@/api/accessControl'
import StatusLine from '@/components/StatusLine'
import { AsyncButton } from '@/components/AsyncButton'
import { stateTone } from '@/components/ToneBadge'
import type { AccessVerdict, NodeFilter } from '@/utils/accessControl'
const P = 'admin:access_control.'
interface Props {
  data?: DestinationStatus
  verdict: AccessVerdict | null
  failed: boolean
  refreshing: boolean
  readAt: number
  busy: boolean
  onRetry: () => Promise<unknown>
  onOpenNodes: (filter?: NodeFilter) => void
  onOpenLists: (problem: boolean) => void
  onPublish: () => Promise<void>
  onPause: (paused: boolean) => Promise<void>
}
export default function StatusOverview({ data, verdict, failed, refreshing, readAt, busy, onRetry, onOpenNodes, onOpenLists, onPublish, onPause }: Props) {
  const { t } = useTranslation(['admin', 'common']), theme = useTheme()
  const staleTone = stateTone(theme, 'attention'), StaleIcon = staleTone.Icon
  const [now, setNow] = useState(Date.now())
  useEffect(() => { if (data?.next_publish_at == null) return; const timer = window.setInterval(() => setNow(Date.now()), 1000); return () => window.clearInterval(timer) }, [data?.next_publish_at])
  const retry = <AsyncButton pending={refreshing} onClick={onRetry}>{t('common:actions.retry')}</AsyncButton>
  if (!data) return failed
    ? <Alert sx={{ mb: 2 }} severity="error" action={retry}>{t(`${P}status_failed`)}</Alert>
    : <Skeleton variant="rounded" height={72} sx={{ mb: 2 }} role="progressbar" aria-label={t(`${P}status_unknown`)} aria-busy="true" />
  const title = verdict ? t(`${P}verdict.${verdict.kind}`, verdict as unknown as Record<string, unknown>) : t(`${P}status_unknown`)
  const counts = {
    third_party: data.nodes.filter(node => node.kind !== 'psp').length,
    upgrade: data.nodes.filter(node => node.kind === 'psp' && node.state === 'unsupported_version').length,
    offline: data.nodes.filter(node => node.kind === 'psp' && node.state === 'offline').length,
  }
  const fragments = (Object.keys(counts) as Array<keyof typeof counts>).filter(key => counts[key] > 0)
  const listIssue = verdict?.kind === 'list_failed' || verdict?.kind === 'list_pending'
  const listQuota = verdict?.kind === 'publication' && ['domains', 'regexps', 'cidrs'].includes(data.publish_error?.kind ?? '')
  const quotaKind = data.publish_error && ['rules', 'domains', 'regexps', 'cidrs', 'subjects', 'bytes'].includes(data.publish_error.kind)
  return <StatusLine stackActionsOnMobile tone={verdict?.tone ?? 'quiet'} title={title} announcement={title}
    detail={<Stack component="span" spacing={.5}>
      {!!fragments.length && <Box component="span">{t(`${P}not_executing.title`)} {fragments.map((key, index) => <Box component="span" key={key}>{index > 0 && ' · '}<Button size="small" color="inherit" sx={{ p: 0, minWidth: 0, textAlign: 'left' }} disabled={busy} onClick={() => onOpenNodes(key === 'upgrade' ? 'upgrade' : 'excluded')}>{t(`${P}not_executing.${key}`, { count: counts[key] })}</Button></Box>)}</Box>}
      {data.next_publish_at != null && !data.paused && <Typography component="span" aria-hidden="true" aria-label={t(`${P}publish_at`, { time: new Date(data.next_publish_at).toLocaleString() })}>{t(`${P}countdown`, { seconds: Math.max(0, Math.ceil((data.next_publish_at - now) / 1000)) })}</Typography>}
      {data.publish_error && <Typography component="span">{t(`${P}${quotaKind && data.publish_error.used != null && data.publish_error.limit != null ? 'publish_error' : 'publish_invalid'}`, { kind: quotaKind ? t(`${P}quota.${data.publish_error.kind}`) : '', used: data.publish_error.used, limit: data.publish_error.limit })}</Typography>}
    </Stack>}
    meta={<Stack component="span" spacing={.5}>
      {readAt > 0 && <Box component="span" aria-hidden="true">{t(`${P}read_at`, { time: new Date(readAt).toLocaleTimeString() })}</Box>}
      {failed && <Stack component="span" direction="row" sx={{ alignItems: 'center', gap: 1, flexWrap: 'wrap' }}><Box component="span" sx={{ display: 'inline-flex', alignItems: 'flex-start', gap: .5, maxWidth: '100%', color: staleTone.fg }}><StaleIcon aria-hidden sx={{ fontSize: 16, flexShrink: 0, color: staleTone.iconColor }} /><Box component="span" sx={{ minWidth: 0, overflowWrap: 'anywhere' }}>{t(`${P}status_stale`)}</Box></Box>{retry}</Stack>}
    </Stack>}
    actions={<Stack direction="row" sx={{ flexWrap: 'wrap', gap: 1 }}>
      <Button disabled={busy} onClick={() => onOpenNodes()}>{t(`${P}coverage.open`)}</Button>
      {(listIssue || listQuota) && <Button disabled={busy} onClick={() => onOpenLists(listIssue)}>{t(`${P}open_lists`)}</Button>}
      <AsyncButton pending={busy} onClick={onPublish}>{t(`${P}publish`)}</AsyncButton>
      <AsyncButton pending={busy} color={data.paused ? 'primary' : 'error'} onClick={() => onPause(!data.paused)}>{t(`${P}${data.paused ? 'resume' : 'pause'}`)}</AsyncButton>
    </Stack>} />
}
