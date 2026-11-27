import { useTranslation } from 'react-i18next'

import type { RuleSet } from '@/api/rules'
import { DetailDialog } from '@/components/data/DetailDialog'
import { EmptyState } from '@/components/feedback/EmptyState'
import { Button } from '@/components/ui/button'
import { HunkList, useContentHunks } from './ChangesDialog'
import { changedByBoth, changedFields, type ConflictField } from './conflict'

/**
 * One side of the comparison: what changed from the version the draft started from, as the list of fields that differ and,
 * for the rules text, the changed lines. A rule set can be half a megabyte, so only `content` is compared line by line;
 * every other field is named, not diffed.
 */
function Side({ title, base, other }: { title: string; base: RuleSet; other: RuleSet }) {
  const { t } = useTranslation(['admin'])
  const fields = changedFields(base, other).filter(f => f !== 'content')
  const { hunks, blocks, added, removed } = useContentHunks(base.content, other.content)
  return (
    <section className="flex flex-col gap-3">
      <h3 className="text-sm font-semibold">{title}{hunks.length > 0 && <span className="ml-2 font-mono text-xs font-normal text-muted-foreground">+{added} −{removed}</span>}</h3>
      {fields.length > 0 && <p className="text-sm">{t('admin:rules.conflict.fields', { fields: fields.map(f => t(`admin:rules.conflict.field.${f}`)).join(', ') })}</p>}
      {hunks.length > 0 ? <HunkList hunks={hunks} blocks={blocks} />
        : fields.length === 0 ? <EmptyState compact title={t('admin:rules.conflict.no_changes')} />
        : <p className="text-sm text-muted-foreground">{t('admin:rules.conflict.content_same')}</p>}
    </section>
  )
}

/**
 * "Review their changes" (design 3.12, 4.2 B5): the server's current version and my draft, each against the version my
 * draft started from, so what they did and what I did can be told apart. Which of the two wins is not decided here; the
 * page offers Overwrite and Discard.
 */
export function ConflictDialog({ base, theirs, mine, onClose }: { base: RuleSet; theirs: RuleSet; mine: RuleSet; onClose: () => void }) {
  const { t } = useTranslation(['admin', 'common'])
  const both = changedByBoth(base, theirs, mine)
  const label = (f: ConflictField) => t(`admin:rules.conflict.field.${f}`)
  return (
    <DetailDialog open onClose={onClose} size="xl" title={t('admin:rules.stale_review')} description={t('admin:rules.conflict.description')}
      footer={<Button variant="outline" onClick={onClose}>{t('common:actions.close')}</Button>}>
      <div className="flex flex-col gap-6">
        <p className="text-sm">{both.length > 0 ? t('admin:rules.conflict.both', { fields: both.map(label).join(', ') }) : t('admin:rules.conflict.none_both')}</p>
        <Side title={t('admin:rules.conflict.theirs')} base={base} other={theirs} />
        <Side title={t('admin:rules.conflict.mine')} base={base} other={mine} />
      </div>
    </DetailDialog>
  )
}
