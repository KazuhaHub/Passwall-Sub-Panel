import { useCallback, useRef, useState } from 'react'
import type { SaveBarError } from './SaveBar'

/** One rule per field: returns the message that says how to fix it, or null when the value is fine. */
export type FieldRules<D> = { [K in keyof D]?: (draft: D) => string | null }
export type FieldErrors<D> = { [K in keyof D]?: string }

/**
 * Validate-on-blur for pages that keep their own draft state (plan 3.3). It
 * exists for the three behaviours every form was re-implementing: an error
 * appears when a field is LEFT, not while it is typed into; it disappears the
 * moment an edit fixes it; and a failed save can put the server's message on
 * the field it is about. It owns no values and has no opinion on submitting.
 */
export function useFieldErrors<D>(rules: FieldRules<D>) {
  // `soft` marks the errors that do not stop a save (see setError); every other error does.
  const [state, setState] = useState<{ errors: FieldErrors<D>; soft: { [K in keyof D]?: true } }>({ errors: {}, soft: {} })
  const { errors, soft } = state
  // Rules are an object literal at the call site, new every render.
  const rulesRef = useRef(rules)
  rulesRef.current = rules

  const run = useCallback((name: keyof D, draft: D): string | null => rulesRef.current[name]?.(draft) ?? null, [])

  const put = useCallback((name: keyof D, message: string | null, blocking: boolean) => {
    setState(prev => {
      const wasSoft = prev.soft[name] === true
      if ((prev.errors[name] ?? null) === message && (message === null || wasSoft === !blocking)) return prev
      const errors = { ...prev.errors }
      const soft = { ...prev.soft }
      if (message === null) { delete errors[name]; delete soft[name] }
      else { errors[name] = message; if (blocking) delete soft[name]; else soft[name] = true }
      return { errors, soft }
    })
  }, [])

  /** The field was left: validate it. A failing rule blocks the save. */
  const blur = useCallback((name: keyof D, draft: D) => put(name, run(name, draft), true), [put, run])

  /**
   * The field changed: re-validate only if it is already showing an error, so a fix registers at
   * once. An error the rule does not repeat (a refusal that came back from the server) goes away
   * with the edit; one the rule does produce is the rule's, and blocks.
   */
  const change = useCallback((name: keyof D, draft: D) => {
    setState(prev => {
      if (prev.errors[name] === undefined) return prev
      const message = run(name, draft)
      if (message === prev.errors[name]) return prev
      const errors = { ...prev.errors }
      const soft = { ...prev.soft }
      delete soft[name]
      if (message === null) delete errors[name]
      else errors[name] = message
      return { errors, soft }
    })
  }, [run])

  /** Validate every field with a rule; true when the form may be saved. */
  const validateAll = useCallback((draft: D): boolean => {
    const next: FieldErrors<D> = {}
    for (const name of Object.keys(rulesRef.current) as (keyof D)[]) {
      const message = run(name, draft)
      if (message !== null) next[name] = message
    }
    setState({ errors: next, soft: {} })
    return Object.keys(next).length === 0
  }, [run])

  const reset = useCallback(() => setState({ errors: {}, soft: {} }), [])

  /**
   * Put a message on a field (null clears it): the server's refusal after a failed save, say.
   * It does NOT stop the next save by default: the person may only need to retry, so the message
   * stays visible and named in the SaveBar, but Save stays on. Pass `{ blocking: true }` for one
   * that must be fixed first.
   */
  const setError = useCallback((name: keyof D, message: string | null, opts?: { blocking?: boolean }) => put(name, message, opts?.blocking === true), [put])

  /** Whether this field's error stops a save: give it to the Field as `errorBlocksSave`. */
  const blocks = useCallback((name: keyof D): boolean => soft[name] !== true, [soft])

  /**
   * The current errors as SaveBar errors, for the ones no Field shows (a Field given its
   * `error` and `errorBlocksSave={blocks(name)}` reports itself, and passing both is merged into
   * one). `labels` names each field; `idOf` gives the id of its control, which is what orders the
   * error among the other problems by its place in the page and lets Save take the person to it.
   */
  const saveBarErrors = useCallback((labels: { [K in keyof D]?: string }, idOf?: (name: keyof D) => string): SaveBarError[] =>
    (Object.keys(errors) as (keyof D)[]).flatMap(name => {
      const message = errors[name]
      return message === undefined ? [] : [{ label: labels[name], message, blocking: soft[name] !== true, ...(idOf ? { id: idOf(name) } : {}) }]
    }), [errors, soft])

  return { errors, count: Object.keys(errors).length, blur, change, validateAll, setError, blocks, reset, saveBarErrors }
}
