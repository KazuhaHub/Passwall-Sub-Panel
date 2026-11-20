import { useEffect, useMemo, useRef, useState, type Dispatch, type SetStateAction } from 'react'
import { checkRoutes, type RouteBudget, type RouteView, type RouteWarning, type RoutesView } from '@/api/routes'
import { pushToast } from '@/components/feedback/toasts'
import { useSaveRoutes } from '@/query/routes'
import { useQueryScope } from '@/query/useQueryScope'
import { useNodesT } from '../../shared/useNodesT'
import { draftIssues, draftOf, inputOf, isDirty, type DraftRoute } from './routeDraft'
import { routeErrorText, type RouteLabeler } from './routeErrors'

const CHECK_DEBOUNCE_MS = 600
const NO_ROWS: DraftRoute[] = []

const sameRoutes = (a: readonly RouteView[], b: readonly RouteView[]) =>
  JSON.stringify(inputOf(draftOf(a))) === JSON.stringify(inputOf(draftOf(b)))

export type SetupRows = { rows: DraftRoute[]; setRows: Dispatch<SetStateAction<DraftRoute[]>> }

/**
 * The editing state of a node's routes: the draft (adopted from the server, or held by the create wizard in setup
 * mode), whether it differs from what is saved, the local checks, the dry run against the server, and the save.
 * Kept out of the section so the section is only layout.
 */
export function useRoutesDraft({ nodeId, data, setup }: { nodeId: number; data: RoutesView | undefined; setup?: SetupRows }) {
  const t = useNodesT()
  const scope = useQueryScope()
  const save = useSaveRoutes(scope, nodeId)

  const [savedDraft, setSavedDraft] = useState<DraftRoute[] | null>(null)
  const draft = setup?.rows ?? savedDraft
  const setDraft: Dispatch<SetStateAction<DraftRoute[] | null>> = setup
    ? update => setup.setRows(previous => (typeof update === 'function' ? update(previous) : update) ?? [])
    : setSavedDraft
  const base = useRef<RouteView[]>([])
  // Adopt what the server holds when the editor opens and whenever it changes while the admin has no edits of
  // their own; edits are never overwritten.
  useEffect(() => {
    if (setup || !data) return
    if (draft === null || (!isDirty(draft, base.current) && !sameRoutes(data.routes, base.current))) setDraft(draftOf(data.routes))
    base.current = data.routes
  // The draft is read, not a trigger: only a new server answer re-adopts.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data])

  const rows = draft ?? NO_ROWS
  const dirty = !setup && !!data && draft !== null && isDirty(draft, data.routes)
  const issues = useMemo(() => draftIssues(rows), [rows])
  const supported = data?.supported ?? false

  // Rows are named by position, with the label when there is one; the server refers to routes by id.
  const labelOf: RouteLabeler = ref => {
    if (typeof ref === 'number') return `#${ref}`
    const i = rows.findIndex(r => r.id === ref)
    if (i < 0) return String(ref ?? '')
    return rows[i].name.trim() ? `#${i + 1} ${rows[i].name.trim()}` : `#${i + 1}`
  }

  // The dry run: warnings and the budget for the draft before it is saved.
  const [check, setCheck] = useState<RoutesView | null>(null)
  const [checkFailed, setCheckFailed] = useState('')
  const [saved, setSaved] = useState<{ warnings: RouteWarning[]; budget?: RouteBudget } | null>(null)
  const [saveError, setSaveError] = useState('')
  const draftJson = JSON.stringify(inputOf(rows))
  useEffect(() => {
    // The save's own warnings and refusal stay until the admin edits again.
    if (dirty) { setSaved(null); setSaveError('') }
    if (!dirty || !supported || issues.length > 0) { setCheck(null); setCheckFailed(''); return }
    const controller = new AbortController()
    const timer = setTimeout(() => {
      checkRoutes(nodeId, inputOf(rows), { signal: controller.signal })
        .then(view => { if (!controller.signal.aborted) { setCheck(view); setCheckFailed('') } })
        .catch(err => { if (!controller.signal.aborted) { setCheck(null); setCheckFailed(routeErrorText(t, err, labelOf)) } })
    }, CHECK_DEBOUNCE_MS)
    return () => { controller.abort(); clearTimeout(timer) }
  // The JSON of the draft is the trigger; rows and labelOf follow from it.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draftJson, dirty, supported, issues.length, nodeId])

  async function doSave(routes: DraftRoute[]) {
    setSaveError('')
    try {
      const view = await save.mutateAsync(inputOf(routes))
      base.current = view.routes
      setDraft(draftOf(view.routes))
      setSaved({ warnings: view.warnings, budget: view.budget })
      pushToast({ message: t('nodes.routes.saved'), severity: 'success' })
    } catch (err) {
      setSaveError(routeErrorText(t, err, labelOf))
    }
  }

  return { rows, setDraft, dirty, issues, supported, labelOf, check, checkFailed, saved, saveError, saving: save.isPending, doSave }
}
