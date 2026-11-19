import { useCallback, useRef, useState } from 'react'

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
  const [errors, setErrors] = useState<FieldErrors<D>>({})
  // Rules are an object literal at the call site, new every render.
  const rulesRef = useRef(rules)
  rulesRef.current = rules

  const run = useCallback((name: keyof D, draft: D): string | null => rulesRef.current[name]?.(draft) ?? null, [])

  const put = useCallback((name: keyof D, message: string | null) => {
    setErrors(prev => {
      if ((prev[name] ?? null) === message) return prev
      const next = { ...prev }
      if (message === null) delete next[name]
      else next[name] = message
      return next
    })
  }, [])

  /** The field was left: validate it. */
  const blur = useCallback((name: keyof D, draft: D) => put(name, run(name, draft)), [put, run])

  /** The field changed: re-validate only if it is already showing an error, so a fix registers at once. */
  const change = useCallback((name: keyof D, draft: D) => {
    setErrors(prev => {
      if (prev[name] === undefined) return prev
      const message = run(name, draft)
      if (message === prev[name]) return prev
      const next = { ...prev }
      if (message === null) delete next[name]
      else next[name] = message
      return next
    })
  }, [run])

  /** Validate every field with a rule; true when the form may be saved. */
  const validateAll = useCallback((draft: D): boolean => {
    const next: FieldErrors<D> = {}
    for (const name of Object.keys(rulesRef.current) as (keyof D)[]) {
      const message = run(name, draft)
      if (message !== null) next[name] = message
    }
    setErrors(next)
    return Object.keys(next).length === 0
  }, [run])

  const reset = useCallback(() => setErrors({}), [])

  return { errors, count: Object.keys(errors).length, blur, change, validateAll, setError: put as (name: keyof D, message: string | null) => void, reset }
}
