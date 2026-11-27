import { useCallback } from 'react'
import { Link } from 'react-router'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import type { Node } from '@/api/types'
import { spacedT } from '@/i18n/spacing'

/** The admin namespace's `t` with interpolation boundaries resolved (copy rule 8). Keys read `certs.…`. */
export function useCertsT(): TFunction {
  const { t } = useTranslation('admin')
  // eslint-disable-next-line react-hooks/exhaustive-deps
  return useCallback(spacedT(t), [t])
}

/** The common namespace's `t`, for the words every page shares (Cancel, Save, Retry). */
export function useCommonT(): TFunction {
  const { t } = useTranslation('common')
  // eslint-disable-next-line react-hooks/exhaustive-deps
  return useCallback(spacedT(t), [t])
}

/** The name an admin gave a node, with the server it sits on when more than one node might share it. */
export function nodeName(n: Node): string {
  return n.display_name || `#${n.id}`
}

/** Links to the nodes that deploy a certificate; the node page is where its certificate source is changed. */
export function NodeLinks({ nodes, max, className }: { nodes: readonly Node[]; max?: number; className?: string }) {
  const t = useCertsT()
  const shown = max === undefined ? nodes : nodes.slice(0, max)
  const rest = nodes.length - shown.length
  return (
    <span className={className}>
      {shown.map((n, i) => (
        <span key={n.id}>
          {i > 0 && ', '}
          <Link to={`/admin/nodes/${n.id}`} className="underline decoration-dotted underline-offset-4 hover:decoration-solid" title={`${nodeName(n)} · ${n.panel_name}`}>{nodeName(n)}</Link>
        </span>
      ))}
      {rest > 0 && <span className="text-muted-foreground">{t('certs.used_more', { count: rest })}</span>}
    </span>
  )
}
