import { setNodeCertSource } from '@/api/certs'
import { updateInboundConfig, updateNodeMetadata } from '@/api/nodes'
import type { Node } from '@/api/types'
import type { PanelType } from '@/api/servers'
import { type InboundFormState } from './inboundModel'
import { canonicalProtocolName } from './inboundSettings'
import { inboundDocuments } from './inboundDocuments'

/** The parts of an inbound save that did not go through, with why: what the save bar names after a partial save. */
export interface PartialFailure { part: 'flow' | 'cert'; error: unknown }

/**
 * Saves an inbound (design 4.5): the configuration, then the node's VLESS flow when it changed, then the
 * certificate source binding. The first is the inbound itself and its failure rejects, nothing was saved. The other
 * two are separate writes that follow it; their failures are returned, not thrown, so the caller can say the
 * inbound IS saved and which part is not (they used to be swallowed after a toast).
 *
 * `flow` is a node property, not part of the inbound request: it goes through the metadata endpoint and only
 * `flow` is sent, because the server keeps every field that is empty or absent and assigns `flow` unconditionally.
 * No `sort_order`: an explicit value is a move through the order writer.
 */
export async function saveInbound(node: Node, f: InboundFormState, panelType: PanelType, json: boolean, only?: ReadonlySet<PartialFailure['part']>): Promise<PartialFailure[]> {
  const docs = inboundDocuments(f, panelType, json)
  const managed = f.tls_cert_mode === 'psp_managed' && f.cert_id > 0
  // A retry after a partial save sends only the parts that failed: every inbound request is pushed to the upstream server, so repeating it is not free.
  if (!only) await updateInboundConfig(node.id, {
    remark: f.display_name,
    enable: panelType === '3xui' ? f.enable : true,
    listen: f.listen,
    port: f.port,
    protocol: canonicalProtocolName(f.protocol),
    ...docs,
    allocate: '',
    cert_source: managed ? 'psp_managed' : undefined,
    cert_id: managed ? f.cert_id : undefined,
  })
  const failed: PartialFailure[] = []
  // Non-VLESS protocols clear it.
  const flow = f.protocol === 'vless' ? f.vless_flow.trim() : ''
  if ((!only || only.has('flow')) && flow !== (node.flow ?? '')) {
    try { await updateNodeMetadata(node.id, { flow }) } catch (error) { failed.push({ part: 'flow', error }) }
  }
  // The update request already asks the backend to inject a managed cert before the panel update; this call
  // persists the binding and is content-diff-gated, so it does not cause a second push. Switching away from
  // managed unbinds it so the renewal worker stops re-deploying; the manual certificate was pushed above.
  if (!only || only.has('cert')) try {
    if (managed) await setNodeCertSource(node.id, 'psp_managed', f.cert_id)
    else if (node.cert_source === 'psp_managed') await setNodeCertSource(node.id, 'manual', 0)
  } catch (error) { failed.push({ part: 'cert', error }) }
  return failed
}
