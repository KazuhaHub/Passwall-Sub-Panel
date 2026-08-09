import { useCallback } from 'react'
import { useSearchParams } from 'react-router'

/**
 * Persist a tab selection in the URL query string so a refresh stays put.
 * The default value is omitted from the URL to keep links short.
 */
export function useTabParam<T extends string>(
  name: string,
  fallback: T,
  allowed?: readonly T[],
): [T, (next: T) => void] {
  const [params, setParams] = useSearchParams()
  const raw = params.get(name)
  const value: T = (() => {
    if (!raw) return fallback
    if (allowed && !allowed.includes(raw as T)) return fallback
    return raw as T
  })()
  const setValue = useCallback((next: T) => {
    setParams(prev => {
      const out = new URLSearchParams(prev)
      if (next === fallback) out.delete(name)
      else out.set(name, next)
      return out
    }, { replace: true })
  }, [name, fallback, setParams])
  return [value, setValue]
}
