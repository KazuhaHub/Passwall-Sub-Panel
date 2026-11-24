import { useRef, useState } from 'react'
import { Navigate, useParams, useSearchParams } from 'react-router'

import type { RuleSet } from '@/api/rules'
import type { Template } from '@/api/templates'
import { ObjectPage, objectErrorKind, type ObjectPageState } from '@/components/common/ObjectPage'
import { useBackTarget } from '@/components/common/objectReturn'
import { useTranslation } from 'react-i18next'
import { useRuleSets, useTemplates } from '@/query/rules'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { duplicateOf, EMPTY } from './draft'
import { TemplatePage } from './TemplatePage'
import { TEMPLATES_PATH } from './templateRoutes'

const NONE: never[] = []

/** The one read both routes share: the list (a template is found in it by slug) and the rule sets the picker offers. */
function useTemplateData() {
  const scope = useQueryScope()
  const templates = useTemplates(scope)
  const ruleSets = useRuleSets(scope)
  const failed = templates.isError || ruleSets.isError
  const ready = templates.data !== undefined && ruleSets.data !== undefined
  return {
    templates, ruleSets, failed, ready,
    items: templates.data?.items ?? (NONE as Template[]),
    rules: ruleSets.data?.items ?? (NONE as RuleSet[]),
    reload: () => Promise.all([templates.refetch(), ruleSets.refetch()]),
  }
}

/**
 * `/admin/templates/:slug`: one template as a page. Read from the list the Templates page already holds (there is
 * no other read to make), so a link opens it cold with the same two requests the list makes. A slug that is not in
 * the list is "no such template" in the page's own frame, with the way back to the list.
 */
export default function TemplateRoute() {
  const { t } = useTranslation(['admin'])
  const { slug = '' } = useParams()
  const back = useBackTarget(TEMPLATES_PATH)
  const canConfig = useCan('config.write')
  const data = useTemplateData()
  // Set when this page deleted the template: the page then leaves for the list instead of saying "not found".
  const leaving = useRef(false)
  // Bumped after a restore so the page starts again from the restored content (and a clean draft).
  const [rev, setRev] = useState(0)
  const found = data.items.find(i => i.slug === slug)
  const frame = { to: back, label: t('admin:templates.title') }

  if (found) {
    return (
      <TemplatePage key={`${slug}:${rev}`} mode="edit" seed={found} existingSlugs={data.items.map(i => i.slug)} ruleSets={data.rules}
        back={back} readOnly={!canConfig} leaving={leaving} onReseed={async () => { await data.reload(); setRev(r => r + 1) }} />
    )
  }
  if (data.failed) {
    const error = data.templates.error ?? data.ruleSets.error
    const kind = objectErrorKind(error)
    const state: ObjectPageState = kind === 'forbidden' ? { kind, title: t('admin:templates.detail.forbidden') }
      : { kind: 'error', reason: error, retry: () => data.reload(), title: t('admin:templates.load_failed') }
    return <ObjectPage back={frame} state={state} />
  }
  if (!data.ready) return <ObjectPage back={frame} state={{ kind: 'loading' }} />
  if (leaving.current) return <Navigate to={back} replace />
  return <ObjectPage back={frame} state={{ kind: 'not_found', title: t('admin:templates.detail.not_found') }} />
}

/**
 * `/admin/templates/new`: the create form, blank or (`?copy=<slug>`) a duplicate of a template. Creating needs config
 * write access, as the list's button does; without it the address says so instead of showing a form that cannot save.
 */
export function TemplateCreateRoute() {
  const { t } = useTranslation(['admin'])
  const back = useBackTarget(TEMPLATES_PATH)
  const canConfig = useCan('config.write')
  const [params] = useSearchParams()
  const copy = params.get('copy')
  const data = useTemplateData()
  const leaving = useRef(false)
  const frame = { to: back, label: t('admin:templates.title') }

  if (!canConfig) return <ObjectPage back={frame} state={{ kind: 'forbidden', title: t('admin:templates.detail.forbidden_create') }} />
  if (data.failed) return <ObjectPage back={frame} state={{ kind: 'error', reason: data.templates.error ?? data.ruleSets.error, retry: () => data.reload(), title: t('admin:templates.load_failed') }} />
  if (!data.ready) return <ObjectPage back={frame} state={{ kind: 'loading' }} />
  const source = copy ? data.items.find(i => i.slug === copy) : undefined
  return (
    <TemplatePage key={copy ?? ''} mode="create" seed={source ? duplicateOf(source) : EMPTY} existingSlugs={data.items.map(i => i.slug)} ruleSets={data.rules}
      back={back} readOnly={false} leaving={leaving} onReseed={async () => {}} />
  )
}
