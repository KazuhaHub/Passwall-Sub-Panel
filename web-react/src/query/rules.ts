import { queryOptions, useQuery } from '@tanstack/react-query'
import { getRuleSet, listRuleSets, type RuleSetListParams, type RuleSetSummary } from '@/api/rules'
import { listTemplates, type Template, type TemplateListParams } from '@/api/templates'
import type { ListResponse } from '@/api/types'
import { ruleKeys, templateKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * Rule sets and templates. Both are YAML-backed configuration on disk behind an
 * mtime cache, so a read is cheap and a write is the only thing that can change
 * them — dictionary treatment, no polling.
 */
export function ruleSetsQuery(scope: QueryScope, params: RuleSetListParams = {}) {
  return queryOptions({
    queryKey: ruleKeys.list(scope, params),
    queryFn: ({ signal }): Promise<ListResponse<RuleSetSummary>> => listRuleSets(params, signal),
    ...freshness(policies.dictionaries),
  })
}

export function useRuleSets(scope: QueryScope, params: RuleSetListParams = {}) {
  return useQuery(ruleSetsQuery(scope, params))
}

/**
 * One whole rule set for the editor. Never served from a cache (`gcTime: 0`) and never refreshed behind an open editor
 * (`staleTime: Infinity`, no focus refetch): the page copies it into a draft once, so a document that changed under it
 * would be neither shown nor merged. Every open is exactly one read of this rule set, and the list is not involved.
 */
export function ruleSetDetailQuery(scope: QueryScope, slug: string) {
  return queryOptions({
    queryKey: ruleKeys.detail(scope, slug),
    queryFn: ({ signal }) => getRuleSet(slug, { signal, quiet: true }),
    staleTime: Infinity,
    gcTime: 0,
    refetchOnWindowFocus: false,
  })
}

export function useRuleSetDetail(scope: QueryScope, slug: string, enabled = true) {
  return useQuery({ ...ruleSetDetailQuery(scope, slug), enabled })
}

export function templatesQuery(scope: QueryScope, params: TemplateListParams = {}) {
  return queryOptions({
    queryKey: templateKeys.list(scope, params),
    queryFn: ({ signal }): Promise<ListResponse<Template>> => listTemplates(params, signal),
    ...freshness(policies.dictionaries),
  })
}

export function useTemplates(scope: QueryScope, params: TemplateListParams = {}) {
  return useQuery(templatesQuery(scope, params))
}
