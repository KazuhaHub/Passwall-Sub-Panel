import { useMemo } from 'react'
import type { Front } from '@/api/fronts'
import { RelationGraph } from '@/components/diagram/RelationGraph'
import { useAdminT } from '../useAdminT'
import { frontGraph, type GraphWords } from './frontGraph'
import type { useFrontNames } from './useFrontNames'

/**
 * The shared ports drawn as "client, shared port, SNI, service" (RelationGraph), above the list that edits them.
 * Picking a box lights every route through it, which answers "what depends on this listener" without reading rows.
 */
export default function FrontGraphPanel({ fronts, inactive, names }: {
  fronts: readonly Front[]
  inactive: ReadonlySet<number>
  names: ReturnType<typeof useFrontNames>
}) {
  const t = useAdminT()
  const model = useMemo(() => {
    const words: GraphWords = {
      columns: [t('admin:servers.fronts.graph.col_port'), t('admin:servers.fronts.graph.col_sni'), t('admin:servers.fronts.graph.col_target')],
      port: port => t('admin:servers.fronts.front_title', { port }),
      routes: count => t('admin:servers.fronts.graph.routes', { count }),
      other: t('admin:servers.fronts.graph.other'),
      listener: (id, loopback) => ({
        label: names.nodeName(id),
        sublabel: loopback ? t('admin:servers.fronts.graph.loopback', { port: loopback }) : undefined,
      }),
      forward: (node, line) => t('admin:servers.fronts.target.forward_short', { node: names.nodeName(node), line }),
      link: link => t('admin:servers.fronts.target.link_short', { link }),
      external: (host, port) => `${host}:${port}`,
    }
    return frontGraph(fronts, inactive, words)
  }, [fronts, inactive, names, t])

  return (
    <section aria-label={t('admin:servers.fronts.graph.title')} className="flex flex-col gap-2">
      <p className="text-sm text-muted-foreground">{t('admin:servers.fronts.graph.caption')}</p>
      <RelationGraph columns={model.columns} edges={model.edges} maxHeight="22rem" />
    </section>
  )
}
