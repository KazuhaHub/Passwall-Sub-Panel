import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { FormDialog } from '@/components/data/FormDialog'
import { Field } from '@/components/form/Field'
import { NativeSelect } from '@/components/ui/native-select'
import type { Block } from '@/utils/ruleset/blocks'
import { TargetSelect } from './TargetSelect'
import type { TargetChoices } from './targets'

/** What the dialog decided: a group to move into, a position, or a new group (with its target) before a position. */
export type MoveChoice =
  | { kind: 'after'; block: number }
  | { kind: 'top' }
  | { kind: 'into'; block: number }
  | { kind: 'new'; before: number; target: string }

const label = (b: Block, text: string) => `${b.index + 1} · ${text} (${b.count.toLocaleString()})`

/**
 * "Move to…" (design 3.7), the way to reorder or regroup that needs no dragging and the only one on a phone. For a
 * group: place it after another or at the top. For rules: into a group, or into a new group beside the current one, and
 * it says where the rules will then go, since that is what changes.
 */
export function MoveToDialog({ mode, blocks, from, choices, count, onSubmit, onClose }: {
  mode: 'block' | 'rows'
  blocks: readonly Block[]
  /** The group being moved, or the group the rows are in. */
  from: number
  choices: TargetChoices
  /** How many rules are moving (rows mode). */
  count?: number
  onSubmit: (choice: MoveChoice) => void
  onClose: () => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const textOf = (b: Block) => (b.kind === 'text' ? t('admin:rules.blocks.text_block', { count: b.count }) : b.target || '—')
  const others = blocks.filter(b => b.index !== from)
  const [place, setPlace] = useState<string>(mode === 'block' ? (from === 0 ? 'top' : `after:${from - 1}`) : `into:${others[0]?.index ?? from}`)
  const [target, setTarget] = useState('')
  const isNew = place === 'new_before' || place === 'new_after'
  const fromBlock = blocks[from]

  const choice = (): MoveChoice | null => {
    if (place === 'top') return { kind: 'top' }
    if (place.startsWith('after:')) return { kind: 'after', block: Number(place.slice(6)) }
    if (place.startsWith('into:')) return { kind: 'into', block: Number(place.slice(5)) }
    if (isNew) return target ? { kind: 'new', before: place === 'new_before' ? from : from + 1, target } : null
    return null
  }
  const dest = place.startsWith('into:') ? blocks[Number(place.slice(5))] : null

  return (
    <FormDialog
      title={t('admin:rules.move.title')}
      description={mode === 'block' ? textOf(fromBlock) : t('admin:rules.matchers.selected_count', { count: count ?? 0 })}
      dirty
      pending={false}
      submitLabel={t('admin:rules.move.submit')}
      onClose={onClose}
      onSubmit={() => { const c = choice(); if (c) onSubmit(c) }}
    >
      <Field label={mode === 'block' ? t('admin:rules.move.after') : t('admin:rules.move.into')}>
        <NativeSelect value={place} onChange={e => setPlace(e.target.value)}>
          {mode === 'block' ? (
            <>
              <option value="top">{t('admin:rules.move.first')}</option>
              {blocks.filter(b => b.index !== from && b.index !== from - 1).map(b => <option key={b.index} value={`after:${b.index}`}>{label(b, textOf(b))}</option>)}
            </>
          ) : (
            <>
              {others.map(b => b.kind === 'rules' && <option key={b.index} value={`into:${b.index}`}>{label(b, textOf(b))}</option>)}
              <option value="new_before">{t('admin:rules.move.new_before')}</option>
              <option value="new_after">{t('admin:rules.move.new_after')}</option>
            </>
          )}
        </NativeSelect>
      </Field>
      {isNew && <TargetSelect value={target} choices={choices} label={t('admin:rules.blocks.target')} onChange={setTarget} />}
      {mode === 'rows' && (dest || (isNew && target)) && fromBlock && (
        <p className="text-sm text-muted-foreground">
          {t('admin:rules.move.changes_target', { from: fromBlock.target, to: dest ? dest.target : target })}
        </p>
      )}
    </FormDialog>
  )
}
