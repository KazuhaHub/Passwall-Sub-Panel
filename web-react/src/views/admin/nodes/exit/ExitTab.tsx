import { useCallback, useEffect, useMemo, useState } from 'react'
import { getTransitLink, updateNodeMetadata, updateTransitLink, type TransitLink } from '@/api/nodes'
import type { Node } from '@/api/types'
import { LoadingState } from '@/components/feedback/LoadingState'
import { pushToast } from '@/components/feedback/toasts'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { useAllNodes, useAllServers } from '@/query/nodes'
import { useNodeRoutes } from '@/query/routes'
import { useQueryScope } from '@/query/useQueryScope'
import { useApplyNode } from '../lines/useNodeDetail'
import { reasonOf } from '../shared/reasonOf'
import { useNodesT } from '../shared/useNodesT'
import { DefaultExit, type ExitMode } from './DefaultExit'
import { RoutesSection } from './routes/RoutesSection'
import { useEgressReady } from './useEgressReady'

const NONE: never[] = []

/**
 * Exit (design 4.3): where traffic leaves. The default exit saves with the tab's bar; the routes by destination
 * are a different resource with their own dry run and their own Save, so they sit below and report only whether
 * they have unsaved edits.
 *
 * The default exit's save is two writes, in this order: the tunnel's settings (when they were edited), then the
 * node's exit. They are not atomic. When the second fails after the first succeeded the bar says which part is
 * already saved, and the tunnel's form stops being dirty, so a retry sends only what is left.
 */
export function ExitTab({ node, onDirtyChange, routesOnly }: {
  node: Node
  onDirtyChange: (dirty: boolean) => void
  /** A tunnel line's listener: its exit follows the line (see design 4.7), only its own routes by destination are edited here. */
  routesOnly?: boolean
}) {
  const t = useNodesT()
  const scope = useQueryScope()
  const apply = useApplyNode()
  const servers = useAllServers(scope)
  const nodes = useAllNodes(scope)
  const list = servers.data ?? NONE
  const landing = list.find(s => s.id === node.panel_id)
  const native = landing?.panel_type === 'psp'
  const exits = useMemo(() => list.filter(s => s.panel_type === 'psp' && s.id !== node.panel_id), [list, node.panel_id])
  const savedExit = node.egress_panel_id ?? 0
  useEgressReady(node)
  const routeCount = useNodeRoutes(scope, node.id, !!routesOnly).data?.routes.length

  // null: untouched, so the choice follows the node (a refetch must not look like an edit).
  const [edit, setEdit] = useState<{ mode: ExitMode; exitId: number } | null>(null)
  const mode: ExitMode = edit?.mode ?? (savedExit > 0 ? 'remote' : 'local')
  const exitId = edit ? (edit.mode === 'remote' ? edit.exitId : 0) : savedExit
  const target = mode === 'local' ? 0 : exitId

  // The tunnel between this node's server and the chosen exit; its settings are shared by every node of the pair.
  const [link, setLink] = useState<TransitLink | null>(null)
  const [linkDirty, setLinkDirty] = useState(false)
  const [linkError, setLinkError] = useState(false)
  useEffect(() => {
    if (target <= 0) { setLink(null); setLinkDirty(false); setLinkError(false); return }
    let cancelled = false
    setLinkError(false); setLink(null)
    getTransitLink(node.panel_id, target)
      .then(l => { if (!cancelled) { setLink(l); setLinkDirty(false) } })
      .catch(() => { if (!cancelled) setLinkError(true) })
    return () => { cancelled = true }
  }, [node.panel_id, target])

  const [attempted, setAttempted] = useState(false)
  const [pending, setPending] = useState(false)
  const [problem, setProblem] = useState('')
  const [routesDirty, setRoutesDirty] = useState(false)
  const dirty = target !== savedExit || (mode === 'remote' && savedExit === 0 && edit !== null) || linkDirty
  useEffect(() => { onDirtyChange(dirty || routesDirty) }, [dirty, routesDirty, onDirtyChange])
  useEffect(() => () => onDirtyChange(false), [onDirtyChange])
  const onRoutesDirty = useCallback((d: boolean) => setRoutesDirty(d), [])

  const needsServer = mode === 'remote' && exitId === 0
  const defaultExitNodesOf = (exit: number) => {
    const names = (nodes.data ?? NONE).filter(n => n.panel_id === node.panel_id && (n.egress_panel_id ?? 0) === exit).map(n => n.display_name)
    // This node counts even when the list's copy is older.
    if (savedExit === exit && !(nodes.data ?? NONE).some(n => n.id === node.id)) names.push(node.display_name)
    return names
  }

  async function save() {
    if (needsServer) { setAttempted(true); setTimeout(() => document.getElementById('exit-server')?.focus(), 0); return }
    setPending(true); setProblem('')
    let linkSaved = false
    try {
      if (target > 0 && link && linkDirty) {
        const reality = link.protocol === 'vless_reality'
        const saved = await updateTransitLink(node.panel_id, target, {
          protocol: link.protocol, exit_address: link.exit_address.trim(),
          reality_server_name: reality ? link.reality_server_name.trim() : undefined,
          reality_target: reality ? link.reality_target.trim() : undefined,
        })
        setLink(saved); setLinkDirty(false); linkSaved = true
      }
      try {
        const saved = await updateNodeMetadata(node.id, { egress_panel_id: target, flow: node.flow ?? '' })
        apply(saved)
        pushToast({ message: t('nodes.toast.saved'), severity: 'success' })
        setEdit(null); setAttempted(false)
      } catch (err) {
        if (linkSaved) setProblem(t('nodes.exit.partial_saved', { reason: reasonOf(err) }))
        throw err
      }
    } catch { /* The request layer already surfaced the failure; what was saved is named in the bar. */ } finally { setPending(false) }
  }

  // Until the servers are known the cards would read as "not a Passwall Node server" for a moment.
  if (servers.isPending) return <LoadingState />
  return (
    <div className="flex max-w-3xl flex-col gap-8">
      {routesOnly && <p className="text-sm text-muted-foreground">{t('nodes.tunnel.routes_note', { count: routeCount ?? 0 })}</p>}
      {/* The routes are their own resource with their own Save: their fields must not hold this bar's Save back. */}
      {!routesOnly && <FormValidityProvider>
        <form className="flex flex-col gap-5" onSubmit={e => { e.preventDefault(); void save() }} noValidate>
          <DefaultExit node={node} exits={exits} native={native} mode={mode} exitId={exitId} savedExit={savedExit} ready={!!node.egress_ready}
            onMode={m => { setProblem(''); setEdit({ mode: m, exitId: m === 'remote' ? (exitId || savedExit) : 0 }) }}
            onExit={id => { setProblem(''); setEdit({ mode: 'remote', exitId: id }) }}
            error={attempted && needsServer ? t('nodes.exit.pick_server_error') : undefined}
            link={link} linkError={linkError} onLink={p => { setLink(l => (l ? { ...l, ...p } : l)); setLinkDirty(true); setProblem('') }} />
          <SaveBar dirty={dirty || problem !== ''} pending={pending} errors={problem ? [{ message: problem, blocking: false }] : []} onSave={() => void save()}
            onDiscard={() => { setEdit(null); setAttempted(false); setProblem(''); setLinkDirty(false); if (target > 0) getTransitLink(node.panel_id, target).then(setLink).catch(() => {}) }} />
        </form>
      </FormValidityProvider>}
      {native && (
        <RoutesSection nodeId={node.id} entryPanelId={node.panel_id} servers={exits.map(s => ({ id: s.id, name: s.name }))} pendingExit={target}
          defaultExitNodesOf={defaultExitNodesOf} onDirtyChange={onRoutesDirty} />
      )}
    </div>
  )
}
