import { useMemo } from 'react'
import type { Server } from '@/api/servers'
import type { Node } from '@/api/types'
import { FormSection } from '@/components/form/FormSection'
import { DefaultExit } from '../exit/DefaultExit'
import { RoutesSection } from '../exit/routes/RoutesSection'
import type { DraftRoute } from '../exit/routes/routeDraft'
import { useNodesT } from '../shared/useNodesT'

const NOOP = () => {}

/**
 * Step 3: the default exit and the routes by destination, drawn by the SAME sections as the node page's Exit tab,
 * in setup mode: nothing is read or saved here, the rows live in the wizard and go out with the plan. The tunnel's
 * own settings are not shown, because the pair has none until the node exists.
 */
export function ExitStep({ name, landing, native, servers, egress, onEgress, remote, onRemote, routes, onRoutes }: {
  name: string
  landing: Server
  native: boolean
  /** Every Passwall Node server other than the landing's own. */
  servers: readonly Server[]
  egress: number
  onEgress: (id: number) => void
  /** "Another server" is chosen; which one may still be empty (the wizard will not go on until it is picked). */
  remote: boolean
  onRemote: (remote: boolean) => void
  routes: DraftRoute[]
  onRoutes: React.Dispatch<React.SetStateAction<DraftRoute[]>>
}) {
  const t = useNodesT()
  // The picture and names only read the node's name and server; there is no saved node yet.
  const node = useMemo(() => ({ display_name: name, panel_id: landing.id }) as Node, [name, landing.id])
  return (
    <div className="flex flex-col gap-6">
      <DefaultExit node={node} exits={servers} native={native} mode={remote ? 'remote' : 'local'} exitId={egress} savedExit={0} ready={false}
        onMode={m => { onRemote(m === 'remote'); if (m === 'local') onEgress(0) }} onExit={onEgress} link={null} linkError={false} onLink={NOOP} />
      {native && (
        <FormSection title={t('nodes.routes.title')} advanced sectionKey="create-routes">
          <RoutesSection nodeId={0} entryPanelId={landing.id} pendingExit={egress} setup={{ rows: routes, setRows: onRoutes }} servers={servers.map(s => ({ id: s.id, name: s.name }))} />
        </FormSection>
      )}
    </div>
  )
}
