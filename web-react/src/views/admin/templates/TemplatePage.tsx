import { useState, type MutableRefObject } from 'react'
import { useTranslation } from 'react-i18next'
import { useLocation, useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'

import type { RuleSetSummary } from '@/api/rules'
import { deleteTemplate, resetTemplate, saveTemplate, type Template } from '@/api/templates'
import type { ListResponse } from '@/api/types'
import { ObjectPage } from '@/components/common/ObjectPage'
import { useObjectLeaveGuard } from '@/components/common/ObjectTabs'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { pushToast } from '@/components/feedback/toasts'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconCopy, IconDelete } from '@/lib/icons'
import { templateKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { discardChangesConfirm } from '../accessControl/confirmCopy'
import { askDeleteTemplate, askResetTemplate, isProtected, isSeeded } from './actions'
import { copyOf, sameTemplate } from './draft'
import { TemplateForm } from './TemplateForm'
import { newTemplatePath, RESERVED_SLUG, templatePath } from './templateRoutes'

/**
 * One template as a page, for an existing one and for the create form (`seed` is then blank, or a copy). The save sends
 * the draft as it is, exactly as the old side sheet did; the hint under the rule-set picker only reads it. Leaving with
 * an edited draft asks once; a finished create and a delete made here leave without asking (`leaving`, set before
 * navigating because the dirty state clears a render later).
 */
export function TemplatePage({ mode, seed, existingSlugs, ruleSets, back, readOnly, leaving, onReseed }: {
  mode: 'edit' | 'create'
  /** What the form starts from: the stored template, a blank one, or a copy. */
  seed: Template
  existingSlugs: readonly string[]
  ruleSets: readonly RuleSetSummary[]
  back: string
  readOnly: boolean
  leaving: MutableRefObject<boolean>
  /** After a restore: the route reloads the list and starts this page again from the restored content. */
  onReseed: () => Promise<void>
}) {
  const { t } = useTranslation(['admin', 'common'])
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const scope = useQueryScope()
  const editing = mode === 'edit'
  const [form, setFormState] = useState<Template>(() => copyOf(seed))
  // What "unsaved" is measured against: the stored template; a new one (or a copy) is unsaved from the start, so it is compared with a blank one.
  const [baseline, setBaseline] = useState<Template>(() => copyOf(editing ? seed : { slug: '', name: '', client_type: 'mihomo', is_default: false, rule_sets: [], content: '' }))
  const [attempted, setAttempted] = useState(false)
  const [pending, setPending] = useState(false)
  const [acting, setActing] = useState(false)
  const dirty = !readOnly && !sameTemplate(form, baseline)
  useObjectLeaveGuard({ dirty, copy: discardChangesConfirm(t), bypass: leaving })

  const slugTaken = !editing && existingSlugs.includes(form.slug)
  const slugReserved = !editing && form.slug === RESERVED_SLUG
  const slugError = attempted && !form.slug ? t('admin:templates.validate.slug_name_required')
    : attempted && slugTaken ? t('admin:templates.validate.slug_exists', { slug: form.slug })
    : attempted && slugReserved ? t('admin:templates.validate.slug_reserved', { slug: form.slug }) : undefined
  const nameError = attempted && !form.name ? t('admin:templates.validate.slug_name_required') : undefined

  function patchList(update: (prev: Template[]) => Template[]) {
    queryClient.setQueryData<ListResponse<Template>>(templateKeys.list(scope, {}), prev => (prev ? { ...prev, items: update(prev.items) } : prev))
  }

  async function save() {
    if (!form.slug || !form.name) { setAttempted(true); pushToast({ message: t('admin:templates.validate.slug_name_required'), severity: 'warning' }); return }
    // Save is a slug-keyed upsert: a new template with an existing slug would silently overwrite that one.
    if (slugTaken) { setAttempted(true); pushToast({ message: t('admin:templates.validate.slug_exists', { slug: form.slug }), severity: 'warning' }); return }
    if (slugReserved) { setAttempted(true); pushToast({ message: t('admin:templates.validate.slug_reserved', { slug: form.slug }), severity: 'warning' }); return }
    setPending(true)
    try {
      await saveTemplate(form)
      pushToast({ message: t('admin:templates.toast.saved'), severity: 'success' })
      if (editing) {
        patchList(prev => prev.map(item => (item.slug === form.slug ? copyOf(form) : item)))
        setBaseline(copyOf(form))
      } else {
        leaving.current = true
        patchList(prev => [...prev, copyOf(form)])
        void queryClient.invalidateQueries({ queryKey: templateKeys.lists(scope) })
        // The new template's own page, with the list's place still behind its back link.
        void navigate(templatePath(form.slug), { replace: true, state: location.state })
      }
    } catch { /* The request layer already surfaced the failure. */ } finally { setPending(false) }
  }

  async function remove() {
    if (!await askDeleteTemplate(t, seed.slug)) return
    setActing(true)
    try {
      await deleteTemplate(seed.slug)
      pushToast({ message: t('admin:templates.toast.deleted'), severity: 'success' })
      leaving.current = true
      patchList(prev => prev.filter(item => item.slug !== seed.slug))
      void navigate(back, { replace: true })
    } catch { /* The request layer already surfaced the failure. */ } finally { setActing(false) }
  }

  async function restore() {
    if (!await askResetTemplate(t, seed.slug)) return
    setActing(true)
    try {
      await resetTemplate(seed.slug)
      pushToast({ message: t('admin:templates.toast.reset'), severity: 'success' })
      await onReseed()
    } catch { /* The request layer already surfaced the failure. */ } finally { setActing(false) }
  }

  const more: MoreMenuItem[] = []
  if (editing && !readOnly) {
    if (isSeeded(seed)) more.push({ id: 'reset', label: t('admin:templates.reset_to_default'), disabled: acting, onSelect: () => void restore() })
    // A protected template keeps its Delete item, off, saying why, instead of silently having none.
    more.push(isProtected(seed)
      ? { id: 'delete', label: t('admin:templates.row_delete'), disabledReason: t(isSeeded(seed) ? 'admin:templates.cannot_delete_seeded' : 'admin:templates.cannot_delete_default'), icon: IconDelete, destructive: true, disabled: true }
      : { id: 'delete', label: t('admin:templates.row_delete'), icon: IconDelete, destructive: true, disabled: acting, onSelect: () => void remove() })
  }

  const title = editing ? (baseline.name || baseline.slug) : t('admin:templates.create')
  return (
    <ObjectPage
      back={{ to: back, label: t('admin:templates.title') }}
      title={title}
      badges={editing ? (
        <>
          <Badge variant="outline">{baseline.client_type}</Badge>
          {baseline.is_default && <Badge variant="secondary">{t('admin:templates.status.default')}</Badge>}
        </>
      ) : undefined}
      subtitle={editing ? <span className="font-mono text-xs">{baseline.slug}</span> : undefined}
      secondary={editing && !readOnly ? (
        <Button variant="outline" disabled={acting} onClick={() => void navigate(newTemplatePath(seed.slug), { state: location.state })}>
          <IconCopy />{t('admin:templates.duplicate')}
        </Button>
      ) : undefined}
      more={more}
    >
      {readOnly && <Alert className="mb-4"><AlertDescription>{t('admin:templates.readonly_note')}</AlertDescription></Alert>}
      <FormValidityProvider>
        <form onSubmit={e => { e.preventDefault(); void save() }} noValidate>
          <TemplateForm form={form} setForm={u => setFormState(u)} editing={editing} readOnly={readOnly} slugError={slugError} nameError={nameError} ruleSets={ruleSets} />
          {!readOnly && <SaveBar dirty={dirty} pending={pending} onSave={() => void save()} onDiscard={() => { setFormState(copyOf(editing ? baseline : seed)); setAttempted(false) }} />}
        </form>
      </FormValidityProvider>
    </ObjectPage>
  )
}
