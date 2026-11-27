import { useCallback, useEffect, useRef, useState } from 'react'
import { explainFailureOf, explainRoute, type ExplainFailure, type ExplainRequest, type ExplainResponse, type RuleSet } from '@/api/rules'

/** What a result was asked for: when any of it differs from what is on screen now, the result is out of date. */
export interface ExplainAsked {
  draft: RuleSet
  templateSlug: string
  userId: number | null
  /** The connection as typed, serialised. */
  probe: string
  /** What was sent (the size of a refused draft is measured from it). */
  request: ExplainRequest
}

export type ExplainOutcome =
  | { status: 'idle' }
  | { status: 'pending'; asked: ExplainAsked }
  | { status: 'done'; asked: ExplainAsked; response: ExplainResponse }
  | { status: 'failed'; asked: ExplainAsked; failure: ExplainFailure }

/**
 * The state of the routing test's server mode: which scope is chosen (this draft alone, in the browser, or a template, on the
 * server), the template and the user, and the last answer. It lives next to the page's tester (not in it) so that closing the
 * tester, or moving it between its column, its dialog and its phone screen, loses nothing.
 *
 * A request is sent only by `run`, which the Test button and Enter call: never because something was typed. One at a time: while
 * one is pending a second is refused, and leaving the page aborts it.
 */
export function useRouteExplain() {
  const [scope, setScope] = useState<'draft' | 'template'>('draft')
  const [templateSlug, setTemplateSlug] = useState('')
  const [userId, setUserId] = useState<number | null>(null)
  const [outcome, setOutcome] = useState<ExplainOutcome>({ status: 'idle' })
  const pending = useRef<AbortController | null>(null)

  useEffect(() => () => pending.current?.abort(), [])

  const run = useCallback((req: ExplainRequest, asked: ExplainAsked) => {
    if (pending.current) return
    const controller = new AbortController()
    pending.current = controller
    setOutcome({ status: 'pending', asked })
    void explainRoute(req, controller.signal).then(response => {
      if (controller.signal.aborted) return
      setOutcome({ status: 'done', asked, response })
    }).catch(err => {
      if (controller.signal.aborted) return
      setOutcome({ status: 'failed', asked, failure: explainFailureOf(err) })
    }).finally(() => { if (pending.current === controller) pending.current = null })
  }, [])

  /** Forgets a failure (an edit may have fixed what it concerned); a result stays, marked out of date by comparison. */
  const clearFailure = useCallback(() => setOutcome(o => (o.status === 'failed' ? { status: 'idle' } : o)), [])

  return { scope, setScope, templateSlug, setTemplateSlug, userId, setUserId, outcome, run, clearFailure }
}

export type RouteExplain = ReturnType<typeof useRouteExplain>
