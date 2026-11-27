import { client } from './client'
import type { ListResponse } from './types'

export interface RuleSet {
  slug: string
  name: string
  sort: number
  enabled: boolean
  direct_subscription_domain: boolean
  proxy_group_order: string[]
  proxy_group_members?: Record<string, ProxyGroupMember[]>
  proxy_group_options?: Record<string, ProxyGroupOptions>
  mihomo_sub_rules?: MihomoSubRule[]
  mihomo_rematch_outbounds?: MihomoRematchOutbound[]
  content: string
  /**
   * What the stored file hashed to when this copy was read (read-only: responses only). A save carries it back as
   * `base_revision` so the server can refuse to overwrite a newer one.
   */
  revision?: string
}

export interface MihomoSubRule {
  name: string
  content: string
}

export interface MihomoRematchOutbound {
  name: string
  target_rematch_name?: string
  target_sub_rule?: string
}

export interface ProxyGroupMember {
  kind: 'builtin' | 'proxy_group' | 'node' | 'line' | 'node_set' | 'rematch'
  value?: string
  node_id?: number
  line_id?: string
}

export type ProxyGroupType = 'select' | 'url-test' | 'fallback' | 'load-balance'
export type LoadBalanceStrategy = 'round-robin' | 'consistent-hashing' | 'sticky-sessions'

export interface ProxyGroupOptions {
  type: ProxyGroupType
  url?: string
  interval?: number
  lazy?: boolean
  timeout?: number
  tolerance?: number
  strategy?: LoadBalanceStrategy
}

export interface ProxyGroupIssue {
  level: 'error' | 'warning'
  section?: 'proxy_group' | 'rules' | 'sub_rule' | 'rematch_outbound'
  group?: string
  name?: string
  code?: string
  params?: Record<string, string | number>
  message: string
}

export interface ProxyGroupInspectNode {
  id: number
  display_name: string
  server_address: string
  region: string
  tags: string[]
  enabled: boolean
}

export interface ProxyGroupInspectGroup {
  name: string
  configured: boolean
  options_configured: boolean
  options: ProxyGroupOptions
  default_members: ProxyGroupMember[]
  members: ProxyGroupMember[]
}

export interface ProxyGroupInspection {
  groups: ProxyGroupInspectGroup[]
  builtins: string[]
  nodes: ProxyGroupInspectNode[]
  lines: ProxyGroupInspectLine[]
  regions: string[]
  tags: string[]
  issues: ProxyGroupIssue[]
}

export interface ProxyGroupInspectLine {
  node_id: number
  line_id: string
  kind: 'direct' | 'relay'
  name: string
  display_name: string
  available: boolean
  /** The entry's own subscription labels (ADR 0040). */
  region?: string
  tags?: string[]
}

export interface RuleSetListParams {
  page?: number
  page_size?: number
  keyword?: string
  sort_by?: string
  sort_dir?: 'asc' | 'desc'
}

export async function listRuleSets(params: RuleSetListParams = {}, signal?: AbortSignal) {
  const merged = { page: 1, page_size: 200, ...params }
  const { data } = await client.get<ListResponse<RuleSet>>('/admin/rules', { params: merged, signal })
  return data
}

export async function getRuleSet(slug: string) {
  const { data } = await client.get<RuleSet>(`/admin/rules/${slug}`)
  return data
}

/**
 * The body limit of the rule-set endpoints (router.go ruleSetBodyLimit): 8 MiB of JSON for the PUT and the two draft
 * compilers. The editor measures the bytes it is about to send against this before it sends anything.
 */
export const RULESET_BODY_LIMIT = 8 << 20

// The editor words a 413 and a stale save itself; every other failure of these requests still gets the usual toast.
const OWN_FAILURES = { _skipErrorToastFor: [413, 'ruleset_stale'] }

/** Why a rule-set request was refused, for the two refusals the editor words itself. */
export type RuleSetFailure =
  | { kind: 'too_large'; limitBytes?: number }
  | { kind: 'stale'; current?: RuleSet }
  | { kind: 'other' }

/**
 * Reads a failed rule-set request. ANY 413 is "too large": the panel's own is JSON with `limit_bytes`, but a reverse
 * proxy in front answers with an HTML page (nginx) and no limit, which the caller then says it does not know.
 * `stale` is the 409 `ruleset_stale`, which carries the server's current version when it could still be read.
 */
export function ruleSetFailureOf(err: unknown): RuleSetFailure {
  const res = (err as { response?: { status?: number; data?: unknown } } | null)?.response
  if (res?.status === 413) {
    const limit = (res.data as { limit_bytes?: unknown } | null | undefined)?.limit_bytes
    return { kind: 'too_large', limitBytes: typeof limit === 'number' && limit > 0 ? limit : undefined }
  }
  const data = res?.data as { code?: unknown; current?: unknown } | null | undefined
  if (res?.status === 409 && data?.code === 'ruleset_stale') {
    const cur = data.current
    return { kind: 'stale', current: cur && typeof cur === 'object' ? cur as RuleSet : undefined }
  }
  return { kind: 'other' }
}

/**
 * `baseRevision` is the revision the draft started from; the server answers 409 when the stored file has moved on since.
 * It is left out for a rule set that does not exist yet. The read-only `revision` never goes back in the body.
 */
export function ruleSetRequestBody(rs: RuleSet, baseRevision?: string) {
  const { revision: _revision, ...rest } = rs
  return baseRevision ? { ...rest, base_revision: baseRevision } : rest
}

export async function saveRuleSet(rs: RuleSet, baseRevision?: string) {
  const { data } = await client.put<RuleSet>(`/admin/rules/${rs.slug}`, ruleSetRequestBody(rs, baseRevision), OWN_FAILURES)
  return data
}

/** `claimTooLarge`: the caller words a 413 itself (the toast stays for every other failure). */
export async function inspectProxyGroups(req: {
  content: string
  proxy_group_members: Record<string, ProxyGroupMember[]>
  proxy_group_options: Record<string, ProxyGroupOptions>
  mihomo_sub_rules?: MihomoSubRule[]
  mihomo_rematch_outbounds?: MihomoRematchOutbound[]
}, signal?: AbortSignal, claimTooLarge = false) {
  const { data } = await client.post<ProxyGroupInspection>('/admin/rules/inspect-proxy-groups', req, claimTooLarge ? { signal, _skipErrorToastFor: [413] } : { signal })
  return data
}

export interface GroupSubscriptionPreview {
  group_id: number
  status: 'ready' | 'disabled'
  groups: { name: string; type?: string; members: string[] }[]
}

export async function previewProxyGroups(req: { preview_group_id?: number; draft: RuleSet }, signal?: AbortSignal) {
  const { data } = await client.post<GroupSubscriptionPreview>('/admin/rules/preview-proxy-groups', req, { signal, _skipErrorToast: true })
  return data
}

export async function deleteRuleSet(slug: string) {
  await client.delete(`/admin/rules/${slug}`)
}

// resetRuleSet overwrites the on-disk yaml with the binary's embedded
// seed copy. 404 means the slug is admin-created and has no canonical
// fallback — UI hides the affordance in that case.
export async function resetRuleSet(slug: string) {
  await client.post(`/admin/rules/${slug}/reset`)
}

// SEEDED_RULESET_SLUGS mirrors internal/seed/files/rulesets/. Keep in
// sync with the Go side.
export const SEEDED_RULESET_SLUGS = ['default_rules']
