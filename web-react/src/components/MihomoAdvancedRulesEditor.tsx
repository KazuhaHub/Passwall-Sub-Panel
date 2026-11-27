import { lazy, Suspense, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { inspectProxyGroups, type MihomoRematchOutbound, type MihomoSubRule, type ProxyGroupIssue, type RuleSet } from '@/api/rules'
import { LevelAlert } from '@/components/LevelAlert'
import { IconButton } from '@/components/common/IconButton'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { IconAdd, IconDelete, IconSpinner } from '@/lib/icons'
import { cn } from '@/lib/utils'

const CodeEditor = lazy(() => import('@/components/CodeEditor'))

interface Props {
  value: RuleSet
  onChange: (next: RuleSet) => void
  onValidationChange?: (hasErrors: boolean) => void
  /** Which of the two lists this is: they are separate tabs of the rule-set editor, each mounted on its own. */
  part: 'sub_rules' | 'rematches'
  /**
   * Show the findings for the Rematch rows. They stay hidden until the person has left the tab once or tried
   * to save (the old dialog showed them only on a close attempt): a row just added is empty on purpose and
   * must not start out red.
   */
  revealRematchIssues?: boolean
  /**
   * Draws the content of the selected sub-rule in place of the plain code editor. The rule-set page gives it the grouped rule
   * editor of its Rules tab, so a sub-rule is edited the same way the main rules are. `index` and `generation` together identify the
   * sub-rule (key the editor by both: `generation` changes whenever a sub-rule is added or removed, so a list shifting under an
   * index never hands one sub-rule's undo history to another).
   */
  renderContent?: (args: { index: number; generation: number; value: string; onChange: (content: string) => void; ariaLabel: string }) => ReactNode
}

/**
 * The Mihomo-only parts of a rule set: its sub-rules (named rule lists a main rule can jump to) and its Rematch
 * outbounds. Each is edited on its own tab of the rule-set sheet. The content is checked by the server
 * (inspect-proxy-groups, 300 ms after the last change) and the findings that belong to these parts are listed here.
 */
export default function MihomoAdvancedRulesEditor({ value, onChange, onValidationChange, part, revealRematchIssues = false, renderContent }: Props) {
  const { t } = useTranslation(['admin', 'common'])
  const subRules = value.mihomo_sub_rules || []
  const rematches = value.mihomo_rematch_outbounds || []
  const [selectedSubRuleIndex, setSelectedSubRuleIndex] = useState(0)
  const [issues, setIssues] = useState<ProxyGroupIssue[]>([])
  const [generation, setGeneration] = useState(0)
  const [checking, setChecking] = useState(false)
  const serialized = useMemo(() => JSON.stringify([
    value.content,
    value.proxy_group_members || {},
    value.proxy_group_options || {},
    subRules,
    rematches,
  ]), [value.content, value.proxy_group_members, value.proxy_group_options, subRules, rematches])

  useEffect(() => {
    const controller = new AbortController()
    const timer = window.setTimeout(() => {
      setChecking(true)
      void inspectProxyGroups({
        content: value.content,
        proxy_group_members: value.proxy_group_members || {},
        proxy_group_options: value.proxy_group_options || {},
        mihomo_sub_rules: subRules,
        mihomo_rematch_outbounds: rematches,
      }, controller.signal).then(result => {
        setIssues(result.issues.filter(issue => issue.section && issue.section !== 'proxy_group'))
        onValidationChange?.(result.issues.some(issue => issue.level === 'error'))
      }).catch(error => {
        if (error?.code !== 'ERR_CANCELED') onValidationChange?.(true)
      }).finally(() => {
        if (!controller.signal.aborted) setChecking(false)
      })
    }, 300)
    return () => { window.clearTimeout(timer); controller.abort() }
  }, [serialized]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (selectedSubRuleIndex >= subRules.length) setSelectedSubRuleIndex(Math.max(0, subRules.length - 1))
  }, [subRules.length, selectedSubRuleIndex])

  const selectedIndex = selectedSubRuleIndex < subRules.length ? selectedSubRuleIndex : -1
  const selected = selectedIndex >= 0 ? subRules[selectedIndex] : undefined

  function patch(next: Partial<RuleSet>) {
    onChange({ ...value, ...next })
  }

  function nextSubRuleName() {
    const used = new Set(subRules.map(rule => rule.name))
    let index = subRules.length + 1
    while (used.has(`sub-rule-${index}`)) index++
    return `sub-rule-${index}`
  }

  function addSubRule() {
    const name = nextSubRuleName()
    patch({ mihomo_sub_rules: [...subRules, { name, content: '- MATCH,DIRECT' }] })
    setGeneration(g => g + 1)
    setSelectedSubRuleIndex(subRules.length)
  }

  function updateSubRule(index: number, next: MihomoSubRule) {
    patch({ mihomo_sub_rules: subRules.map((rule, i) => i === index ? next : rule) })
  }

  function removeSubRule(index: number) {
    patch({ mihomo_sub_rules: subRules.filter((_, i) => i !== index) })
    setGeneration(g => g + 1)
    if (index < selectedSubRuleIndex) setSelectedSubRuleIndex(selectedSubRuleIndex - 1)
    else if (index === selectedSubRuleIndex && selectedSubRuleIndex >= subRules.length - 1) setSelectedSubRuleIndex(Math.max(0, selectedSubRuleIndex - 1))
  }

  function addRematch() {
    patch({ mihomo_rematch_outbounds: [...rematches, { name: '', target_rematch_name: '', target_sub_rule: '' }] })
  }

  function updateRematch(index: number, next: MihomoRematchOutbound) {
    patch({ mihomo_rematch_outbounds: rematches.map((outbound, i) => i === index ? next : outbound) })
  }

  function issuesForRematch(index: number, outbound: MihomoRematchOutbound) {
    const name = outbound.name.trim()
    return issues.filter(issue => issue.section === 'rematch_outbound' && (
      issue.params?.index === index || (issue.params?.index == null && name !== '' && issue.name === name)
    ))
  }

  const issueText = (issue: ProxyGroupIssue) => t(`admin:rules.mihomo.issues.${issue.code}`, { ...issue.params, name: issue.name, defaultValue: issue.message })

  if (part === 'rematches') {
    return (
      <div className="flex flex-col gap-3">
        <LevelAlert level="warning">{t('admin:rules.mihomo.rematch_compatibility')}</LevelAlert>
        <p className="text-xs text-muted-foreground">{t('admin:rules.mihomo.rematches_hint')}</p>
        <div className="flex justify-end">
          <Button variant="outline" size="sm" onClick={addRematch}><IconAdd />{t('admin:rules.members.add')}</Button>
        </div>
        <ul className="flex flex-col gap-2">
          {rematches.map((outbound, index) => {
            const rowIssues = revealRematchIssues ? issuesForRematch(index, outbound) : []
            return (
              <li key={index} className="flex flex-col gap-2">
                <div className="grid grid-cols-1 items-end gap-3 rounded-lg border p-3 md:grid-cols-[1fr_1fr_1fr_auto]">
                  <Field label={t('admin:rules.mihomo.outbound_name')}>
                    <Input value={outbound.name} onChange={event => updateRematch(index, { ...outbound, name: event.target.value })} />
                  </Field>
                  <Field label={t('admin:rules.mihomo.target_rematch_name')}>
                    <Input value={outbound.target_rematch_name || ''} onChange={event => updateRematch(index, { ...outbound, target_rematch_name: event.target.value })} />
                  </Field>
                  <Field label={t('admin:rules.mihomo.target_sub_rule')}>
                    <NativeSelect value={outbound.target_sub_rule || ''} onChange={event => updateRematch(index, { ...outbound, target_sub_rule: event.target.value })}>
                      <option value="">—</option>
                      {subRules.filter(rule => rule.name.trim()).map(rule => <option key={rule.name} value={rule.name}>{rule.name}</option>)}
                    </NativeSelect>
                  </Field>
                  <IconButton label={`${t('common:actions.delete')} ${outbound.name || t('admin:rules.mihomo.outbound_name')}`} variant="ghost" className="justify-self-end text-destructive"
                    onClick={() => patch({ mihomo_rematch_outbounds: rematches.filter((_, i) => i !== index) })}>
                    <IconDelete />
                  </IconButton>
                </div>
                {rowIssues.map((issue, issueIndex) => <LevelAlert key={`${issue.code}:${issue.name}:${issueIndex}`} level={issue.level}>{issueText(issue)}</LevelAlert>)}
              </li>
            )
          })}
        </ul>
        {!rematches.length && <p className="py-6 text-center text-sm text-muted-foreground">{t('admin:rules.mihomo.no_rematches')}</p>}
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      <LevelAlert level="info">{t('admin:rules.mihomo.sub_rules_compatibility')}</LevelAlert>
      <p className="text-xs text-muted-foreground">{t('admin:rules.mihomo.sub_rules_hint')}</p>
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={addSubRule}><IconAdd />{t('admin:rules.members.add')}</Button>
          {checking && <IconSpinner aria-hidden className="size-4 animate-spin text-muted-foreground" />}
        </div>
        <span className="text-sm text-muted-foreground">{t('admin:rules.mihomo.sub_rules_count', { count: subRules.length })}</span>
      </div>
      <div className="grid min-h-[26rem] grid-cols-1 overflow-hidden rounded-lg border md:grid-cols-[15rem_minmax(0,1fr)]">
        <ul className="max-h-40 overflow-y-auto border-b bg-muted/30 md:max-h-none md:border-r md:border-b-0">
          {subRules.map((rule, index) => (
            <li key={`${rule.name}:${index}`} className={cn('flex items-center gap-1 border-b pr-1', index === selectedIndex && 'bg-accent')}>
              <button type="button" aria-current={index === selectedIndex ? 'true' : undefined} onClick={() => setSelectedSubRuleIndex(index)}
                className="min-w-0 flex-1 rounded-md px-3 py-2 text-left text-sm outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
                <span className="block truncate" title={rule.name}>{rule.name || t('admin:rules.mihomo.unnamed_sub_rule')}</span>
              </button>
              <IconButton label={`${t('common:actions.delete')} ${rule.name || t('admin:rules.mihomo.unnamed_sub_rule')}`} size="icon-xs" className="text-destructive" onClick={() => removeSubRule(index)}>
                <IconDelete />
              </IconButton>
            </li>
          ))}
        </ul>
        <div className="flex min-w-0 flex-col gap-2 p-3">
          {selected ? <>
            <Field label={t('admin:rules.mihomo.sub_rule_name')}>
              <Input value={selected.name} onChange={event => updateSubRule(selectedIndex, { ...selected, name: event.target.value })} />
            </Field>
            {renderContent ? (
              <div className="min-w-0">
                {renderContent({ index: selectedIndex, generation, value: selected.content, onChange: content => updateSubRule(selectedIndex, { ...selected, content }), ariaLabel: selected.name || t('admin:rules.mihomo.unnamed_sub_rule') })}
              </div>
            ) : (
              <div className="min-h-0 flex-1 overflow-hidden rounded-lg border">
                <Suspense fallback={<div className="grid h-80 place-items-center"><IconSpinner aria-hidden className="animate-spin text-muted-foreground" /></div>}>
                  <CodeEditor value={selected.content} onChange={content => updateSubRule(selectedIndex, { ...selected, content })} height="20rem" ariaLabel={selected.name || t('admin:rules.mihomo.unnamed_sub_rule')} />
                </Suspense>
              </div>
            )}
          </> : <p className="py-16 text-center text-sm text-muted-foreground">{t('admin:rules.mihomo.no_sub_rules')}</p>}
        </div>
      </div>
      <div className="flex flex-col gap-2">
        {issues.filter(issue => issue.section === 'sub_rule' || issue.section === 'rules').map((issue, index) => (
          <LevelAlert key={`${issue.code}:${issue.name}:${index}`} level={issue.level}>{issueText(issue)}</LevelAlert>
        ))}
      </div>
    </div>
  )
}
