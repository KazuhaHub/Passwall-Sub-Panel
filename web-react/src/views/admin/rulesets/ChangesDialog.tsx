import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { DetailDialog } from '@/components/data/DetailDialog'
import { EmptyState } from '@/components/feedback/EmptyState'
import { Button } from '@/components/ui/button'
import { buildBlocks, blockAt } from '@/utils/ruleset/blocks'
import { hunksOf } from '@/utils/ruleset/diff'
import { parseDoc } from '@/utils/ruleset/parse'
import { cn } from '@/lib/utils'

const MAX_HUNKS = 200

/**
 * "Review changes" (design 3.12 item 9): the lines that differ from what is saved, with a few lines around each, the
 * way a code review shows them. What is listed is what is sent: the saved text is the draft's `content`.
 */
export function ChangesDialog({ saved, draft, onClose }: { saved: string; draft: string; onClose: () => void }) {
  const { t } = useTranslation(['admin', 'common'])
  const { hunks, blocks } = useMemo(() => {
    const split = (s: string) => (s === '' ? [] : s.replace(/\n$/, '').split('\n'))
    return { hunks: hunksOf(split(saved), split(draft), 2), blocks: buildBlocks(parseDoc(draft)) }
  }, [saved, draft])
  const added = hunks.reduce((n, h) => n + h.lines.filter(l => l.kind === 'add').length, 0)
  const removed = hunks.reduce((n, h) => n + h.lines.filter(l => l.kind === 'del').length, 0)
  return (
    <DetailDialog open onClose={onClose} size="xl" title={t('admin:rules.changes.title')} description={hunks.length > 0 ? `+${added} −${removed}` : undefined}
      footer={<Button variant="outline" onClick={onClose}>{t('common:actions.close')}</Button>}>
      {hunks.length === 0 ? <EmptyState compact title={t('admin:rules.changes.none')} /> : (
        <div className="flex flex-col gap-4">
          {hunks.slice(0, MAX_HUNKS).map(h => {
            const b = blockAt(blocks, Math.max(0, h.newStart - 1 + (h.lines.findIndex(l => l.kind !== 'same'))))
            const a = h.lines.filter(l => l.kind === 'add').length
            const r = h.lines.filter(l => l.kind === 'del').length
            return (
              <section key={`${h.oldStart}:${h.newStart}`} className="overflow-hidden rounded-lg border">
                <h3 className="border-b bg-muted/40 px-3 py-1.5 text-xs font-medium">
                  {b && b.kind === 'rules'
                    ? t('admin:rules.changes.block', { index: b.index + 1, target: b.target, added: a, removed: r })
                    : t('admin:rules.changes.hunk', { line: h.newStart, added: a, removed: r })}
                </h3>
                <pre className="overflow-x-auto font-mono text-xs leading-5">
                  {h.lines.map((l, i) => (
                    <div key={i} className={cn('px-3 whitespace-pre', l.kind === 'add' && 'bg-success/10', l.kind === 'del' && 'bg-destructive/10 text-destructive')}>
                      <span aria-hidden className="mr-2 inline-block w-3 select-none text-muted-foreground">{l.kind === 'add' ? '+' : l.kind === 'del' ? '−' : ' '}</span>
                      <span className="sr-only">{t(l.kind === 'add' ? 'admin:rules.changes.added_line' : l.kind === 'del' ? 'admin:rules.changes.removed_line' : 'admin:rules.changes.kept_line')} </span>
                      {l.text.replace(/\r$/, '')}
                    </div>
                  ))}
                </pre>
              </section>
            )
          })}
          {hunks.length > MAX_HUNKS && <p className="text-sm text-muted-foreground">{t('admin:rules.changes.truncated', { count: hunks.length - MAX_HUNKS })}</p>}
        </div>
      )}
    </DetailDialog>
  )
}
