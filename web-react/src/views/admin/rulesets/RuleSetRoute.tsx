import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Navigate, useParams, useSearchParams } from 'react-router'

import type { RuleSet } from '@/api/rules'
import type { Group } from '@/api/types'
import { ObjectPage, objectErrorKind, type ObjectPageState } from '@/components/common/ObjectPage'
import { useBackTarget } from '@/components/common/objectReturn'
import { useAllGroups } from '@/query/groups'
import { useRuleSets, useTemplates } from '@/query/rules'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { duplicateOf, EMPTY } from './model'
import { RuleSetPage } from './RuleSetPage'
import { RULESETS_PATH } from './ruleSetRoutes'

const NONE: never[] = []

/** The three reads the list makes, shared by both routes: the rule sets (one is found in the list by slug), the templates that bind them, and the user groups the members preview offers. */
function useRuleSetData() {
  const scope = useQueryScope()
  const rules = useRuleSets(scope)
  const templates = useTemplates(scope)
  const groups = useAllGroups(scope)
  const failed = rules.isError || templates.isError || groups.isError
  const ready = rules.data !== undefined && templates.data !== undefined && groups.data !== undefined
  return {
    rules, templates, groups, failed, ready,
    items: rules.data?.items ?? (NONE as RuleSet[]),
    tpls: templates.data?.items ?? [],
    groupList: groups.data ?? (NONE as Group[]),
    reload: () => Promise.all([rules.refetch(), templates.refetch(), groups.refetch()]),
  }
}

/**
 * `/admin/rules/:slug`: one rule set as a page, read from the list the Rule sets page already holds, so a link opens it
 * cold with the same three requests the list makes. An administrator edits; an operator, who can read rule sets, sees the
 * same page without the means to change anything. A slug that is not in the list is "no such rule set" in the page's own
 * frame.
 */
export default function RuleSetRoute() {
  const { t } = useTranslation(['admin'])
  const { slug = '' } = useParams()
  const back = useBackTarget(RULESETS_PATH)
  const canConfig = useCan('config.write')
  const data = useRuleSetData()
  // Set when this page deleted the rule set: the page then leaves for the list instead of saying "not found".
  const leaving = useRef(false)
  // Bumped after a restore so the page starts again from the restored content (and a clean draft).
  const [rev, setRev] = useState(0)
  const found = data.items.find(i => i.slug === slug)
  const frame = { to: back, label: t('admin:rules.page.breadcrumb') }

  if (found) {
    return (
      <RuleSetPage key={`${slug}:${rev}`} mode="edit" seed={found} metaSource={found} existingSlugs={data.items.map(i => i.slug)} templates={data.tpls}
        groups={data.groupList} back={back} readOnly={!canConfig} leaving={leaving} onReseed={async () => { await data.reload(); setRev(r => r + 1) }} />
    )
  }
  if (data.failed) {
    const error = data.rules.error ?? data.templates.error ?? data.groups.error
    const kind = objectErrorKind(error)
    const state: ObjectPageState = kind === 'forbidden' ? { kind, title: t('admin:rules.detail.forbidden') }
      : { kind: 'error', reason: error, retry: () => data.reload(), title: t('admin:rules.load_failed') }
    return <ObjectPage back={frame} state={state} />
  }
  if (!data.ready) return <ObjectPage back={frame} state={{ kind: 'loading' }} />
  if (leaving.current) return <Navigate to={back} replace />
  return <ObjectPage back={frame} state={{ kind: 'not_found', title: t('admin:rules.detail.not_found') }} />
}

/**
 * `/admin/rules/new`: the create form, blank or (`?copy=<slug>`) a duplicate of a rule set. Creating needs config write
 * access, as the list's button does; without it the address says so instead of showing a form that cannot save.
 */
export function RuleSetCreateRoute() {
  const { t } = useTranslation(['admin'])
  const back = useBackTarget(RULESETS_PATH)
  const canConfig = useCan('config.write')
  const [params] = useSearchParams()
  const copy = params.get('copy')
  const data = useRuleSetData()
  const leaving = useRef(false)
  const frame = { to: back, label: t('admin:rules.page.breadcrumb') }

  if (!canConfig) return <ObjectPage back={frame} state={{ kind: 'forbidden', title: t('admin:rules.detail.forbidden_create') }} />
  if (data.failed) return <ObjectPage back={frame} state={{ kind: 'error', reason: data.rules.error ?? data.templates.error ?? data.groups.error, retry: () => data.reload(), title: t('admin:rules.load_failed') }} />
  if (!data.ready) return <ObjectPage back={frame} state={{ kind: 'loading' }} />
  const source = copy ? data.items.find(i => i.slug === copy) : undefined
  return (
    <RuleSetPage key={copy ?? ''} mode="create" seed={source ? duplicateOf(source) : EMPTY} metaSource={source ?? EMPTY} existingSlugs={data.items.map(i => i.slug)}
      templates={data.tpls} groups={data.groupList} back={back} readOnly={false} leaving={leaving} onReseed={async () => {}} />
  )
}

