import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import type { Node } from '@/api/types'
import { pushToast } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { factorTextToMilli, milliToFactorText } from '@/utils/trafficFactor'
import { useNodesT } from '../shared/useNodesT'
import { saveLineChange } from './lineSave'
import { usePlanPresenter } from './PlanDialog'
import { useApplyNode } from './useNodeDetail'

/**
 * The node's own line (design 4.4.6): whether it is published, and its multiplier. Its address and port are the
 * node's, so they are shown, with where they are changed. The save sends the node's current lines unchanged,
 * because `hide_direct` is applied by the server only together with `relays`.
 */
export function DirectEditor({ node, onDirtyChange, onSaved }: { node: Node; onDirtyChange: (dirty: boolean) => void; onSaved: (r: { isNew: boolean }) => void }) {
  const t = useNodesT()
  const apply = useApplyNode()
  const { present, dialog } = usePlanPresenter()
  const [published, setPublished] = useState(!node.hide_direct)
  const [factor, setFactor] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const factorText = factor ?? milliToFactorText(node.traffic_factor_milli)
  const factorBad = factor !== null && factorTextToMilli(factor) === null
  const dirty = published !== !node.hide_direct || factor !== null
  useEffect(() => { onDirtyChange(dirty) }, [dirty, onDirtyChange])
  useEffect(() => () => onDirtyChange(false), [onDirtyChange])

  async function save() {
    if (factorBad) { document.getElementById('line-factor')?.focus(); return }
    setPending(true)
    try {
      const { saved } = await saveLineChange(node.id, { kind: 'direct', hidden: !published, ...(factor !== null ? { factor: { text: factor, milli: factorTextToMilli(factor) } } : {}) }, { present })
      apply(saved)
      pushToast({ message: t('nodes.line.toast.saved'), severity: 'success' })
      setFactor(null)
      onSaved({ isNew: false })
    } catch { /* The request layer already surfaced the failure. */ } finally { setPending(false) }
  }

  return (
    <FormValidityProvider>
      <form className="flex flex-col gap-5" onSubmit={e => { e.preventDefault(); void save() }} noValidate>
        <Field orientation="horizontal" label={t('nodes.line.publish_direct')} hint={t('nodes.line.publish_direct_hint')}>
          <Switch checked={published} onCheckedChange={setPublished} />
        </Field>
        <Field label={t('nodes.field.traffic_factor_direct')} hint={t('nodes.hint.traffic_factor')} error={factorBad ? t('nodes.validate.traffic_factor') : undefined}>
          <Input id="line-factor" inputMode="decimal" className="max-w-40 tabular-nums" value={factorText} onChange={e => setFactor(e.target.value)} />
        </Field>
        <div className="flex flex-col gap-1 text-sm">
          <span className="text-muted-foreground">{t('nodes.col.address')}</span>
          <span className="font-mono">{node.server_address}{node.desired_port ? `:${node.desired_port}` : ''}</span>
          <span className="text-xs text-muted-foreground">
            {t('nodes.line.direct_address_hint')}{' '}
            <Link to={`/admin/nodes/${node.id}`} className="underline underline-offset-2">{t('nodes.action.open_node')}</Link>
          </span>
        </div>
        <SaveBar dirty={dirty} pending={pending} saveLabel={t('nodes.line.action.save')} onSave={() => void save()}
          onDiscard={() => { setPublished(!node.hide_direct); setFactor(null) }} />
      </form>
      {dialog}
    </FormValidityProvider>
  )
}
