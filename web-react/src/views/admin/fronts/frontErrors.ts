import type { TFunction } from 'i18next'
import { frontErrorOf, frontErrorWording } from '@/api/fronts'
import type { DraftIssue } from './frontDraft'

/** The words for a refusal: the editor's own text for its code, the server's English when the code is new. */
export function frontErrorText(t: TFunction, err: unknown): string {
  const e = frontErrorOf(err)
  if (!e) return err instanceof Error ? err.message : String(err)
  if (!e.code) return e.message
  const { key, values } = frontErrorWording(e)
  return t(key, values) as string
}

export function issueText(t: TFunction, issue: DraftIssue, nodeName: (id: number) => string): string {
  switch (issue.code) {
    case 'loopback': case 'loopback_probe': case 'listener_unrouted':
      return t(`admin:servers.fronts.issue.${issue.code}`, { node: nodeName(issue.node_id) }) as string
    case 'loopback_twice':
      return t('admin:servers.fronts.issue.loopback_twice', { port: issue.port }) as string
    case 'route_target': case 'route_names':
      return t(`admin:servers.fronts.issue.${issue.code}`, { route: issue.route }) as string
    case 'name_twice':
      return t('admin:servers.fronts.issue.name_twice', { name: issue.name, route: issue.route, owner: issue.owner }) as string
    default:
      return t(`admin:servers.fronts.issue.${issue.code}`) as string
  }
}
