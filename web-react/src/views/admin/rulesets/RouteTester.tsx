import { useDeferredValue, useId, useMemo, useState, type Dispatch, type KeyboardEvent, type SetStateAction } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'

import type { ExplainRequest, RuleSet } from '@/api/rules'
import type { Template } from '@/api/templates'
import UserAutocomplete from '@/components/UserAutocomplete'
import { Notice } from '@/components/common/Notice'
import { Disclosure } from '@/components/common/Disclosure'
import { Field } from '@/components/form/Field'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { IconSpinner } from '@/lib/icons'
import { useCan } from '@/utils/permissions'
import { blockAt, type Block } from '@/utils/ruleset/blocks'
import type { Continuation } from '@/utils/ruleset/continuation'
import { prepareProbe, routeFor, type MaybeHit, type ProbeInput, type ProbeProblem, type RouteHit, type UnknownReason } from '@/utils/ruleset/match'
import type { Doc } from '@/utils/ruleset/parse'
import { ExplainResultView } from './ExplainResultView'
import { tooLargeText } from './saveFailures'
import { isBuiltinTarget } from './targets'
import type { RouteExplain } from './useRouteExplain'

export const EMPTY_PROBE: ProbeInput = { address: '', port: '443', network: 'tcp', resolvedIp: '', process: '', srcPort: '', srcIp: '' }

const SHOWN_LATER = 8
const SHOWN_MAYBE = 5

/** The rule of a line as it reads in the list: no dash, no indent. */
const ruleText = (doc: Doc, line: number) => doc.lines[line].raw.replace(/\r$/, '').trim().replace(/^-\s+/, '')

/**
 * "Test routing" (design 3.9): a domain or an IP with a port and a protocol, run top to bottom through the DRAFT. It shows the
 * first rule that matches, the group and the target it sends to, and the rules below that would have matched as well and so
 * never apply. What a browser cannot decide is said, never hidden: a line that needs the client's GeoIP data, a DNS answer, a
 * condition that was not entered, a regular expression that cannot be run safely or a sub-rule is listed as "may match first",
 * and the answer under it is marked not certain (see utils/ruleset/match for what is decided and what is not).
 *
 * The answer is worked out from a deferred copy of what is typed, so typing in the box is never held up by a pass over the rules.
 */
export function RouteTester({ doc, blocks, input, setInput, textOnly, cont, groupsKnown, onJump, server }: {
  doc: Doc
  blocks: readonly Block[]
  input: ProbeInput
  setInput: Dispatch<SetStateAction<ProbeInput>>
  /** The document has lines the editor cannot read as a list: routing is not tested, and no line can be opened. */
  textOnly: boolean
  /** Which targets end the matching: from the backend's reading of the draft's groups, or "unknown" when that is not in (see utils/ruleset/continuation). */
  cont: Continuation
  /** `cont` rests on the backend's members for this very draft (and not on "unknown"). */
  groupsKnown: boolean
  /** Opens a line in the group it is in; absent when that is not possible (text view). */
  onJump?: (line: number) => void
  /**
   * What the server mode needs (design 4.2 B4): the whole draft to send, the templates to choose from and the state that outlives
   * this panel. Absent for a sub-rule's editor, which has no routing test at all; the mode is also hidden from an operator.
   */
  server?: { draft: RuleSet; templates: readonly Template[]; explain: RouteExplain }
}) {
  const { t } = useTranslation(['admin', 'common'])
  const admin = useCan('config.write')
  const scopeId = useId()
  const explain = server?.explain
  // The server can walk a Mihomo rule list only (its sing-box answer would be a different computation), so only the Mihomo
  // templates this rule set is in are offered, and the sing-box ones are mentioned so that nobody looks for them.
  const bound = useMemo(() => (server ? server.templates.filter(x => (x.rule_sets || []).includes(server.draft.slug)) : []), [server])
  const mihomo = useMemo(() => bound.filter(x => x.client_type === 'mihomo'), [bound])
  const singboxBound = bound.length - mihomo.length
  const chosen = mihomo.find(x => x.slug === explain?.templateSlug) ?? mihomo[0]
  const serverAvailable = !!server && admin && mihomo.length > 0
  const serverMode = serverAvailable && explain!.scope === 'template' && !!chosen
  const [attempted, setAttempted] = useState(false)
  const set = (patch: Partial<ProbeInput>) => { explain?.clearFailure(); setInput(p => ({ ...p, ...patch })) }
  const [moreOpen, setMoreOpen] = useState(() => input.resolvedIp !== '' || input.process !== '' || input.srcPort !== '' || input.srcIp !== '')
  const deferred = useDeferredValue(input)
  const prepared = useMemo(() => (deferred.address.trim() === '' ? null : prepareProbe(deferred)), [deferred])
  const result = useMemo(() => (!serverMode && !textOnly && prepared?.ok ? routeFor(doc, prepared.probe, cont) : null), [doc, prepared, textOnly, cont, serverMode])
  const problem: ProbeProblem | null = prepared && !prepared.ok ? prepared.problem : attempted && input.address.trim() === '' ? 'address_empty' : null
  const stale = deferred !== input

  // ---- the server mode: asked for by the Test button or Enter, never by typing --------------------------------------------------
  const outcome = explain?.outcome
  const failure = outcome?.status === 'failed' ? outcome.failure : null
  const probeKey = JSON.stringify(input)
  const outOfDate = !!outcome && outcome.status !== 'idle' && !!server && (outcome.asked.draft !== server.draft || outcome.asked.templateSlug !== chosen?.slug || outcome.asked.userId !== explain!.userId || outcome.asked.probe !== probeKey)
  const pending = outcome?.status === 'pending'
  const test = () => {
    if (!serverMode || !server || !explain || !chosen || pending) return
    setAttempted(true)
    const prepared = prepareProbe(input)
    if (!prepared.ok) return
    const p = prepared.probe
    const request: ExplainRequest = {
      template_slug: chosen.slug,
      draft: server.draft,
      ...(explain.userId ? { user_id: explain.userId } : {}),
      // A port that is 0 or absent is "not given" to the server; what was left blank is left out.
      probe: {
        host: p.host, network: p.network,
        ...(p.port !== null ? { port: p.port } : {}),
        ...(input.resolvedIp.trim() !== '' ? { ip: input.resolvedIp.trim() } : {}),
        ...(p.process !== '' ? { process: p.process } : {}),
        ...(input.srcIp.trim() !== '' ? { src_ip: input.srcIp.trim() } : {}),
        ...(p.srcPort !== null ? { src_port: p.srcPort } : {}),
      },
    }
    explain.run(request, { draft: server.draft, templateSlug: chosen.slug, userId: explain.userId, probe: probeKey, request })
  }
  // Enter in a field of this panel never saves the page it sits in (the column is inside the page's form); with the server mode on it asks.
  const onKeyDown = (e: KeyboardEvent<HTMLElement>) => {
    if (e.key !== 'Enter' || !(e.target instanceof HTMLInputElement)) return
    e.preventDefault()
    // Enter that commits an input-method candidate is not a request to test.
    if (e.nativeEvent.isComposing || e.keyCode === 229) return
    if (serverMode) test()
  }
  const fieldFailure = failure?.kind === 'field' ? failure.field : null
  // An error inside "More conditions" must not hide in the closed section.
  const hiddenProblem = problem === 'resolved_invalid' || problem === 'src_invalid' || fieldFailure === 'resolved' || fieldFailure === 'src'
  const failureText = (field: 'address' | 'port' | 'resolved' | 'src') => (fieldFailure === field && failure?.kind === 'field' ? t(`admin:rules.tester.problem.${failure.code.replace(/^explain_/, '')}`) : undefined)

  const groupOf = (line: number) => blockAt(blocks, line)?.index ?? 0
  const lineLink = (line: number) => {
    const label = t('admin:rules.blocks.line_single', { line: (line + 1).toLocaleString() })
    return onJump && !textOnly
      ? <button type="button" className="font-medium underline underline-offset-4" aria-label={t('admin:rules.tester.go_to_line', { line: (line + 1).toLocaleString() })} onClick={() => onJump(line)}>{label}</button>
      : <span className="font-medium">{label}</span>
  }
  const hitRow = (hit: RouteHit, key: string) => (
    <li key={key} className="flex min-w-0 flex-col gap-0.5 rounded-md border px-2 py-1.5">
      <span className="flex flex-wrap items-center gap-x-2 text-xs">{lineLink(hit.line)}<span className="text-muted-foreground">{t('admin:rules.blocks.heading', { index: groupOf(hit.line) + 1, target: hit.target })}</span></span>
      <code className="font-mono text-xs break-all">{ruleText(doc, hit.line)}</code>
    </li>
  )

  return (
    <section aria-label={t('admin:rules.tester.title')} aria-busy={stale || pending} className="flex min-w-0 flex-col gap-3" onKeyDown={onKeyDown}>
      {server && (
        !admin ? <p className="text-xs text-muted-foreground">{t('admin:rules.tester.server.admin_only')}</p>
        : mihomo.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t(singboxBound > 0 ? 'admin:rules.tester.server.only_singbox' : 'admin:rules.tester.server.no_template')}</p>
        ) : (
          <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-1.5">
              <span id={scopeId} className="text-sm font-medium">{t('admin:rules.tester.server.scope')}</span>
              <SegmentedControl<'draft' | 'template'> aria-labelledby={scopeId} value={explain!.scope}
                onChange={next => { explain!.setScope(next); if (next === 'template' && !explain!.templateSlug) explain!.setTemplateSlug(mihomo[0].slug) }}
                options={[{ value: 'draft', label: t('admin:rules.tester.server.scope_draft') }, { value: 'template', label: t('admin:rules.tester.template') }]} />
            </div>
            {serverMode && (
              <>
                <Field label={t('admin:rules.tester.server.template_field')} error={failure?.kind === 'not_found' ? t('admin:rules.tester.server.error.not_found') : failure?.kind === 'unsupported_client' ? t('admin:rules.tester.server.error.unsupported_client') : undefined}
                  hint={singboxBound > 0 ? t('admin:rules.tester.server.singbox_note') : undefined}>
                  <NativeSelect value={chosen!.slug} onChange={e => { explain!.clearFailure(); explain!.setTemplateSlug(e.target.value) }}>
                    {mihomo.map(x => <option key={x.slug} value={x.slug}>{x.name || x.slug}</option>)}
                  </NativeSelect>
                </Field>
                <div className="flex flex-col gap-1.5">
                  <span className="text-sm font-medium">{t('admin:rules.tester.user')}</span>
                  <UserAutocomplete value={explain!.userId} onChange={id => { explain!.clearFailure(); explain!.setUserId(id) }} label={t('admin:rules.tester.user')} fullWidth />
                  <span className="text-xs text-muted-foreground">{t('admin:rules.tester.server.user_hint')}</span>
                </div>
              </>
            )}
          </div>
        )
      )}
      <div className="flex flex-col gap-3">
        <Field label={t('admin:rules.tester.address')} error={problem === 'address_invalid' || problem === 'address_idn' || problem === 'address_empty' ? t(`admin:rules.tester.problem.${problem}`) : failureText('address')} hint={<>{t('admin:rules.tester.ip_note')} {t('admin:rules.tester.address_assumption')}</>}>
          <Input value={input.address} placeholder={t('admin:rules.tester.address_placeholder')} spellCheck={false} autoCapitalize="none" autoCorrect="off" onChange={e => set({ address: e.target.value })} />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label={t('admin:rules.tester.port')} error={problem === 'port_invalid' ? t('admin:rules.tester.problem.port_invalid') : failureText('port')}>
            <Input value={input.port} inputMode="numeric" onChange={e => set({ port: e.target.value })} />
          </Field>
          <div className="flex flex-col gap-1.5">
            <span id="tester-network" className="text-sm font-medium">{t('admin:rules.tester.network')}</span>
            <SegmentedControl<'tcp' | 'udp'> aria-labelledby="tester-network" value={input.network} onChange={network => set({ network })}
              options={[{ value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }]} />
          </div>
        </div>
        <Disclosure open={moreOpen || hiddenProblem} onToggle={() => setMoreOpen(o => !o)} summary={<span className="text-sm font-medium">{t('admin:rules.tester.more')}</span>} bodyClassName="mt-2 flex flex-col gap-3">
          <Field label={t('admin:rules.tester.resolved_ip')} error={problem === 'resolved_invalid' ? t('admin:rules.tester.problem.resolved_invalid') : failureText('resolved')}>
            <Input value={input.resolvedIp} spellCheck={false} onChange={e => set({ resolvedIp: e.target.value })} />
          </Field>
          <Field label={t('admin:rules.tester.process')}>
            <Input value={input.process} spellCheck={false} onChange={e => set({ process: e.target.value })} />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label={t('admin:rules.tester.src_port')} error={problem === 'src_invalid' ? t('admin:rules.tester.problem.src_invalid') : failureText('src')}>
              <Input value={input.srcPort} inputMode="numeric" onChange={e => set({ srcPort: e.target.value })} />
            </Field>
            <Field label={t('admin:rules.tester.src_ip')}>
              <Input value={input.srcIp} spellCheck={false} onChange={e => set({ srcIp: e.target.value })} />
            </Field>
          </div>
        </Disclosure>
        {serverMode && (
          <div>
            <Button type="button" disabled={pending} onClick={test}>
              {pending && <IconSpinner aria-hidden className="animate-spin" />}
              {t(pending ? 'admin:rules.tester.server.running' : 'admin:rules.tester.server.run')}
            </Button>
          </div>
        )}
      </div>

      <div role="status" aria-live="polite" className="flex min-w-0 flex-col gap-3 text-sm">
        {serverMode ? (
          <>
            {outcome && outcome.status === 'done' && <p className="text-xs text-muted-foreground">{t('admin:rules.tester.server.answer_server', { template: mihomo.find(x => x.slug === outcome.response.template_slug)?.name || outcome.response.template_slug })}</p>}
            {outcome && outcome.status === 'done' && server && (
              <ExplainResultView t={t} response={outcome.response} draftSlug={server.draft.slug} draftName={server.draft.name} stale={outOfDate} onJump={onJump && !textOnly && !outOfDate ? onJump : undefined} />
            )}
            {failure?.kind === 'too_large' && outcome?.status === 'failed' && <Notice tone="danger">{tooLargeText(t, failure.limitBytes, new TextEncoder().encode(JSON.stringify(outcome.asked.request)).length)}</Notice>}
            {failure?.kind === 'forbidden' && <Notice tone="danger">{t('admin:rules.tester.server.admin_only')}</Notice>}
            {failure?.kind === 'other' && <Notice tone="danger">{t(failure.code === 'explain_no_group' ? 'admin:rules.tester.server.error.explain_no_group' : 'admin:rules.tester.server.error.other')}</Notice>}
            {(!outcome || outcome.status === 'idle') && <p className="text-muted-foreground">{t('admin:rules.tester.server.empty')}</p>}
          </>
        ) : textOnly ? (
          <Notice tone="warning">{t('admin:rules.tester.text_only')}</Notice>
        ) : prepared === null ? (
          <p className="text-muted-foreground">{t('admin:rules.tester.empty')}</p>
        ) : result ? (
          <>
            {serverAvailable && <p className="text-xs text-muted-foreground">{t('admin:rules.tester.server.answer_local')}</p>}
            <Result t={t} doc={doc} result={result} udp={deferred.network === 'udp'} resolvedEntered={deferred.resolvedIp.trim() !== ''} groupsKnown={groupsKnown} groupOf={groupOf} lineLink={lineLink} hitRow={hitRow} />
          </>
        ) : null}
      </div>

      <p className="text-xs text-muted-foreground">{serverMode ? t('admin:rules.tester.server.scope_note') : `${t('admin:rules.tester.mihomo_only')} ${t('admin:rules.tester.scope')}`}</p>
    </section>
  )
}

function Result({ t, doc, result, udp, resolvedEntered, groupsKnown, groupOf, lineLink, hitRow }: {
  t: TFunction
  udp: boolean
  resolvedEntered: boolean
  groupsKnown: boolean
  doc: Doc
  result: ReturnType<typeof routeFor>
  groupOf: (line: number) => number
  lineLink: (line: number) => React.ReactNode
  hitRow: (hit: RouteHit, key: string) => React.ReactNode
}) {
  const { first, final, maybe, maybeTotal, later, laterTotal, passed } = result
  const [allLater, setAllLater] = useState(false)
  const shownLater = allLater ? later : later.slice(0, SHOWN_LATER)
  const reasonKey: Record<UnknownReason, string> = {
    client_data: 'branch_client_data', resolve: 'branch_resolve', input: 'branch_input', regex: 'branch_regex', sub_rule: 'branch_sub_rule', unsupported: 'branch_unsupported',
  }
  const maybeRow = (m: MaybeHit) => (
    <li key={m.line} className="flex min-w-0 flex-col gap-0.5 rounded-md border px-2 py-1.5 text-xs">
      <span className="break-words">{t(`admin:rules.tester.${m.reason === 'resolve' && resolvedEntered ? 'branch_resolve_entered' : reasonKey[m.reason]}`, { line: (m.line + 1).toLocaleString(), rule: ruleText(doc, m.line) })}</span>
      {m.target !== '' && <span className="text-muted-foreground">{t('admin:rules.tester.branch_if_match', { target: m.target })}</span>}
      <span>{lineLink(m.line)}</span>
    </li>
  )

  const goes = first && (isBuiltinTarget(first.target) && first.target !== 'PASS'
    ? t('admin:rules.tester.goes_builtin', { action: t(`admin:rules.tester.action.${first.target}`) })
    : t('admin:rules.tester.goes_group', { target: first.target }))

  return (
    <>
      {maybeTotal > 0 && (
        <Notice tone="warning">
          <p className="font-medium">{t('admin:rules.tester.uncertain_title')}</p>
          <p>{t('admin:rules.tester.uncertain_body')}</p>
        </Notice>
      )}
      {maybeTotal > 0 && (
        <ul className="flex flex-col gap-1.5">
          {maybe.slice(0, SHOWN_MAYBE).map(maybeRow)}
          {maybeTotal > SHOWN_MAYBE && <li className="text-xs text-muted-foreground">{t('admin:rules.tester.more_count', { count: (maybeTotal - SHOWN_MAYBE).toLocaleString() })}</li>}
        </ul>
      )}
      {maybeTotal > 0 && first && <p className="text-xs text-muted-foreground">{t('admin:rules.tester.branch_else')}</p>}

      {passed.map(p => <p key={p.line} className="text-xs text-muted-foreground">{t('admin:rules.tester.passed', { line: (p.line + 1).toLocaleString() })}</p>)}

      {first ? (
        <div className="flex flex-col gap-2 rounded-lg border bg-muted/30 p-3">
          <p>{t('admin:rules.tester.matched', { index: groupOf(first.line) + 1, group: first.target, rule: ruleText(doc, first.line), line: (first.line + 1).toLocaleString() })}</p>
          <p className="font-medium">{goes}</p>
          <p>{lineLink(first.line)}</p>
        </div>
      ) : maybeTotal === 0 ? (
        <p>{t('admin:rules.tester.no_match')}</p>
      ) : (
        <p>{t('admin:rules.tester.no_certain')}</p>
      )}

      {first && !final && (
        <Notice tone="warning">
          <p className="font-medium">{t('admin:rules.tester.continues_title')}</p>
          <p>{t('admin:rules.tester.continues_body', { target: first.target })}</p>
          {!groupsKnown && <p className="mt-1 text-xs text-muted-foreground">{t('admin:rules.tester.no_groups')}</p>}
        </Notice>
      )}
      {first && udp && <p className="text-xs text-muted-foreground">{t('admin:rules.tester.udp_note')}</p>}

      {laterTotal > 0 && (
        <div className="flex flex-col gap-1.5">
          <p className="text-xs text-muted-foreground">{t(final ? 'admin:rules.tester.also_matching' : 'admin:rules.tester.also_may_apply', { count: laterTotal.toLocaleString() })}</p>
          <ul className="flex flex-col gap-1.5">{shownLater.map(h => hitRow(h, String(h.line)))}</ul>
          {!allLater && laterTotal > SHOWN_LATER && (
            <div><Button variant="ghost" size="sm" onClick={() => setAllLater(true)}>{t('admin:rules.tester.more_count', { count: (Math.min(later.length, laterTotal) - SHOWN_LATER).toLocaleString() })}</Button></div>
          )}
        </div>
      )}
    </>
  )
}
