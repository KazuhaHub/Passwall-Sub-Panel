import type { TFunction } from 'i18next'
import type { Tone } from '@/components/common/tones'
import type { NodeIssueGroup } from '@/utils/nodeIssueGroups'

export const VIEWS = ['attention', 'diagnostic', 'all'] as const
export type IssueView = (typeof VIEWS)[number]

/** Which reports to list by review state. In the address as `state`; the default is the one an admin came for. */
export const REVIEW_STATES = ['unacknowledged', 'acknowledged', 'all'] as const
export type ReviewState = (typeof REVIEW_STATES)[number]

/** The API's `acknowledged` parameter: left out for "all", which is how it has always been asked. */
export function acknowledgedParam(state: ReviewState): boolean | undefined {
  return state === 'all' ? undefined : state === 'acknowledged'
}

export const pendingIds = (group: NodeIssueGroup) => group.issues.filter(i => !i.acknowledged_at).map(i => i.id)

export function issueTitle(group: NodeIssueGroup, view: IssueView, t: TFunction): string {
  if (view === 'diagnostic' && group.category === 'statistics') return t('node_issues.startup_title')
  return t(`node_issues.category_titles.${group.category}`)
}

export function suggestedAction(group: NodeIssueGroup, view: IssueView, t: TFunction): string {
  return t(view === 'diagnostic' && group.category === 'statistics' ? 'node_issues.startup_action' : `node_issues.category_actions.${group.category}`)
}

export function serverNameOf(group: NodeIssueGroup, t: TFunction): string {
  return group.issues.find(i => i.server_name)?.server_name || t('node_issues.unknown_server')
}

export type GroupReview = 'acknowledged' | 'unacknowledged' | 'mixed'
export function reviewOf(group: NodeIssueGroup): GroupReview {
  if (group.unacknowledgedCount === 0) return 'acknowledged'
  return group.unacknowledgedCount === group.issues.length ? 'unacknowledged' : 'mixed'
}

/** A report nobody has looked at is the thing to notice; a reviewed one is quiet. */
export const REVIEW_TONE: Record<GroupReview, Tone> = { unacknowledged: 'warning', mixed: 'warning', acknowledged: 'neutral' }
