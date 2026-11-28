import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useParams, useSearchParams } from 'react-router'

import type { Group } from '@/api/types'
import { ObjectPage, objectErrorKind, type ObjectPageState } from '@/components/common/ObjectPage'
import { useBackTarget } from '@/components/common/objectReturn'
import { useAllGroups } from '@/query/groups'
import { useRuleSetDetail, useRuleSets, useTemplates } from '@/query/rules'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { duplicateOf, EMPTY } from './model'
import { RuleSetPage } from './RuleSetPage'
import { RULESETS_PATH } from './ruleSetRoutes'

const NONE: never[] = []

/** The reads the page needs that are not the rule set itself: the templates that bind it and the user groups the members preview offers. */
function useRuleSetContext() {
  const scope = useQueryScope()
  const templates = useTemplates(scope)
  const groups = useAllGroups(scope)
  return {
    templates, groups,
    failed: templates.isError || groups.isError,
    ready: templates.data !== undefined && groups.data !== undefined,
    error: templates.error ?? groups.error,
    tpls: templates.data?.items ?? [],
    groupList: groups.data ?? (NONE as Group[]),
    reload: () => Promise.all([templates.refetch(), groups.refetch()]),
  }
}

/**
 * `/admin/rules/:slug`: one rule set as a page, read with its own `GET /admin/rules/:slug` (the list carries only summaries,
 * no rules text), next to the templates and groups the page shows. An administrator edits; an operator, who can read rule sets,
 * sees the same page without the means to change anything. A slug the server does not know is "no such rule set" in the page's
 * own frame.
 */
export default function RuleSetRoute() {
  const { t } = useTranslation(['admin'])
  const { slug = '' } = useParams()
  const scope = useQueryScope()
  const back = useBackTarget(RULESETS_PATH)
  const canConfig = useCan('config.write')
  const ctx = useRuleSetContext()
  const detail = useRuleSetDetail(scope, slug)
  // Set when this page deleted the rule set: the page then leaves for the list instead of saying "not found".
  const leaving = useRef(false)
  // Bumped after a restore so the page starts again from the restored content (and a clean draft).
  const [rev, setRev] = useState(0)
  const found = detail.data
  const frame = { to: back, label: t('admin:rules.page.breadcrumb') }

  if (found && ctx.ready) {
    return (
      <RuleSetPage key={`${slug}:${rev}`} mode="edit" seed={found} metaSource={found} existingSlugs={NONE} templates={ctx.tpls}
        groups={ctx.groupList} back={back} readOnly={!canConfig} leaving={leaving} onReseed={async () => { await Promise.all([detail.refetch(), ctx.reload()]); setRev(r => r + 1) }} />
    )
  }
  if (detail.isError || ctx.failed) {
    const error = detail.error ?? ctx.error
    const kind = objectErrorKind(error)
    const state: ObjectPageState = kind === 'forbidden' ? { kind, title: t('admin:rules.detail.forbidden') }
      : kind === 'not_found' && detail.isError ? { kind, title: t('admin:rules.detail.not_found') }
      : { kind: 'error', reason: error, retry: () => { void detail.refetch(); return ctx.reload() }, title: t('admin:rules.load_failed') }
    return <ObjectPage back={frame} state={state} />
  }
  return <ObjectPage back={frame} state={{ kind: 'loading' }} />
}

/**
 * `/admin/rules/new`: the create form, blank or (`?copy=<slug>`) a duplicate of a rule set. Creating needs config write
 * access, as the list's button does; without it the address says so instead of showing a form that cannot save. The list
 * (summaries) supplies the names already taken; a copy reads its source whole, and a source that cannot be read leaves the
 * form blank, as an unknown `copy` always has.
 */
export function RuleSetCreateRoute() {
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const back = useBackTarget(RULESETS_PATH)
  const canConfig = useCan('config.write')
  const [params] = useSearchParams()
  const copy = params.get('copy')
  const rules = useRuleSets(scope)
  const ctx = useRuleSetContext()
  const source = useRuleSetDetail(scope, copy ?? '', canConfig && !!copy)
  const leaving = useRef(false)
  const frame = { to: back, label: t('admin:rules.page.breadcrumb') }

  if (!canConfig) return <ObjectPage back={frame} state={{ kind: 'forbidden', title: t('admin:rules.detail.forbidden_create') }} />
  if (rules.isError || ctx.failed) return <ObjectPage back={frame} state={{ kind: 'error', reason: rules.error ?? ctx.error, retry: () => Promise.all([rules.refetch(), ctx.reload()]), title: t('admin:rules.load_failed') }} />
  if (rules.data === undefined || !ctx.ready || (copy && source.isPending)) return <ObjectPage back={frame} state={{ kind: 'loading' }} />
  const from = copy ? source.data : undefined
  return (
    <RuleSetPage key={copy ?? ''} mode="create" seed={from ? duplicateOf(from) : EMPTY} metaSource={from ?? EMPTY} existingSlugs={rules.data.items.map(i => i.slug)}
      templates={ctx.tpls} groups={ctx.groupList} back={back} readOnly={false} leaving={leaving} onReseed={async () => {}} />
  )
}
