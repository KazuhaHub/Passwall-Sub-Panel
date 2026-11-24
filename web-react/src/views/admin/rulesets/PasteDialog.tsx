import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { FormDialog } from '@/components/data/FormDialog'
import { Field } from '@/components/form/Field'
import { NativeSelect } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { Alert, AlertDescription } from '@/components/ui/alert'
import type { Block } from '@/utils/ruleset/blocks'
import { reviewPaste, scanPaste, type BareDomainType, type PasteEntry, type PasteTarget } from '@/utils/ruleset/detect'
import type { Doc } from '@/utils/ruleset/parse'
import { TargetSelect } from './TargetSelect'
import type { TargetChoices } from './targets'
import { typeName } from './ruleTypes'

/** What this dialog adds to. A new group needs a target of its own, and says where it goes. */
export type PasteWhere = { kind: 'into'; block: number } | { kind: 'new'; before: number }

const SHOWN = 6

/**
 * Pasting a list, or a file's text (design 3.6). Nothing is written until the list has been checked: "Check" reads every
 * line, and the review says what will be added, what was skipped and why, and which entries would never take effect or
 * would take over rules of a later group. The button then says how many it adds.
 */
export function PasteDialog({ where, doc, blocks, choices, initialText = '', onSubmit, onClose }: {
  where: PasteWhere
  doc: Doc
  blocks: readonly Block[]
  choices: TargetChoices
  initialText?: string
  /** `target` is only meaningful for a new group. */
  onSubmit: (entries: PasteEntry[], target: string) => void
  onClose: () => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const [text, setText] = useState(initialText)
  const [bare, setBare] = useState<BareDomainType>('DOMAIN-SUFFIX')
  const [checkedText, setCheckedText] = useState<string | null>(initialText ? initialText : null)
  const [target, setTarget] = useState('')
  const into = where.kind === 'into' ? blocks[where.block] : null
  const groupName = into ? into.target : t('admin:rules.paste.new_group_title')

  const review = useMemo(() => {
    if (checkedText === null) return null
    const scan = scanPaste(checkedText, bare)
    const place: PasteTarget = where.kind === 'into' ? { kind: 'into', block: where.block } : { kind: 'new', before: where.before }
    return { scan, review: reviewPaste(scan.entries, doc, blocks, place) }
  }, [checkedText, bare, where, doc, blocks])

  const needsTarget = where.kind === 'new' && target === ''
  const count = review?.review.add.length ?? 0
  const byType = useMemo(() => {
    const m = new Map<string, number>()
    for (const e of review?.review.add ?? []) m.set(e.type, (m.get(e.type) ?? 0) + 1)
    return [...m]
  }, [review])
  const notes = (review?.review.add ?? []).filter(e => e.note)
  const wildcards = notes.filter(e => e.note === 'wildcard')
  const urls = notes.filter(e => e.note === 'url_host')

  return (
    <FormDialog
      size="lg"
      title={into ? t('admin:rules.paste.title', { target: into.target }) : t('admin:rules.paste.new_group_title')}
      description={groupName}
      dirty={text.trim() !== ''}
      pending={false}
      submitLabel={review && checkedText === text ? t('admin:rules.paste.submit', { count: count.toLocaleString() }) : t('admin:rules.paste.check')}
      onClose={onClose}
      onSubmit={() => {
        if (checkedText !== text) { setCheckedText(text); return }
        if (count === 0 || needsTarget) return
        onSubmit(review!.review.add, target)
      }}
    >
      {where.kind === 'new' && (
        <TargetSelect value={target} choices={choices} label={t('admin:rules.blocks.target')} onChange={setTarget} />
      )}
      <Field label={t('admin:rules.paste.list_label')}>
        <Textarea
          className="min-h-40 font-mono text-xs"
          value={text}
          placeholder={t('admin:rules.paste.placeholder')}
          spellCheck={false}
          autoFocus
          onChange={e => setText(e.target.value)}
        />
      </Field>
      <div className="flex flex-wrap items-end gap-3">
        <div className="w-full sm:w-64">
          <Field label={t('admin:rules.paste.bare_domain')}>
            <NativeSelect value={bare} onChange={e => setBare(e.target.value as BareDomainType)}>
              <option value="DOMAIN-SUFFIX">{typeName(t, 'DOMAIN-SUFFIX', true)}</option>
              <option value="DOMAIN">{typeName(t, 'DOMAIN', true)}</option>
            </NativeSelect>
          </Field>
        </div>
      </div>
      <div role="status" aria-live="polite" className="flex flex-col gap-2 text-sm">
        {review && (
          <>
            <p className="font-medium">{t('admin:rules.paste.summary', { count: count.toLocaleString() })}</p>
            {byType.length > 0 && (
              <p className="text-muted-foreground">{byType.map(([type, n]) => t('admin:rules.paste.by_type', { type: typeName(t, type, true), count: n.toLocaleString() })).join(' · ')}</p>
            )}
            <ul className="flex list-disc flex-col gap-1 pl-5 text-muted-foreground">
              {review.review.duplicateInGroup + review.review.duplicateInPaste > 0 && <li>{t('admin:rules.paste.skipped_duplicate', { count: review.review.duplicateInGroup + review.review.duplicateInPaste })}</li>}
              {review.scan.comments > 0 && <li>{t('admin:rules.paste.skipped_comment', { count: review.scan.comments })}</li>}
              {review.scan.ignoredTarget > 0 && <li>{t('admin:rules.paste.ignored_target', { count: review.scan.ignoredTarget })}</li>}
              {review.scan.unrecognized.length > 0 && <li>{t('admin:rules.paste.unrecognized', { count: review.scan.unrecognized.length })}</li>}
            </ul>
            {review.scan.unrecognized.length > 0 && (
              <ul className="max-h-24 overflow-auto font-mono text-xs text-muted-foreground">
                {review.scan.unrecognized.slice(0, SHOWN).map(u => <li key={u.line} className="truncate">{u.line}: {u.text}</li>)}
              </ul>
            )}
            {(review.review.earlier.length > 0 || review.review.later.length > 0 || wildcards.length > 0 || urls.length > 0) && (
              <Alert>
                <AlertDescription>
                  <ul className="flex list-disc flex-col gap-1 pl-4">
                    {review.review.earlier.slice(0, SHOWN).map(e => (
                      <li key={e.value}>{t('admin:rules.paste.earlier_block', { value: e.value, index: e.block + 1, target: blocks[e.block]?.target })}</li>
                    ))}
                    {review.review.earlier.length > SHOWN && <li>{t('admin:rules.paste.more', { count: review.review.earlier.length - SHOWN })}</li>}
                    {review.review.later.map(l => (
                      <li key={l.block}>{t('admin:rules.paste.overrides_later', { index: l.block + 1, target: blocks[l.block]?.target, count: l.count })}</li>
                    ))}
                    {wildcards.slice(0, SHOWN).map(e => <li key={`w${e.line}`}>{t('admin:rules.paste.wildcard_as_suffix', { domain: e.value })}</li>)}
                    {urls.slice(0, SHOWN).map(e => <li key={`u${e.line}`}>{t('admin:rules.paste.url_host', { host: e.value })}</li>)}
                  </ul>
                </AlertDescription>
              </Alert>
            )}
          </>
        )}
      </div>
    </FormDialog>
  )
}
