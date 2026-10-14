import { useCallback, useRef } from 'react'
import { confirm, type ConfirmOpts } from '@/components/ConfirmHost'

export function useDirtyClose(dirty: boolean, copy: ConfirmOpts): () => Promise<boolean> {
  const pending = useRef<Promise<boolean> | null>(null)
  return useCallback(() => {
    if (pending.current) return pending.current
    if (!dirty) return Promise.resolve(true)
    pending.current = confirm(copy).finally(() => { pending.current = null })
    return pending.current
  }, [dirty, copy])
}
