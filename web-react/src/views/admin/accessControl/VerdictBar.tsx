import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { AsyncButton } from '@/components/common/AsyncButton'
import { TONE_STYLE, type Tone } from '@/components/common/tones'
import { ErrorState } from '@/components/feedback/ErrorState'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { IconChevronRight, IconError, IconPending, IconSuccess, IconWarning, IconCircle, type IconType } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { useDestLists, useDestPolicies, useDestSourceRules, useDestStatus } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { accessVerdict, applyMinutes, collectingNodeCount, enabledPolicies, limitKindKey, type AccessVerdict, type StatusLineTone } from '@/utils/accessControl'
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
    <button type="button" onClick={onClick}
      className="inline cursor-pointer rounded-sm align-baseline underline underline-offset-2 outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
      {children}
    </button>
  )
}

// The verdict's five levels on the shared tone set. The icon and the words carry the level as well as
// the colour, and only the left edge and the icon take the hue; the sentence stays in the foreground.
const LEVEL: Record<StatusLineTone, { tone: Tone; Icon: IconType }> = {
  failing: { tone: 'danger', Icon: IconError },
  attention: { tone: 'warning', Icon: IconWarning },
  measuring: { tone: 'info', Icon: IconPending },
  ok: { tone: 'success', Icon: IconSuccess },
  quiet: { tone: 'neutral', Icon: IconCircle },
}

/** The one summary on the page: a level, a sentence, what it is made of, when it was read, and the action it offers. */
function VerdictLine({ level, dataTone, title, detail, meta, actions }: {
  level: StatusLineTone
  dataTone: string
  title: ReactNode
  detail?: ReactNode
  meta?: ReactNode
  actions?: ReactNode
}) {
  const { tone, Icon } = LEVEL[level]
  const style = TONE_STYLE[tone]
  return (
    <div data-testid="access-verdict" data-tone={dataTone}
      className={cn('mb-4 flex flex-col gap-3 rounded-lg border border-l-4 bg-card p-3 sm:flex-row sm:items-start', style.line)}>
      <div className="flex min-w-0 flex-1 items-start gap-2.5">
        <Icon aria-hidden className={cn('mt-0.5 size-4.5 shrink-0', style.ink)} />
        <div className="min-w-0">
          {/* Only the sentence is the live region: the countdown and the read-at time are aria-hidden, so a poll or a ticking second is never read out. */}
          <p role="status" className="font-semibold">{title}</p>
          {detail ? <div className="mt-1 text-sm text-muted-foreground">{detail}</div> : null}
          {meta ? <div aria-hidden="true" className="mt-0.5 text-xs text-muted-foreground">{meta}</div> : null}
        </div>
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
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

  if (status.isPending) return <Skeleton className="mb-4 h-[4.5rem] w-full" />
  if (status.isError || !data || !verdict) {
    return <ErrorState compact className="mb-4" title={t(`${V}read_failed`)} retry={() => void status.refetch()} />
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
          <span aria-hidden className="block">{t(`${V}detail.countdown`, { count: eta })}</span>
          <span className="block">{t(`${V}detail.restart`)}</span>
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
      action = <Button size="sm" variant="outline" onClick={() => onOpenNodes()}>{t(`${V}action.nodes`)}<IconChevronRight /></Button>
      break
    case 'lists':
      action = <Button size="sm" variant="outline" onClick={onOpenLists}>{t(`${V}action.lists`)}<IconChevronRight /></Button>
      break
    case 'resume':
      action = <AsyncButton size="sm" onClick={onResume}>{t(`${V}action.resume`)}</AsyncButton>
      break
    case 'publish':
      action = <AsyncButton size="sm" onClick={onPublish}>{t(`${V}action.publish`)}</AsyncButton>
      break
  }

  const readAt = new Date(status.dataUpdatedAt).toLocaleTimeString(i18n.language, { hour12: false })
  const stale = status.isRefetchError
  const metaText = stale ? `${t(`${V}read_at`, { time: readAt })} · ${t(`${V}stale`)}` : t(`${V}read_at`, { time: readAt })

  return (
    <VerdictLine dataTone={`${verdict.tone}:${verdict.kind}`} level={verdict.tone}
      title={title[verdict.kind]} detail={detail} meta={metaText} actions={action} />
  )
}
