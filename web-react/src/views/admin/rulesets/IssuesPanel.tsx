import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Disclosure } from '@/components/common/Disclosure'
import { Notice } from '@/components/common/Notice'
import { DetailDialog } from '@/components/data/DetailDialog'
import { EmptyState } from '@/components/feedback/EmptyState'
import { Button } from '@/components/ui/button'
import { IconInfo, IconSpinner, IconWarning } from '@/lib/icons'
import type { Finding, ShadowKind } from '@/utils/ruleset/lint'
import type { Doc } from '@/utils/ruleset/parse'

const PAGE = 50
const ORDER: { kind: ShadowKind; level: 'warning' | 'info'; key: string }[] = [
  { kind: 'other', level: 'warning', key: 'group_other' },
  { kind: 'after_match', level: 'warning', key: 'group_after' },
  { kind: 'same', level: 'info', key: 'group_same' },
]

const ruleText = (doc: Doc, line: number) => doc.lines[line].raw.replace(/\r$/, '').trim().replace(/^-\s+/, '')

/**
 * The problems panel (design 3.13): the rules an earlier rule already covers, in three folded groups so that a rule set with
 * a thousand of them does not bury everything else. "Never applies" (the earlier rule has another target: what the author meant
 * is not what happens) comes first, then "after the fallback", then the harmless redundant ones. Each line names the rule that
 * wins and jumps to the line. It reports and nothing more: no button here edits the rules, because which of two rules to keep is
 * the author's call, and the answer is not always the later one.
 *
 * `findings` is null while the analysis for the current text is still running (it runs when the browser is idle).
 */
export function IssuesPanel({ doc, findings, textOnly, onJump, onClose }: {
  doc: Doc
  findings: readonly Finding[] | null
  textOnly: boolean
  onJump?: (line: number) => void
  onClose: () => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const [open, setOpen] = useState<Record<string, boolean>>({ other: true })
  const [shown, setShown] = useState<Record<string, number>>({})

  const message = (f: Finding) => {
    const by = (f.by + 1).toLocaleString()
    if (f.kind === 'after_match') return t('admin:rules.lint.after_match', { line: by })
    if (f.kind === 'other') return t('admin:rules.lint.shadowed_other', { line: by, winner: doc.lines[f.by].rule?.target ?? '' })
    return f.duplicate ? t('admin:rules.lint.duplicate', { line: by }) : t('admin:rules.lint.shadowed_same', { line: by })
  }

  return (
    <DetailDialog open onClose={onClose} size="lg" title={t('admin:rules.issues.title')} description={t('admin:rules.issues.description')}>
      <div className="flex flex-col gap-3">
        {textOnly ? <Notice tone="warning">{t('admin:rules.issues.text_only')}</Notice>
          : findings === null ? <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground"><IconSpinner aria-hidden className="animate-spin" />{t('admin:rules.issues.computing')}</p>
          : findings.length === 0 ? <EmptyState compact title={t('admin:rules.issues.none')} />
          : ORDER.map(({ kind, level, key }) => {
            const list = findings.filter(f => f.kind === kind)
            if (list.length === 0) return null
            const count = shown[kind] ?? PAGE
            const Icon = level === 'warning' ? IconWarning : IconInfo
            return (
              <Disclosure key={kind} open={!!open[kind]} onToggle={() => setOpen(o => ({ ...o, [kind]: !o[kind] }))} className="rounded-lg border" buttonClassName="px-3 py-2"
                summary={(
                  <span className="flex min-w-0 flex-wrap items-center gap-x-2 text-sm">
                    <Icon aria-hidden className="size-4 shrink-0" />
                    <span className="font-medium">{t(`admin:rules.issues.level.${level}`)}</span>
                    <span className="tabular-nums">{list.length.toLocaleString()}</span>
                    <span className="text-muted-foreground">{t(`admin:rules.lint.${key}`)}</span>
                  </span>
                )}
                bodyClassName="flex flex-col gap-1.5 border-t p-2"
              >
                <ul className="flex flex-col gap-1.5">
                  {list.slice(0, count).map(f => (
                    <li key={f.line} className="flex min-w-0 flex-col gap-0.5 rounded-md border px-2 py-1.5">
                      <code className="font-mono text-xs break-all">{ruleText(doc, f.line)}</code>
                      <span className="text-xs text-muted-foreground">{message(f)}</span>
                      {onJump && (
                        <span><Button variant="ghost" size="xs" onClick={() => { onJump(f.line); onClose() }}>{t('admin:rules.tester.go_to_line', { line: (f.line + 1).toLocaleString() })}</Button></span>
                      )}
                    </li>
                  ))}
                </ul>
                {list.length > count && (
                  <div><Button variant="outline" size="sm" onClick={() => setShown(s => ({ ...s, [kind]: count + PAGE }))}>{t('admin:rules.issues.show_more', { count: Math.min(PAGE, list.length - count).toLocaleString() })}</Button></div>
                )}
              </Disclosure>
            )
          })}
      </div>
    </DetailDialog>
  )
}
