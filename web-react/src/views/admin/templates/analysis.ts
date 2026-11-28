import type { RuleSetSummary } from '@/api/rules'
import type { Template } from '@/api/templates'

// What a template's proxy groups cover of the targets its bound rule sets send traffic to. These are read-only
// hints for the admin (a rule that points at a group the template does not define would break the client's
// config); they never change what is saved.

function normalizeTarget(raw: string) { return raw.trim().replace(/^['"]|['"]$/g, '') }

export function extractProxyGroups(content: string): Set<string> {
  const groups = new Set<string>()
  let inProxyGroups = false
  for (const line of content.split('\n')) {
    if (/^proxy-groups\s*:/.test(line)) { inProxyGroups = true; continue }
    if (inProxyGroups && /^\S/.test(line) && !/^proxy-groups\s*:/.test(line)) break
    const match = line.match(/^\s*-\s*name:\s*(.+?)\s*$/)
    if (inProxyGroups && match) groups.add(normalizeTarget(match[1]))
  }
  return groups
}

/** The template asks the panel to generate the proxy groups ({{ proxy_groups }} or {{ outbounds }}). */
export function usesDynamicProxyGroups(content: string) {
  return /\{\{\s*proxy_groups\s*\}\}/.test(content) || /\{\{\s*outbounds\s*\}\}/.test(content)
}

/**
 * The proxy-group names the enabled rule sets send traffic to. Each rule set's `targets` is derived by the server from its
 * rules text with the renderer's own parser (built-in exits, comments and `no-resolve` already left out), so the page never
 * holds or splits the text itself.
 */
export function extractRuleTargets(sets: readonly Pick<RuleSetSummary, 'enabled' | 'targets'>[]): Set<string> {
  const targets = new Set<string>()
  for (const rs of sets) {
    if (!rs.enabled) continue
    for (const target of rs.targets ?? []) targets.add(target)
  }
  return targets
}

export function missingTargets(groups: Set<string>, targets: Set<string>): string[] {
  if (groups.size === 0) return Array.from(targets)
  return Array.from(targets).filter(t => !groups.has(t))
}

export type Coverage =
  | { kind: 'none' }
  | { kind: 'auto' }
  | { kind: 'missing'; names: string[] }
  | { kind: 'ok' }

/** The "Targets" column: whether the template defines every group its bound rule sets point at. */
export function coverageOf(tpl: Pick<Template, 'rule_sets' | 'content'>, ruleSets: ReadonlyMap<string, RuleSetSummary>): Coverage {
  const sets = (tpl.rule_sets || []).map(s => ruleSets.get(s)).filter((x): x is RuleSetSummary => Boolean(x))
  if (sets.length === 0) return { kind: 'none' }
  if (usesDynamicProxyGroups(tpl.content)) return { kind: 'auto' }
  const names = missingTargets(extractProxyGroups(tpl.content), extractRuleTargets(sets))
  return names.length > 0 ? { kind: 'missing', names } : { kind: 'ok' }
}

export type FormHint = { tone: 'good' | 'bad' | 'muted'; key: string; params?: Record<string, unknown> }

/** The line under the rule-set picker while a template is edited (what the unsaved draft would cover). */
export function formHint(form: Pick<Template, 'rule_sets' | 'content'>, ruleSets: ReadonlyMap<string, RuleSetSummary>): FormHint | null {
  const selected = form.rule_sets.map(s => ruleSets.get(s)).filter((x): x is RuleSetSummary => Boolean(x))
  const groups = extractProxyGroups(form.content)
  const missing = missingTargets(groups, extractRuleTargets(selected))
  if (usesDynamicProxyGroups(form.content) && form.rule_sets.length > 0) return { tone: 'good', key: 'admin:templates.hint.auto_groups' }
  if (missing.length > 0 && form.content.length > 50) {
    return { tone: 'bad', key: 'admin:templates.hint.missing_targets', params: { names: missing.slice(0, 8).join('、') + (missing.length > 8 ? ` +${missing.length - 8}` : '') } }
  }
  if (form.rule_sets.length > 0 && groups.size > 0) return { tone: 'good', key: 'admin:templates.hint.groups_ok', params: { n: groups.size } }
  if (form.rule_sets.length === 0) return { tone: 'muted', key: 'admin:templates.hint.no_binding' }
  if (form.content.length < 50) return { tone: 'muted', key: 'admin:templates.hint.incomplete' }
  return null
}
