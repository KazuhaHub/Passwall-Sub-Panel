import { useTranslation } from 'react-i18next'

import type { DestTestResult, DestTestStep } from '@/api/accessControl'
import { AccessBadge } from '@/components/access/AccessBadge'
import { cn } from '@/lib/utils'
import type { AccessToneKind } from '@/utils/accessControl'

const T = 'admin:access_control.test.'

/** The step that ended the evaluation, if a step did. */
export function terminatingStep(r: DestTestResult): DestTestStep | undefined {
  return r.steps.find(s => s.result === 'hit')
}

export function verdictTone(r: DestTestResult): AccessToneKind {
  if (r.verdict === 'block') return 'block'
  // A report-only hit is "what it WOULD do": the closed table's would-be-denied row.
  if (r.verdict === 'observe') return 'would_deny'
  if (r.verdict === 'allow') return r.terminating_step === 'exempt' ? 'exempt' : 'allow'
  return 'none'
}

/** The inbound a source-rule step belongs to, by its node id (undefined while unknown). */
export type InboundName = (nodeId: number) => string | undefined

/** The verdict's badge and the sentence beside it. */
export function VerdictLine({ result }: { result: DestTestResult }) {
  const { t } = useTranslation(['admin'])
  const kind = verdictTone(result)
  const hit = terminatingStep(result)
  const name = hit?.policy_id ? (hit.name || t(`${T}deleted_policy`)) : (hit?.name ?? '')
  const groupName = hit?.name ?? ''
  const entry = hit?.entry ?? ''
  let sentence: string
  if (result.terminating_step === 'exempt') sentence = t(`${T}verdict.exempt`)
  else if (hit?.kind === 'builtin') sentence = t(`${T}verdict.builtin`)
  // A report-only hit is decided by the flag, whatever the rule's kind: it recorded the connection and blocked nothing.
  else if (hit?.kind === 'group' && (hit.report_only || result.verdict === 'observe')) sentence = t(`${T}verdict.observe_group`, { group: groupName })
  else if (hit?.report_only || result.verdict === 'observe') {
    sentence = t(`${T}verdict.${hit?.kind === 'source' ? 'observe_source' : 'observe'}`, { entry: hit?.entry ?? result.steps.find(s => s.result === 'hit' && s.entry)?.entry ?? '' })
  }
  else if (hit?.kind === 'group') sentence = t(`${T}verdict.group`, { group: groupName })
  else if (hit?.kind === 'source') sentence = t(`${T}verdict.source`, { entry })
  else if (result.verdict === 'block') sentence = t(`${T}verdict.block`, { step: hit?.step ?? 0, name, entry })
  else if (result.verdict === 'allow') sentence = t(`${T}verdict.allow`, { step: hit?.step ?? 0, name })
  else sentence = t(`${T}verdict.default`)
  return (
    <div className="flex flex-wrap items-center gap-2">
      {/* The wrapper carries the test hook and the kind: a quiet kind is a dot with no element of its own. */}
      <span data-testid="test-verdict" data-state={kind} className="inline-flex max-w-full">
        <AccessBadge kind={kind} label={t(`${T}badge.${result.verdict === 'observe' ? 'observe' : kind}`)} />
      </span>
      <p className="min-w-0 [overflow-wrap:anywhere]">{sentence}</p>
    </div>
  )
}

/** The trace, step by step, on the same line-and-dot pattern as the policy rail. */
export default function EvalTrace({ result, inboundName }: { result: DestTestResult; inboundName?: InboundName }) {
  const { t } = useTranslation(['admin'])
  return (
    <ol data-testid="eval-trace" className="m-0 ml-3 list-none border-l-2 border-border p-0 pl-1">
      {result.steps.map(s => {
        const report = s.report_only === true
        const inbound = s.kind === 'source' && s.node_id ? inboundName?.(s.node_id) : undefined
        const named = s.kind === 'source'
          ? [t(`${T}kind.source`), inbound].filter(Boolean).join(' · ')
          : s.kind === 'group' ? `${t(`${T}kind.group`)} · ${s.name || t(`${T}deleted_group`)}`
          : s.kind === 'builtin' ? t(`${T}kind.builtin`)
            : s.kind === 'exempt' ? t(`${T}kind.exempt`)
              : `${t(`${T}kind.${report ? 'report' : s.kind}`)}${s.policy_id ? ` · ${s.name || t(`${T}deleted_policy`)}` : ''}`
        const label = report && s.kind === 'source' ? `${named} · ${t(`${T}kind.report`)}` : named
        const hit = s.result === 'hit'
        const shadowed = s.result === 'shadowed'
        return (
          <li key={s.step} data-result={s.result} className="-ml-3.5 flex items-start gap-3 py-1.5">
            <span aria-hidden className="grid size-6 shrink-0 place-items-center rounded-full bg-muted text-xs font-semibold">{s.step}</span>
            <div className="min-w-0">
              <p className={cn('text-sm [overflow-wrap:anywhere]', hit && 'font-semibold')}>{label}</p>
              <p className={cn('text-sm', shadowed ? 'font-semibold text-foreground' : 'text-muted-foreground')}>
                {report && s.result === 'hit' ? t(`${T}result.hit_report`) : t(`${T}result.${s.result}`)}
                {s.entry && <span className="ml-1.5 font-mono text-[13px]">{s.entry}</span>}
              </p>
            </div>
          </li>
        )
      })}
    </ol>
  )
}
