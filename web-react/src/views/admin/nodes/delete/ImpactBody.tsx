import type { DeleteImpactLine, NodeDeleteImpact } from '@/api/nodes'
import { useNodesT } from '../shared/useNodesT'
import { ImpactPathList } from '../shared/ImpactPathList'

/**
 * The lines a node deletion takes with it, each DRAWN as its path, and the entry listeners that close with them
 * (design 5.2). The same body serves the delete dialog, the remove-from-panel dialog and each node of the bulk
 * dialog, so the three can never describe the same deletion differently.
 */
export function ImpactBody({ impact, entryOf, serverName, mode }: {
  impact: Pick<NodeDeleteImpact, 'lines' | 'tunnels'>
  entryOf: (line: DeleteImpactLine) => string | undefined
  serverName: (panelId: number) => string
  mode: 'delete' | 'detach'
}) {
  const t = useNodesT()
  return (
    <>
      <div>
        <p className="mb-2 font-medium">{t(mode === 'delete' ? 'nodes.impact.lines' : 'nodes.impact.lines_remove', { count: impact.lines.length })}</p>
        <ImpactPathList lines={impact.lines} entryOf={entryOf} />
      </div>
      {impact.tunnels.length > 0 && (
        <div>
          <p className="mb-1 font-medium">{t('nodes.impact.listeners')}</p>
          <ul className="list-disc ps-5 text-muted-foreground" data-testid="impact-listeners">
            {impact.tunnels.map(x => (
              <li key={`${x.node_id}.${x.line_id}`}>
                {t('nodes.impact.listener_item', { server: serverName(x.entry_panel_id), port: x.port ?? 0, line: x.line_name })}
                {x.front_port ? ` · ${t('nodes.impact.listener_front', { port: x.front_port })}` : ''}
              </li>
            ))}
          </ul>
        </div>
      )}
    </>
  )
}
