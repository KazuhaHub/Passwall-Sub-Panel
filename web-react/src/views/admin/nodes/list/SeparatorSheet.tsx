import { useMemo, useState } from 'react'
import { createSeparator, previewSeparatorGroups, updateSeparator, type Separator, type SeparatorMode, type SeparatorGroupsPreview } from '@/api/nodes'
import { Button } from '@/components/ui/button'
import { Combobox, ComboboxChip, ComboboxChips, ComboboxChipsInput, ComboboxContent, ComboboxEmpty, ComboboxItem, ComboboxList, ComboboxValue, useComboboxAnchor } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { AsyncButton } from '@/components/common/AsyncButton'
import { DetailSheet } from '@/components/data/DetailSheet'
import { Field } from '@/components/form/Field'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { confirm } from '@/components/ConfirmHost'
import { pushToast } from '@/components/feedback/toasts'
import { previewHint, useDraftPreview } from '@/hooks/useDraftPreview'
import { discardChangesConfirm } from '../../accessControl/confirmCopy'
import { useNodesT } from '../shared/useNodesT'

interface NodeOption { id: number; name: string }

interface Draft { name: string; enabled: boolean; mode: SeparatorMode; nodes: NodeOption[] }

/**
 * The separator sheet (design 2.6): create from the header button, edit from the row. The same fields as the old
 * dialog, in a sheet so the list stays in view. A new separator is created at the END of the order (the form has
 * no position, as before); the caller highlights it so it is found and moved with drag or Move to….
 */
export function SeparatorSheet({ separatorId, separators, nodes, onClose, onSaved }: {
  /** 'new', or the id being edited; null closed. */
  separatorId: number | 'new' | null
  separators: readonly Separator[]
  nodes: readonly NodeOption[]
  onClose: () => void
  onSaved: (id: number | null) => void
}) {
  const open = separatorId !== null
  const existing = typeof separatorId === 'number' ? separators.find(s => s.id === separatorId) : undefined
  // The draft starts from the saved separator; keyed on the id so opening another one starts over.
  return (
    <SeparatorForm key={String(separatorId)} open={open} existing={existing} nodes={nodes} isNew={separatorId === 'new'} onClose={onClose} onSaved={onSaved} />
  )
}

function SeparatorForm({ open, existing, isNew, nodes, onClose, onSaved }: {
  open: boolean; existing?: Separator; isNew: boolean; nodes: readonly NodeOption[]; onClose: () => void; onSaved: (id: number | null) => void
}) {
  const t = useNodesT()
  const initial = useMemo<Draft>(() => ({
    name: existing?.display_name ?? '', enabled: existing?.enabled ?? true, mode: existing?.mode ?? 'global',
    nodes: nodes.filter(n => existing?.node_ids.includes(n.id)),
  // The draft is seeded once per opening.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [existing?.id])
  const [draft, setDraft] = useState<Draft>(initial)
  const [touched, setTouched] = useState(false)
  const anchor = useComboboxAnchor()
  const dirty = JSON.stringify(draft) !== JSON.stringify(initial)
  const nameError = touched && !draft.name.trim() ? t('nodes.separator.name_required') : undefined
  const nodeIds = draft.nodes.map(n => n.id)

  // ADR 0042 section 4: which groups would show a node-bound separator.
  const preview = useDraftPreview<SeparatorGroupsPreview>(open && draft.mode === 'node_bound' ? JSON.stringify(nodeIds) : null,
    signal => previewSeparatorGroups('node_bound', nodeIds, signal))
  const hint = previewHint(preview, d => { const names = new Map(d.groups.map(g => [g.id, g.name])); return d.visible.map(id => names.get(id) ?? `#${id}`) })

  const close = () => {
    if (!dirty) { onClose(); return }
    void confirm(discardChangesConfirm(t)).then(ok => { if (ok) onClose() })
  }
  const save = async () => {
    setTouched(true)
    if (!draft.name.trim()) return
    const payload = {
      display_name: draft.name.trim(), enabled: draft.enabled, mode: draft.mode,
      // node_ids only means something under node_bound; sending [] in global mode keeps the payload clean.
      node_ids: draft.mode === 'node_bound' ? nodeIds : [],
    }
    try {
      const saved = existing ? await updateSeparator(existing.id, payload) : await createSeparator(payload)
      pushToast({ message: t(existing ? 'nodes.toast.separator_updated' : 'nodes.toast.separator_created'), severity: 'success' })
      onSaved(saved?.id ?? existing?.id ?? null)
    } catch { /* the request layer already surfaced it */ }
  }

  return (
    <DetailSheet
      open={open}
      onClose={close}
      title={t(isNew ? 'nodes.separator.create_title' : 'nodes.separator.edit_title')}
      description={t('nodes.separator.hint')}
      footer={<>
        <Button variant="outline" onClick={close}>{t('common:actions.cancel')}</Button>
        <AsyncButton onClick={save}>{t('common:actions.save')}</AsyncButton>
      </>}
    >
      <div className="flex flex-col gap-4">
        <Field label={t('nodes.separator.name')} required error={nameError}>
          <Input value={draft.name} placeholder="---- Taiwan HiNet ----" onChange={e => setDraft(d => ({ ...d, name: e.target.value }))} onBlur={() => setTouched(true)} />
        </Field>
        <Field label={t('nodes.separator.mode')} hint={t(draft.mode === 'global' ? 'nodes.separator.mode_hint_global' : 'nodes.separator.mode_hint_node_bound')}>
          {p => (
            <SegmentedControl aria-labelledby={p.labelId} value={draft.mode} onChange={mode => setDraft(d => ({ ...d, mode }))}
              options={[{ value: 'global', label: t('nodes.separator.global') }, { value: 'node_bound', label: t('nodes.separator.node_bound_option') }]} />
          )}
        </Field>
        {draft.mode === 'node_bound' && (
          <>
            <Field label={t('nodes.separator.nodes')} hint={t('nodes.separator.nodes_hint')}>
              {p => (
                <Combobox multiple items={nodes} value={draft.nodes} onValueChange={v => setDraft(d => ({ ...d, nodes: v as NodeOption[] }))}
                  itemToStringLabel={(n: NodeOption) => n.name} isItemEqualToValue={(a: NodeOption, b: NodeOption) => a.id === b.id}>
                  <ComboboxChips ref={anchor}>
                    <ComboboxValue>
                      {(values: NodeOption[]) => (
                        <>
                          {values.map(n => <ComboboxChip key={n.id}>{n.name}</ComboboxChip>)}
                          <ComboboxChipsInput id={p.id} aria-describedby={p['aria-describedby']} />
                        </>
                      )}
                    </ComboboxValue>
                  </ComboboxChips>
                  <ComboboxContent anchor={anchor}>
                    <ComboboxEmpty>{t('nodes.order.move_none')}</ComboboxEmpty>
                    <ComboboxList>{(n: NodeOption) => <ComboboxItem key={n.id} value={n}>{n.name}</ComboboxItem>}</ComboboxList>
                  </ComboboxContent>
                </Combobox>
              )}
            </Field>
            {hint && hint.state !== 'loading' && (
              <p data-testid="separator-groups" className={hint.state === 'ready' && hint.items.length === 0 ? 'text-xs text-destructive' : 'text-xs text-muted-foreground'}>
                {hint.state === 'error' ? t('nodes.separator_preview.unknown')
                  : hint.items.length === 0 ? t('nodes.separator_preview.none')
                    : t('nodes.separator_preview.visible', { groups: hint.items.join(t('nodes.line_labels.group_separator')) })}
              </p>
            )}
          </>
        )}
        <Field label={t('nodes.separator.enabled')} orientation="horizontal">
          <Switch checked={draft.enabled} onCheckedChange={enabled => setDraft(d => ({ ...d, enabled }))} />
        </Field>
      </div>
    </DetailSheet>
  )
}
