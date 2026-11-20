import { useEffect, useMemo, useState } from 'react'
import { updateNodeMetadata, type LineVisibilityDraft } from '@/api/nodes'
import type { Node } from '@/api/types'
import { pushToast } from '@/components/feedback/toasts'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { useAllNodes } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'
import { nodeGroupsHint, useLineVisibility } from '../../useLineVisibility'
import { useApplyNode } from '../lines/useNodeDetail'
import { useNodesT } from '../shared/useNodesT'
import { GENERAL_ID, GeneralFields, generalProblems, type GeneralDraft, type GeneralField } from './GeneralFields'

const NONE: never[] = []
const draftOf = (n: Node): GeneralDraft => ({ display_name: n.display_name, server_address: n.server_address, region: n.region, tags: n.tags ?? [] })
const same = (a: GeneralDraft, b: GeneralDraft) => JSON.stringify(a) === JSON.stringify(b)

/**
 * General (design 4.6): what the node is called and who sees it. The save sends the four fields and the node's
 * current `flow` (the server assigns it on every metadata save, so omitting it would clear a VLESS node's flow)
 * and nothing else: no `relays`, so the lines and the direct line's switch stay as they are, no exit and no
 * `sort_order`, which only the order endpoint writes.
 */
export function GeneralTab({ node, onDirtyChange }: { node: Node; onDirtyChange: (dirty: boolean) => void }) {
  const t = useNodesT()
  const scope = useQueryScope()
  const apply = useApplyNode()
  const nodes = useAllNodes(scope)
  // null: untouched, so the form follows the node (a refetch must not look like an edit).
  const [edit, setEdit] = useState<GeneralDraft | null>(null)
  const base = useMemo(() => draftOf(node), [node])
  const draft = edit ?? base
  const dirty = edit !== null && !same(edit, base)
  const [touched, setTouched] = useState<ReadonlySet<GeneralField>>(new Set())
  const [attempted, setAttempted] = useState(false)
  const [pending, setPending] = useState(false)
  useEffect(() => { onDirtyChange(dirty) }, [dirty, onDirtyChange])
  useEffect(() => () => onDirtyChange(false), [onDirtyChange])

  const problems = generalProblems(draft)
  const error = (f: GeneralField) => (attempted || touched.has(f) ? problems[f] : undefined)
  const tags = useMemo(() => [...new Set((nodes.data ?? NONE).flatMap(n => n.tags ?? []))].sort((a, b) => a.localeCompare(b)), [nodes.data])

  // Which groups would receive the node with the unsaved values; its own lines are sent as saved (they are not edited here).
  const visibilityDraft = useMemo<LineVisibilityDraft>(() => ({
    server_address: draft.server_address, region: draft.region, tags: draft.tags,
    relays: (node.relays ?? []).map(r => ({ id: r.id, region: (r.region ?? '').trim(), tags: r.tags ?? [] })),
  }), [draft.server_address, draft.region, draft.tags, node.relays])
  const groups = nodeGroupsHint(useLineVisibility(node.id, visibilityDraft))

  async function save() {
    const bad = Object.keys(problems) as GeneralField[]
    if (bad.length > 0) {
      setAttempted(true)
      const first = (['display_name', 'server_address', 'region'] as GeneralField[]).find(f => bad.includes(f))
      if (first) setTimeout(() => document.getElementById(GENERAL_ID[first])?.focus(), 0)
      return
    }
    setPending(true)
    try {
      const saved = await updateNodeMetadata(node.id, { ...draft, flow: node.flow ?? '' })
      apply(saved)
      pushToast({ message: t('nodes.toast.saved'), severity: 'success' })
      setEdit(null); setTouched(new Set()); setAttempted(false)
    } catch { /* The request layer already surfaced the failure. */ } finally { setPending(false) }
  }

  return (
    <FormValidityProvider>
      <form className="flex max-w-2xl flex-col gap-5" onSubmit={e => { e.preventDefault(); void save() }} noValidate>
        <GeneralFields draft={draft} patch={p => setEdit({ ...draft, ...p })} error={error} blur={f => setTouched(s => (s.has(f) ? s : new Set(s).add(f)))}
          tagOptions={tags} visible={groups.now} removed={groups.removed} />
        {(node.relays ?? []).length > 0 && <p className="text-xs text-muted-foreground">{t('nodes.line_labels.node_change_hint')}</p>}
        <SaveBar dirty={dirty} pending={pending} onSave={() => void save()} onDiscard={() => { setEdit(null); setTouched(new Set()); setAttempted(false) }} />
      </form>
    </FormValidityProvider>
  )
}
