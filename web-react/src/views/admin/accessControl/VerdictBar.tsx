import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Alert, Box, Button, Link, Skeleton } from '@mui/material'
import ChevronRightIcon from '@mui/icons-material/ChevronRight'
import { useTranslation } from 'react-i18next'

import { AsyncButton } from '@/components/AsyncButton'
import StatusLine from '@/components/StatusLine'
import { useDestLists, useDestPolicies, useDestSourceRules, useDestStatus } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { accessVerdict, applyMinutes, collectingNodeCount, enabledPolicies, limitKindKey, type AccessVerdict } from '@/utils/accessControl'
import { formatRelativeTimeShort } from '@/utils/relativeTime'
import type { NodeFilter } from './accessParams'

const V = 'admin:access_control.verdict.'

/** A second-resolution clock, ticking only while `active` (the countdown of a pending publish). */
function useNow(active: boolean): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!active) { setNow(Date.now()); return }
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [active])
  return now
}

function Skip({ children, onClick }: { children: ReactNode; onClick?: () => void }) {
  if (!onClick) return <>{children}</>
  return (
    <Link component="button" type="button" underline="always" onClick={onClick}
      sx={{ color: 'inherit', font: 'inherit', verticalAlign: 'baseline' }}>
      {children}
    </Link>
  )
}

/**
 * THE PAGE VERDICT (S1): one sentence for how far the policy has landed across
 * the fleet, and why some nodes do not enforce it. Computed by accessVerdict
 * (pure, table-tested); this only says it. Only the main sentence is in the
 * live region: the countdown and the read-at time are in the aria-hidden lines
 * under it, so a 10-second poll or a ticking second is never read out.
 */
export default function VerdictBar({ onOpenNodes, onOpenLists, onResume, onPublish }: {
  /** Opens the node coverage sheet, optionally narrowed (`node_state`). */
  onOpenNodes: (filter?: NodeFilter) => void
  onOpenLists: () => void
  /** Confirms and resumes (the page owns the dialog). */
  onResume: () => Promise<void> | void
  /** `POST /dest/publish`. */
  onPublish: () => Promise<void> | void
}) {
  const { t, i18n } = useTranslation(['admin'])
  const scope = useQueryScope()
  const status = useDestStatus(scope)
  const policies = useDestPolicies(scope)
  const lists = useDestLists(scope)
  const sourceRules = useDestSourceRules(scope)

  const data = status.data
  const waiting = !!data && data.generation !== data.published_generation && !data.publish_error && !data.paused
  const now = useNow(waiting)

  const verdict: AccessVerdict | null = useMemo(() => {
    if (!data) return null
    // Deployed means on or report-only: a node runs both, and both need a node that can.
    const sourceOn = (sourceRules.data?.items ?? []).filter(r => r.state !== 'off').length
    const policiesOn = enabledPolicies(policies.data).length
    return accessVerdict(data, { policies: policies.data, lists: lists.data, hasDefinitions: policiesOn + sourceOn > 0, now })
  }, [data, policies.data, lists.data, sourceRules.data, now])

  if (status.isPending) return <Skeleton variant="rounded" height={72} sx={{ mb: 2 }} />
  if (status.isError || !data || !verdict) {
    return (
      <Alert severity="error" sx={{ mb: 2 }} action={<Button color="inherit" size="small" onClick={() => void status.refetch()}>{t('admin:access_control.retry')}</Button>}>
        {t(`${V}read_failed`)}
      </Alert>
    )
  }

  const num = new Intl.NumberFormat(i18n.language)
  const minutes = applyMinutes(data)
  const what = t(`${V}what.${limitKindKey(verdict.limit?.kind)}`)
  const limit = verdict.limit
  const publishTitle = limit && limit.kind !== 'invalid' && limit.used !== undefined && limit.limit !== undefined
    ? t(`${V}title.publish_refused`, { what, used: num.format(limit.used), limit: num.format(limit.limit) })
    : t(`${V}title.publish_refused_invalid`)
  const ago = (ms: number | null) => (ms === null ? '' : formatRelativeTimeShort(now - ms, t))
  const failedList = lists.data?.items.find(l => l.name === verdict.name)

  const title: Record<AccessVerdict['kind'], string> = {
    paused: t(`${V}title.paused`),
    publish_refused: publishTitle,
    rejected: t(`${V}title.refused`, { count: verdict.n }),
    sniffing: t(`${V}title.refused`, { count: verdict.n }),
    over_limit: t(`${V}title.refused`, { count: verdict.n }),
    sniffing_partial: t(`${V}title.sniffing_partial`, { count: verdict.n }),
    no_coverage: t(`${V}title.no_coverage`, { count: verdict.n }),
    upgrade: t(`${V}title.upgrade`, { count: verdict.n }),
    offline: t(`${V}title.offline`, { count: verdict.n }),
    list_failed: t(`${V}title.list_failed`, { name: verdict.name, ago: ago(failedList?.last_fetched_at ?? null) }),
    list_pending: t(`${V}title.list_pending`, { name: verdict.name }),
    waiting: minutes === null
      ? t(`${V}title.waiting_unknown`, { count: verdict.executors })
      : t(`${V}title.waiting`, { count: verdict.executors, minutes }),
    rolling: t(`${V}title.rolling`, { done: verdict.done, total: verdict.total }),
    rolling_slow: t(`${V}title.rolling`, { done: verdict.done, total: verdict.total }),
    applied: t(`${V}title.applied`, { count: verdict.n }),
    idle: t(`${V}title.idle`),
  }

  // The second line: what the verdict is made of. 3X-UI / S-UI not enforcing
  // is by design and is only ever written here, never a colour.
  const sk = verdict.skipped
  const parts: ReactNode[] = []
  if (sk.unsupportedKind > 0) parts.push(<span key="k">{t(`${V}skip.unsupported_kind`, { count: sk.unsupportedKind })}</span>)
  if (sk.notEnrolled > 0) parts.push(<span key="n">{t(`${V}skip.not_enrolled`, { count: sk.notEnrolled })}</span>)
  if (sk.upgrade > 0) parts.push(<Skip key="u" onClick={() => onOpenNodes('upgrade')}>{t(`${V}skip.upgrade`, { count: sk.upgrade })}</Skip>)
  if (sk.offline > 0) parts.push(<Skip key="o" onClick={() => onOpenNodes('inactive')}>{t(`${V}skip.offline`, { count: sk.offline })}</Skip>)

  let detail: ReactNode = null
  switch (verdict.kind) {
    case 'waiting': {
      const eta = verdict.nextPublishAt ? Math.max(0, Math.round((verdict.nextPublishAt - now) / 1000)) : 0
      detail = (
        <>
          <Box component="span" aria-hidden sx={{ display: 'block' }}>{t(`${V}detail.countdown`, { count: eta })}</Box>
          <Box component="span" sx={{ display: 'block' }}>{t(`${V}detail.restart`)}</Box>
        </>
      )
      break
    }
    case 'rejected':
    case 'over_limit':
    case 'sniffing_partial':
    case 'sniffing': {
      const node = data.nodes.find(n => n.panel_name === verdict.node)
      const reason = verdict.kind === 'sniffing_partial'
        ? t(`${V}detail.sniffing_partial`, { n: verdict.inbounds?.length ?? 0 })
        : verdict.kind === 'sniffing'
        ? t(`${V}detail.sniffing`, { inbounds: (verdict.inbounds ?? []).join(t(`${V}detail.list_separator`)) })
        : verdict.kind === 'over_limit' && node?.over_limit
          ? t(`${V}detail.over_limit`, {
            what: t(`${V}what.${limitKindKey(node.over_limit.kind)}`),
            used: num.format(node.over_limit.used ?? 0), limit: num.format(node.over_limit.limit ?? 0),
          })
          : t(`${V}detail.rejected`)
      detail = `${verdict.node}: ${reason}`
      break
    }
    case 'publish_refused': detail = t(`${V}detail.publish_refused`); break
    default: {
      // "Not enforcing: …" lists what does not run; the nodes that record hits are a fact about the fleet, not a
      // reason, so they follow after the separator and stand alone when nothing is skipped. Zero is not written.
      const recording = collectingNodeCount(data)
      const sep = <span key="rec-sep">{t(`${V}skip.separator`)}</span>
      const recordingPart = recording > 0 ? <span key="rec" data-testid="verdict-recording">{t(`${V}skip.recording`, { count: recording })}</span> : null
      const skipped = parts.length > 0
        ? [<span key="prefix">{t(`${V}skip.prefix`)}</span>, ...parts.flatMap((p, i) => (i === 0 ? [p] : [<span key={`s${i}`}>{t(`${V}skip.separator`)}</span>, p]))]
        : []
      const all = [...skipped, ...(recordingPart ? (skipped.length > 0 ? [sep, recordingPart] : [recordingPart]) : [])]
      detail = all.length > 0 ? all : null
    }
  }

  let action: ReactNode = null
  switch (verdict.action) {
    case 'nodes':
      action = <Button size="small" color="inherit" endIcon={<ChevronRightIcon />} onClick={() => onOpenNodes()}>{t(`${V}action.nodes`)}</Button>
      break
    case 'lists':
      action = <Button size="small" color="inherit" endIcon={<ChevronRightIcon />} onClick={onOpenLists}>{t(`${V}action.lists`)}</Button>
      break
    case 'resume':
      action = <AsyncButton size="small" variant="contained" onClick={onResume}>{t(`${V}action.resume`)}</AsyncButton>
      break
    case 'publish':
      action = <AsyncButton size="small" variant="contained" onClick={onPublish}>{t(`${V}action.publish`)}</AsyncButton>
      break
  }

  const readAt = new Date(status.dataUpdatedAt).toLocaleTimeString(i18n.language, { hour12: false })
  const stale = status.isRefetchError
  const metaText = stale ? `${t(`${V}read_at`, { time: readAt })} · ${t(`${V}stale`)}` : t(`${V}read_at`, { time: readAt })

  return (
    <StatusLine testId="access-verdict" dataTone={`${verdict.tone}:${verdict.kind}`} tone={verdict.tone}
      title={title[verdict.kind]} detail={detail} meta={metaText} actions={action}
      liveRegion="title" metaHidden />
  )
}
