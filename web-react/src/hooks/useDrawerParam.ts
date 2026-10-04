import { useCallback, useMemo } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router'

export interface DrawerHistoryState extends Record<string, unknown> {
  drawer?: string
  prefill?: { target?: string; port?: number; network?: string; userId?: number; groupId?: number }
}
export interface DrawerOptions<T> {
  parse?: (raw: string | null) => T | null
  exclusive?: readonly string[]
}
export interface DrawerParam<T = number> {
  id: T | null
  open: (id: T, options?: { replace?: boolean; state?: DrawerHistoryState }) => void
  close: () => void
}

function parseId(raw: string | null): number | null {
  if (!raw || !/^\d+$/.test(raw)) return null
  const id = Number(raw)
  return Number.isSafeInteger(id) && id > 0 ? id : null
}

// Opening pushes one owned history entry. Switching drawers can replace that
// entry atomically so Back cannot revive an intermediate sheet or its prefill.
export function useDrawerParam<T extends string | number = number>(param: string, options: DrawerOptions<T> = {}): DrawerParam<T> {
  const [params, setParams] = useSearchParams()
  const location = useLocation()
  const navigate = useNavigate()
  const state = useMemo<DrawerHistoryState>(() => location.state && typeof location.state === 'object' ? location.state : {}, [location.state])
  const id = options.parse ? options.parse(params.get(param)) : parseId(params.get(param)) as T | null
  const pushedHere = state.drawer === param

  const open = useCallback((next: T, navigation?: { replace?: boolean; state?: DrawerHistoryState }) => {
    setParams(prev => {
      const out = new URLSearchParams(prev)
      for (const other of options.exclusive ?? []) if (other !== param) out.delete(other)
      out.set(param, String(next))
      return out
    }, { replace: navigation?.replace ?? false, state: { ...state, ...navigation?.state, drawer: param } })
  }, [param, options.exclusive, setParams, state])

  const close = useCallback(() => {
    if (pushedHere) { void navigate(-1); return }
    const remaining = { ...state }
    delete remaining.drawer
    delete remaining.prefill
    setParams(prev => {
      const out = new URLSearchParams(prev)
      out.delete(param)
      return out
    }, { replace: true, state: Object.keys(remaining).length ? remaining : null })
  }, [pushedHere, navigate, param, setParams, state])

  return { id, open, close }
}
