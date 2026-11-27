import { useState, type ReactNode } from 'react'
import type { TFunction } from 'i18next'
import { Notice } from '@/components/common/Notice'
import { Button } from '@/components/ui/button'
import type { ExplainReason, ExplainResponse, ExplainRuleRef, ExplainWarning } from '@/api/rules'
import { isBuiltinTarget } from './targets'

const SHOWN_ALSO = 8
const SHOWN_BRANCHES = 5

const WARNING_TONE: Record<ExplainWarning, 'warning' | 'info'> = { draft_not_in_template: 'warning', draft_disabled: 'warning', unconfirmed_lines: 'info' }

/**
 * The server's answer to "where does this connection go in the subscription a client of this template gets" (design 4.2 B4),
 * worded: the first rule that matches and where it is (the user's personal rules, the direct subscription domain rule, a rule
 * set or the template itself), whether matching goes on past it, the lines that could not be decided, the rules below that match
 * too, and what the server said about how the answer was put together.
 *
 * A line of THIS rule set is a link to that line in the editor; a line of any other source is only named.
 */
export function ExplainResultView({ t, response, draftSlug, draftName, stale, onJump }: {
  t: TFunction
  response: ExplainResponse
  draftSlug: string
  draftName: string
  /** What is on screen differs from what was asked: say so rather than let an old answer pass for the current one. */
  stale: boolean
  /** Opens a line (counted from 0) of this rule set's rules in the editor; absent when that is not possible. */
  onJump?: (line: number) => void
}) {
  const { result, branches, branches_total: branchesTotal, also_matching: also, also_matching_total: alsoTotal, pass_skipped: passed = [], warnings = [] } = response
  const [allAlso, setAllAlso] = useState(false)
  const first = result.rule !== undefined ? (result as ExplainRuleRef) : null

  const sourceLabel = (r: ExplainRuleRef) => (r.source === 'rule_set'
    ? t('admin:rules.tester.source.rule_set', { name: r.rule_set === draftSlug ? (draftName || draftSlug) : r.rule_set })
    : t(`admin:rules.tester.source.${r.source}`))
  // Only a line of this rule set can be opened here; the line is 1-based in the response and counted from 0 by the editor.
  const own = (r: ExplainRuleRef) => r.source === 'rule_set' && r.rule_set === draftSlug && r.line > 0 && onJump !== undefined
  const where = (r: ExplainRuleRef): ReactNode => {
    const label = t('admin:rules.blocks.line_single', { line: r.line.toLocaleString() })
    return (
      <span className="flex flex-wrap items-center gap-x-1.5 text-xs">
        <span className="font-medium">{sourceLabel(r)}</span>
        {r.line > 0 && r.source !== 'subscription_domain' && (
          <>
            <span aria-hidden className="text-muted-foreground">·</span>
            {own(r)
              ? <button type="button" className="font-medium underline underline-offset-4" aria-label={t('admin:rules.tester.go_to_line', { line: r.line.toLocaleString() })} onClick={() => onJump!(r.line - 1)}>{label}</button>
              : <span className="text-muted-foreground">{label}</span>}
          </>
        )}
      </span>
    )
  }
  const card = (r: ExplainRuleRef, key: string, extra?: ReactNode) => (
    <li key={key} className="flex min-w-0 flex-col gap-0.5 rounded-md border px-2 py-1.5">
      {where(r)}
      <code className="font-mono text-xs break-all">{r.rule}</code>
      {extra}
    </li>
  )
  const reasonText = (r: ExplainRuleRef) => {
    const reason = (r.reason ?? 'unsupported') as ExplainReason
    return t(`admin:rules.tester.server.reason.${reason}`)
  }

  const target = result.target ?? ''
  const goes = first && (isBuiltinTarget(target) && target !== 'PASS'
    ? t('admin:rules.tester.goes_builtin', { action: t(`admin:rules.tester.action.${target}`) })
    : result.group_default
      ? t('admin:rules.tester.server.goes_group_default', { target, default: result.group_default })
      : t('admin:rules.tester.goes_group', { target }))
  const shownAlso = allAlso ? also : also.slice(0, SHOWN_ALSO)
  const allUnreachable = also.length > 0 && also.every(a => a.status === 'unreachable')

  return (
    <>
      {stale && <Notice tone="info">{t('admin:rules.tester.server.stale')}</Notice>}
      {warnings.map(w => <Notice key={w} tone={WARNING_TONE[w] ?? 'info'}>{t(`admin:rules.tester.server.warning.${w}`)}</Notice>)}
      {response.personal_rules === 'not_in_template' && <Notice tone="warning">{t('admin:rules.tester.server.personal.not_in_template')}</Notice>}
      {response.personal_rules !== 'not_in_template' && <p className="text-xs text-muted-foreground">{t(`admin:rules.tester.server.personal.${response.personal_rules}`)}</p>}

      {result.state === 'undecided' && (
        <>
          <Notice tone="warning">
            <p className="font-medium">{t('admin:rules.tester.uncertain_title')}</p>
            <p>{t('admin:rules.tester.uncertain_body')}</p>
          </Notice>
          <ul className="flex flex-col gap-1.5">
            {branches.slice(0, SHOWN_BRANCHES).map((b, i) => card(b, `b${i}`, (
              <>
                <span className="text-xs">{reasonText(b)}</span>
                {b.target !== '' && <span className="text-xs text-muted-foreground">{t('admin:rules.tester.branch_if_match', { target: b.target })}</span>}
              </>
            )))}
            {branchesTotal > SHOWN_BRANCHES && <li className="text-xs text-muted-foreground">{t('admin:rules.tester.more_count', { count: (branchesTotal - SHOWN_BRANCHES).toLocaleString() })}</li>}
          </ul>
          {first && <p className="text-xs text-muted-foreground">{t('admin:rules.tester.branch_else')}</p>}
        </>
      )}

      {passed.map((p, i) => (
        <p key={`p${i}`} className="text-xs text-muted-foreground">{t('admin:rules.tester.server.passed', { rule: p.rule, where: p.line > 0 ? `${sourceLabel(p)} · ${t('admin:rules.blocks.line_single', { line: p.line.toLocaleString() })}` : sourceLabel(p) })}</p>
      ))}

      {first ? (
        <div className="flex flex-col gap-2 rounded-lg border bg-muted/30 p-3">
          <ul className="flex flex-col">{card(first, 'first')}</ul>
          <p className="font-medium">{goes}</p>
        </div>
      ) : result.state === 'no_match' ? (
        <p>{t('admin:rules.tester.no_match')}</p>
      ) : (
        <p>{t('admin:rules.tester.no_certain')}</p>
      )}

      {result.state === 'continues' && first && (
        <Notice tone="warning">
          <p className="font-medium">{t('admin:rules.tester.continues_title')}</p>
          <p>{t(`admin:rules.tester.server.via.${result.continues_via ?? 'pass'}`, { target })}</p>
        </Notice>
      )}

      {alsoTotal > 0 && (
        <div className="flex flex-col gap-1.5">
          <p className="text-xs text-muted-foreground">{t(allUnreachable ? 'admin:rules.tester.also_matching' : 'admin:rules.tester.also_may_apply', { count: alsoTotal.toLocaleString() })}</p>
          <ul className="flex flex-col gap-1.5">
            {shownAlso.map((a, i) => card(a, `a${i}`, <span className="text-xs text-muted-foreground">{a.target}</span>))}
          </ul>
          {!allAlso && alsoTotal > SHOWN_ALSO && (
            <div><Button variant="ghost" size="sm" onClick={() => setAllAlso(true)}>{t('admin:rules.tester.more_count', { count: (Math.max(also.length, SHOWN_ALSO) - SHOWN_ALSO).toLocaleString() })}</Button></div>
          )}
        </div>
      )}
    </>
  )
}
