import { useEffect, useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { IconDelete, IconRandom } from '@/lib/icons'
import {
  createFront, suggestCamouflage, suggestLoopbackPort, updateFront,
  type Front, type FrontOptions, type FrontsView,
} from '@/api/fronts'
import { IconButton } from '@/components/common/IconButton'
import { InfoTip } from '@/components/common/HelpTip'
import { RelationGraph } from '@/components/diagram/RelationGraph'
import { pushSnack } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { FormSection } from '@/components/form/FormSection'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { frontKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { Notice } from '../Notice'
import { useAdminT } from '../useAdminT'
import {
  changedLinkIDs, draftIssues, draftOf, inputOf, leaving, linesFor, listenersFor, newRouteKey, newlyMoved, targetKey, type Draft, type DraftIssue,
} from './frontDraft'
import { frontErrorText, issueText } from './frontErrors'
import { frontGraph } from './frontGraph'
import { listenerLabel } from './listenerLabel'
import { useFrontNames } from './useFrontNames'

interface Props {
  panelId: number
  front: Front | null
  options: FrontOptions
  defaults: FrontsView['defaults']
  /** Back to the list, with nothing lost (after a save, or after the person confirmed leaving). */
  onDone: () => void
  /** Tells the host whether there are unsaved edits, for its own close guard. */
  onDirtyChange: (dirty: boolean) => void
}

// What is typed, without the parts that are not the admin's: the rows' React keys and the confirmation box.
const snapshot = (d: Draft) => JSON.stringify({ ...d, confirm: false, routes: d.routes.map(r => ({ names: r.names, target: r.target })) })

const ids = {
  port: 'front-port', host: 'front-default-host', defaultPort: 'front-default-port',
  listener: (n: number) => `front-listener-${n}`, loopback: (n: number) => `front-loopback-${n}`,
  names: (k: string) => `front-names-${k}`, target: (k: string) => `front-target-${k}`, confirm: 'front-confirm',
}

/** Which field an issue belongs to: the draft key that is touched when the person leaves it, and the control to focus. */
function whereIs(issue: DraftIssue, draft: Draft): { key: string; id: string } {
  const route = (n: number) => draft.routes[n - 1]?.key ?? ''
  switch (issue.code) {
    case 'port': return { key: 'port', id: ids.port }
    case 'loopback': case 'loopback_probe': return { key: `loopback:${issue.node_id}`, id: ids.loopback(issue.node_id) }
    case 'loopback_twice': {
      const dup = [...draft.listeners].reverse().find(l => Number(l.loopback) === issue.port)
      return { key: `loopback:${dup?.node_id ?? 0}`, id: ids.loopback(dup?.node_id ?? 0) }
    }
    case 'route_target': return { key: `target:${route(issue.route)}`, id: ids.target(route(issue.route)) }
    case 'route_names': case 'name_twice': return { key: `names:${route(issue.route)}`, id: ids.names(route(issue.route)) }
    case 'listener_unrouted': return { key: `listener:${issue.node_id}`, id: ids.listener(issue.node_id) }
    case 'default_host': return { key: 'host', id: ids.host }
    case 'default_port': return { key: 'defaultPort', id: ids.defaultPort }
  }
}

/**
 * One shared port, as a page inside the sheet (a long form, so not a dialog). Every value is the admin's: the
 * buttons only fill fields in. Rules the editor can check are shown beside the field once it has been left, and
 * all of them keep Save off and are named by the bar; the server repeats them and words its own refusals under the
 * form. Moving listeners that hold the port today needs the explicit confirmation, as before.
 */
export default function FrontEditor({ panelId, front, options, defaults, onDone, onDirtyChange }: Props) {
  const t = useAdminT()
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const names = useFrontNames(options)
  const initial = useMemo(() => draftOf(front, defaults.port), [front, defaults.port])
  const [draft, setDraft] = useState<Draft>(initial)
  const [touched, setTouched] = useState<ReadonlySet<string>>(new Set())
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)

  const dirty = snapshot(draft) !== snapshot(initial)
  // The host's close guard reads this; the route guard is here, where the draft is.
  useEffect(() => { onDirtyChange(dirty) }, [dirty, onDirtyChange])
  useLeaveGuard(dirty, {
    title: t('admin:servers.fronts.discard_title'), message: t('admin:servers.fronts.discard_message'),
    confirmText: t('admin:servers.fronts.discard_confirm'), destructive: true, initialFocus: 'cancel',
  }, () => true)

  const port = Number(draft.port)
  const listeners = listenersFor(options, port, draft)
  const lines = linesFor(options, port)
  const touch = (key: string) => setTouched(s => (s.has(key) ? s : new Set(s).add(key)))
  const patch = (p: Partial<Draft>) => setDraft(d => ({ ...d, ...p }))

  // What a route can point at: the listeners the draft moves behind the front and the relay lines entering on this port.
  const targets = useMemo(() => {
    const out: { key: string; label: string; names: string[] }[] = []
    for (const l of draft.listeners) {
      const o = options.listeners.find(x => x.node_id === l.node_id)
      out.push({ key: targetKey.listener(l.node_id), label: o?.parent_node_id ? listenerLabel(t, '', o) : t('admin:servers.fronts.target.listener', { name: o?.name ?? `#${l.node_id}` }), names: o?.server_names ?? [] })
    }
    for (const line of lines) {
      out.push({ key: targetKey.forward(line.node_id, line.line_id), label: t('admin:servers.fronts.target.forward', { node: line.node_name, line: line.line_name }), names: line.server_names })
    }
    for (const link of options.links ?? []) {
      if (link.front_id && link.front_id !== front?.id) continue
      out.push({ key: targetKey.link(link.link_id), label: t('admin:servers.fronts.target.link', { link: link.link_id, entry: link.entry_panel_id, port: link.port }), names: [link.server_name] })
    }
    return out
  }, [draft.listeners, lines, options.listeners, options.links, front?.id, t])
  const labelOf = (key: string) => targets.find(x => x.key === key)?.label ?? ''

  const issues = draftIssues(draft, options, { low: defaults.probe_low, high: defaults.probe_high }, labelOf)
  const moved = newlyMoved(draft, front)
  const away = leaving(draft, front)
  const changedLinks = changedLinkIDs(draft, front)
  const needsConfirm = moved.length + away.listeners.length + away.lines.length + changedLinks.length > 0
  const located = issues.map(issue => ({ issue, ...whereIs(issue, draft) }))
  const shown = (key: string) => {
    if (!touched.has(key)) return undefined
    const hit = located.find(l => l.key === key)
    return hit ? issueText(t, hit.issue, names.nodeName) : undefined
  }

  async function toggleListener(nodeId: number, on: boolean) {
    if (!on) {
      setDraft(d => ({ ...d, listeners: d.listeners.filter(l => l.node_id !== nodeId), routes: d.routes.filter(r => r.target !== targetKey.listener(nodeId)) }))
      return
    }
    const option = options.listeners.find(l => l.node_id === nodeId)
    let loopback = ''
    try { loopback = String(await suggestLoopbackPort(panelId)) } catch { /* the field stays empty to be typed */ }
    setDraft(d => ({
      ...d,
      listeners: [...d.listeners, { node_id: nodeId, loopback }],
      routes: [...d.routes, { key: newRouteKey(), names: (option?.server_names ?? []).join(', '), target: targetKey.listener(nodeId) }],
    }))
  }

  async function randomLoopback(nodeId: number) {
    try {
      const p = await suggestLoopbackPort(panelId)
      setDraft(d => ({ ...d, listeners: d.listeners.map(l => l.node_id === nodeId ? { ...l, loopback: String(p) } : l) }))
    } catch (err) { setError(frontErrorText(t, err)) }
  }

  async function randomSite() {
    try {
      const s = await suggestCamouflage(panelId)
      patch({ host: s.host, defaultPort: String(s.port) })
    } catch (err) { setError(frontErrorText(t, err)) }
  }

  function setRouteTarget(key: string, target: string) {
    setDraft(d => ({
      ...d,
      routes: d.routes.map(r => {
        if (r.key !== key) return r
        const filled = r.names.trim() === '' ? (targets.find(x => x.key === target)?.names ?? []).join(', ') : r.names
        return { ...r, target, names: filled }
      }),
    }))
  }

  async function save() {
    setPending(true)
    setError('')
    try {
      const input = inputOf(draft)
      const saved = front ? await updateFront(panelId, front.id, input) : await createFront(panelId, input)
      await queryClient.invalidateQueries({ queryKey: frontKeys.all(scope) })
      pushSnack(t('admin:servers.fronts.saved'), 'success')
      if (saved.warnings?.includes('default_unresolved')) pushSnack(t('admin:servers.fronts.warning.default_unresolved'), 'warning')
      onDirtyChange(false)
      onDone()
    } catch (err) {
      setError(frontErrorText(t, err))
    } finally {
      setPending(false)
    }
  }

  // The draft as the server would see it, drawn: only what is complete enough to be a route.
  const previewModel = useMemo(() => {
    if (!Number.isInteger(port) || port < 1) return null
    const input = inputOf(draft)
    const routes = input.routes.filter((r, i) => draft.routes[i].target !== '' && r.server_names.length > 0)
    if (routes.length === 0) return null
    const f: Front = { id: front?.id ?? 0, port, listeners: input.listeners, routes, default: { kind: 'external', host: input.default.host, port: input.default.port } }
    return frontGraph([f], new Set(), {
      columns: [t('admin:servers.fronts.graph.col_port'), t('admin:servers.fronts.graph.col_sni'), t('admin:servers.fronts.graph.col_target')],
      port: p => t('admin:servers.fronts.front_title', { port: p }),
      routes: count => t('admin:servers.fronts.graph.routes', { count }),
      other: t('admin:servers.fronts.graph.other'),
      listener: (id, loopback) => ({ label: names.nodeName(id), sublabel: loopback ? t('admin:servers.fronts.graph.loopback', { port: loopback }) : undefined }),
      forward: (node, line) => t('admin:servers.fronts.target.forward_short', { node: names.nodeName(node), line }),
      link: link => t('admin:servers.fronts.target.link_short', { link }),
      external: (host, p) => (host ? `${host}:${p}` : t('admin:servers.fronts.graph.no_default')),
    })
  }, [draft, front?.id, names, port, t])

  const confirmError = needsConfirm && !draft.confirm
  const barErrors = [
    ...located.map(l => ({ message: issueText(t, l.issue, names.nodeName), id: l.id })),
    ...(confirmError ? [{ message: t('admin:servers.fronts.confirm_needed'), id: ids.confirm }] : []),
  ]

  return (
    <FormValidityProvider>
      <div className="flex flex-col gap-6">
        <FormSection title={t('admin:servers.fronts.port')} actions={<InfoTip text={t('admin:servers.fronts.help.what')} />}>
          <Field label={t('admin:servers.fronts.port')} hint={t('admin:servers.fronts.port_hint')} error={shown('port')} className="max-w-56">
            <Input id={ids.port} inputMode="numeric" value={draft.port} onChange={e => patch({ port: e.target.value })} onBlur={() => touch('port')} />
          </Field>
        </FormSection>

        <FormSection title={t('admin:servers.fronts.listeners')} description={t('admin:servers.fronts.help.listeners')}>
          {listeners.length === 0 && <p className="text-sm text-muted-foreground">{t('admin:servers.fronts.no_listeners')}</p>}
          <div className="flex flex-col gap-3">
            {listeners.map(l => {
              const chosen = draft.listeners.find(x => x.node_id === l.node_id)
              const name = listenerLabel(t, l.name, l)
              return (
                <div key={l.node_id} className="flex flex-wrap items-start gap-x-4 gap-y-2">
                  <div className="flex min-w-60 flex-1 flex-col gap-0.5">
                    <label className="flex items-start gap-2 text-sm">
                      <Checkbox id={ids.listener(l.node_id)} className="mt-0.5" checked={!!chosen} disabled={!l.frontable}
                        onCheckedChange={c => { void toggleListener(l.node_id, c === true); touch(`listener:${l.node_id}`) }} />
                      <span>{`${name} (${l.protocol}${l.security ? `+${l.security}` : ''}, :${l.port})`}</span>
                    </label>
                    {!l.frontable && <p className="pl-6 text-xs text-warning-ink">{t(`admin:servers.fronts.refusal.${l.reason ?? 'no_sni'}`)}</p>}
                    {shown(`listener:${l.node_id}`) && <p className="pl-6 text-xs text-destructive">{shown(`listener:${l.node_id}`)}</p>}
                  </div>
                  {chosen && (
                    <div className="flex items-end gap-2">
                      <Field label={t('admin:servers.fronts.loopback')} error={shown(`loopback:${l.node_id}`)} className="w-40">
                        <Input id={ids.loopback(l.node_id)} inputMode="numeric" aria-label={t('admin:servers.fronts.loopback_of', { name })} value={chosen.loopback}
                          onChange={e => setDraft(d => ({ ...d, listeners: d.listeners.map(x => x.node_id === l.node_id ? { ...x, loopback: e.target.value } : x) }))}
                          onBlur={() => touch(`loopback:${l.node_id}`)} />
                      </Field>
                      <Button variant="outline" className="mb-4" onClick={() => void randomLoopback(l.node_id)}><IconRandom />{t('admin:servers.fronts.random')}</Button>
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        </FormSection>

        <FormSection title={t('admin:servers.fronts.routes')} description={t('admin:servers.fronts.help.routes')}>
          <div className="flex flex-col gap-3">
            {draft.routes.map((r, index) => (
              <div key={r.key} className="flex flex-wrap items-start gap-x-3 gap-y-1">
                <Field label={t('admin:servers.fronts.names')} hint={t('admin:servers.fronts.names_hint')} error={shown(`names:${r.key}`)} className="min-w-56 flex-[2]">
                  <Input id={ids.names(r.key)} aria-label={t('admin:servers.fronts.names_of', { route: index + 1 })} value={r.names}
                    onChange={e => setDraft(d => ({ ...d, routes: d.routes.map(x => x.key === r.key ? { ...x, names: e.target.value } : x) }))}
                    onBlur={() => touch(`names:${r.key}`)} />
                </Field>
                <Field label={t('admin:servers.fronts.goes_to')} error={shown(`target:${r.key}`)} className="min-w-56 flex-[2]">
                  <NativeSelect id={ids.target(r.key)} aria-label={t('admin:servers.fronts.target_of', { route: index + 1 })} value={r.target}
                    onChange={e => { setRouteTarget(r.key, e.target.value); touch(`target:${r.key}`) }}>
                    <option value="">{t('admin:servers.fronts.choose_target')}</option>
                    {targets.map(x => <option key={x.key} value={x.key}>{x.label}</option>)}
                  </NativeSelect>
                </Field>
                <IconButton label={t('admin:servers.fronts.remove_route')} className="mt-6" onClick={() => setDraft(d => ({ ...d, routes: d.routes.filter(x => x.key !== r.key) }))}>
                  <IconDelete />
                </IconButton>
              </div>
            ))}
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <Button variant="outline" size="sm" disabled={targets.length === 0}
              onClick={() => setDraft(d => ({ ...d, routes: [...d.routes, { key: newRouteKey(), names: '', target: '' }] }))}>
              {t('admin:servers.fronts.add_route')}
            </Button>
            {targets.length === 0 && <span className="text-xs text-muted-foreground">{t('admin:servers.fronts.no_targets')}</span>}
          </div>
        </FormSection>

        <FormSection title={t('admin:servers.fronts.default')} description={t('admin:servers.fronts.help.default')}>
          <div className="flex flex-wrap items-start gap-x-3 gap-y-1">
            <Field label={t('admin:servers.fronts.default_host')} hint={t('admin:servers.fronts.default_host_hint')} error={shown('host')} className="min-w-64 flex-1">
              <Input id={ids.host} value={draft.host} onChange={e => patch({ host: e.target.value })} onBlur={() => touch('host')} />
            </Field>
            <Field label={t('admin:servers.fronts.default_port')} error={shown('defaultPort')} className="w-28">
              <Input id={ids.defaultPort} inputMode="numeric" value={draft.defaultPort} onChange={e => patch({ defaultPort: e.target.value })} onBlur={() => touch('defaultPort')} />
            </Field>
            <Button variant="outline" className="mt-6" onClick={() => void randomSite()}><IconRandom />{t('admin:servers.fronts.random')}</Button>
          </div>
        </FormSection>

        {previewModel && (
          <section aria-label={t('admin:servers.fronts.graph.preview')} className="flex flex-col gap-2">
            <h3 className="text-sm font-medium">{t('admin:servers.fronts.graph.preview')}</h3>
            <RelationGraph columns={previewModel.columns} edges={previewModel.edges} maxHeight="16rem" />
          </section>
        )}

        {needsConfirm && (
          <Notice>
            <p className="mb-2">
              {[
                moved.length > 0 && t('admin:servers.fronts.confirm_text', { nodes: moved.map(names.nodeName).join(', '), port: draft.port }),
                away.listeners.length > 0 && t('admin:servers.fronts.confirm_leaving_listeners', { nodes: away.listeners.map(names.nodeName).join(', ') }),
                away.lines.length > 0 && t('admin:servers.fronts.confirm_leaving_lines', { count: away.lines.length }),
                changedLinks.length > 0 && t('admin:servers.fronts.confirm_tunnels', { tunnels: changedLinks.join(', ') }),
              ].filter(Boolean).join(' ')}
            </p>
            <label className="flex items-start gap-2 font-medium">
              <Checkbox id={ids.confirm} className="mt-0.5" checked={draft.confirm} onCheckedChange={c => patch({ confirm: c === true })} />
              <span>{t('admin:servers.fronts.confirm_label')}</span>
            </label>
          </Notice>
        )}
        {error && <Notice tone="danger" role="alert">{error}</Notice>}
      </div>
      <SaveBar dirty={dirty} pending={pending} errors={barErrors} onSave={() => void save()} onDiscard={() => { setDraft(initial); setTouched(new Set()); setError('') }} className="mt-6" />
    </FormValidityProvider>
  )
}
