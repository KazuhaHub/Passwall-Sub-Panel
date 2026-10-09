/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { cleanup, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { afterEach, expect, it } from 'vitest'
import { createAppTheme } from '@/theme'
import { flatten, I18N_STATIC_OPTIONS } from '@/i18n/options'
import en from '@/locales/en-US/admin.json'
import zh from '@/locales/zh-CN/admin.json'
import { destinationNode } from '@/test/accessControlFixtures'
import type { DestinationNodeStatus } from '@/api/accessControl'
import NodePolicyStatusRow from './NodePolicyStatusRow'

afterEach(cleanup)
async function mount(language: string, overrides: Partial<DestinationNodeStatus>) {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({ ...I18N_STATIC_OPTIONS, lng: language,
    resources: { 'en-US': { admin: flatten(en) }, 'zh-CN': { admin: flatten(zh) } } })
  return render(<I18nextProvider i18n={i18n}><MemoryRouter><ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language })}>
    <NodePolicyStatusRow node={destinationNode(overrides)} now={900000} />
  </ThemeProvider></MemoryRouter></I18nextProvider>)
}
it.each(['en-US', 'zh-CN'])('distinguishes fallback confirmation and never presents old rule counts as current in %s', async language => {
  const copy = language === 'zh-CN' ? zh.access_control.coverage : en.access_control.coverage
  await mount(language, { state: 'rejected', minted_kind: 'fallback', pending_since: 3000, applied_rules: 99 })
  expect(screen.getByText(copy.fallback_waiting)).toBeTruthy()
  expect(screen.queryByTestId('coverage-rules-1')).toBeNull()
  expect(screen.queryByText(copy.fallback_applied)).toBeNull()
  cleanup()
  await mount(language, { state: 'rejected', minted_kind: 'fallback', applied_rules: 8 })
  expect(screen.getByText(copy.fallback_applied)).toBeTruthy()
  expect(screen.getByText(copy.fallback_members)).toBeTruthy()
  expect(screen.getByTestId('coverage-rules-1').textContent).toContain('8')
  cleanup()
  await mount(language, { state: 'rejected', fallback_exhausted: true, minted_kind: 'empty', pending_since: 3000, applied_rules: 99 })
  expect(screen.getByText(copy.fallback_stopping)).toBeTruthy()
  expect(screen.queryByTestId('coverage-rules-1')).toBeNull()
  cleanup()
  await mount(language, { state: 'rejected', fallback_exhausted: true, minted_kind: 'empty', applied_rules: 99 })
  expect(screen.getByText(copy.fallback_exhausted)).toBeTruthy()
  expect(screen.getByTestId('coverage-rules-1').textContent).toBe(`${copy.executing_rules} 0`)
})
it.each(['en-US', 'zh-CN'])('explains a prolonged deployment, localizes quota names, and marks sing-box execution-only in %s', async language => {
  const copy = language === 'zh-CN' ? zh.access_control.coverage : en.access_control.coverage
  await mount(language, { state: 'pending', pending_since: 0 })
  expect(screen.getByText(copy.state_pending_long.replace('{{minutes}}', '15'))).toBeTruthy()
  expect(screen.getByText(copy.description_pending_long)).toBeTruthy()
  expect(screen.getByRole('link', { name: copy.issues }).getAttribute('href')).toBe('/admin/node-issues?agent=node-one')
  cleanup()
  await mount(language, { state: 'over_limit', over_limit: { kind: 'regexps', used: 130, limit: 128 } })
  expect(screen.getByText((language === 'zh-CN' ? zh : en).access_control.coverage.over_limit
    .replace('{{kind}}', (language === 'zh-CN' ? zh : en).access_control.quota.regexps).replace('{{used}}', '130').replace('{{limit}}', '128'))).toBeTruthy()
  cleanup()
  await mount(language, { state: 'applied', engine: 'sing-box' })
  expect(screen.getByText(copy.state_applied_execute_only)).toBeTruthy()
})
it.each(['en-US', 'zh-CN'])('has localized state labels, explanations and destination issue summaries in %s', language => {
  const dictionary = language === 'zh-CN' ? zh : en
  const coverage = dictionary.access_control.coverage as Record<string, string>
  for (const state of ['applied', 'pending', 'rejected', 'over_limit', 'sniffing', 'unsupported_version', 'unsupported_kind', 'offline', 'paused', 'none']) {
    expect(coverage[`state_${state}`]).toBeTruthy()
    if (state !== 'none') expect(coverage[`description_${state}`]).toBeTruthy()
  }
  for (const code of ['destination_policy_over_limit', 'destination_policy_rejected', 'destination_policy_lkg_rejected', 'destination_policy_sniffing_insufficient']) {
    expect((dictionary.node_issues.code_titles as Record<string, string>)[code]).toBeTruthy()
    expect((dictionary.node_issues.code_summaries as Record<string, string>)[code]).toBeTruthy()
  }
})
