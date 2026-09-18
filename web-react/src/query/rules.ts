import { queryOptions, useQuery } from '@tanstack/react-query'
import { listRuleSets, type RuleSet, type RuleSetListParams } from '@/api/rules'
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
    queryFn: ({ signal }): Promise<ListResponse<RuleSet>> => listRuleSets(params, signal),
    ...freshness(policies.dictionaries),
  })
}

export function useRuleSets(scope: QueryScope, params: RuleSetListParams = {}) {
  return useQuery(ruleSetsQuery(scope, params))
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
