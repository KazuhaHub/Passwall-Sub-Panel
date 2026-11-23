import type { ReactNode } from 'react'

import type { ScopeState } from '@/components/scope/scopeOverrides'
import { Button } from '@/components/ui/button'
import { useGroupsT } from './useGroupsT'

/** What the sheet knows of the group's overrides: still reading, could not read, or the editable state. */
export type ScopeLoad = { status: 'loading' } | { status: 'error' } | { status: 'ready'; scope: ScopeState }

/**
 * Shows its children once the group's overrides are loaded. They come from their own read (and the global
 * settings beside it), so they can fail or lag behind the rest of the sheet; until then the rows would show
 * a made-up "follows global", which is why nothing is drawn rather than a guess.
 */
export function ScopeGate({ load, retry, children }: {
  load: ScopeLoad
  retry: () => void
  children: (scope: ScopeState) => ReactNode
}) {
  const t = useGroupsT()
  if (load.status === 'loading') return <p role="status" className="text-sm text-muted-foreground">{t('admin:groups.scope.loading')}</p>
  if (load.status === 'error') {
    return (
      <div role="alert" className="flex flex-wrap items-center gap-3 text-sm">
        <span className="text-destructive">{t('admin:groups.scope.load_failed')}</span>
        <Button type="button" size="sm" variant="outline" onClick={retry}>{t('admin:groups.retry')}</Button>
      </div>
    )
  }
  return <>{children(load.scope)}</>
}
