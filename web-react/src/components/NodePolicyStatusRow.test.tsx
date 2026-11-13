// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { renderAccess, setTestLang, translate } from '@/test/accessControlHarness'
import type { DestNodeStatus } from '@/api/accessControl'
import NodePolicyStatusRow, { nodeStateText } from './NodePolicyStatusRow'

const NOW = 1_700_000_000_000
const node = (over: Partial<DestNodeStatus> = {}): DestNodeStatus => ({
  panel_id: 1, agent_id: 'a1', panel_name: 'Tokyo 01', kind: 'psp', engine: 'xray', agent_version: '4', supports: { policy: true, hits: false, usage: false },
  collect: '', collect_effective: '', collecting: false, state: 'applied', fallback_reason: '', over_limit: null, sniffing_insufficient: [],
  minted_at: null, pending_since: null, applied_at: NOW - 120_000, applied_rules: 9, last_report_at: null, rules_data_version: null,
  geoip_unavailable: false, allowlist_groups: [], hits_24h: null, ...over,
})

const STATES: Partial<DestNodeStatus>[] = [
  { state: 'applied' }, { state: 'applied', engine: 'sing-box' }, { state: 'pending', pending_since: NOW - 1000 },
  { state: 'pending', pending_since: NOW - 11 * 60_000 }, { state: 'rejected' },
  { state: 'sniffing', sniffing_insufficient: [{ listener: 'l', label: 'vless-443', node_id: 7 }] },
  { state: 'over_limit', over_limit: { kind: 'domains', used: 57312, limit: 50000 } }, { state: 'unsupported_version' },
  { state: 'unsupported_kind', kind: 'x-ui' }, { state: 'offline' }, { state: 'none' }, { state: 'paused' },
]

describe('node state words', () => {
  for (const lang of ['zh-CN', 'en-US'] as const) {
    it.each(STATES)(`%j resolves to words, never a raw key (${lang})`, over => {
      setTestLang(lang)
      const t = (k: string, o?: Record<string, unknown>) => translate(k, o)
      const { label, note } = nodeStateText(t, node(over), NOW)
      for (const s of [label, note]) {
        expect(s).not.toMatch(/access_control\./)
        expect(s).not.toMatch(/\{\{/)
        expect(s.length).toBeGreaterThan(0)
      }
    })
  }
})

describe('NodePolicyStatusRow', () => {
  it('rejected: offers Apply again, which retries the agent', async () => {
    const onRetry = vi.fn()
    renderAccess(<NodePolicyStatusRow node={node({ state: 'rejected' })} variant="row" now={NOW} onRetry={onRetry} />)
    expect(screen.getByText('节点拒绝了新策略')).toBeTruthy()
    expect(screen.getByText(/新加入作用分组的成员/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '重新下发' }))
    await waitFor(() => expect(onRetry).toHaveBeenCalledWith('a1'))
  })

  it('sniffing: names the inbound and links to editing it', () => {
    const onOpenInbound = vi.fn()
    renderAccess(<NodePolicyStatusRow node={node({ state: 'sniffing', sniffing_insufficient: [{ listener: 'l', label: 'vless-443', node_id: 7 }] })}
      variant="block" now={NOW} onOpenInbound={onOpenInbound} />)
    expect(screen.getAllByText(/vless-443/).length).toBeGreaterThan(1)
    fireEvent.click(screen.getByRole('button', { name: '修改入站' }))
    expect(onOpenInbound).toHaveBeenCalledWith(7)
  })

  it('3X-UI is the quiet "not applicable", not an alarm; sing-box reads enforce-only', () => {
    renderAccess(<NodePolicyStatusRow node={node({ state: 'unsupported_kind', kind: 'x-ui' })} variant="row" now={NOW} />)
    expect(screen.getByTestId('node-state-badge').getAttribute('data-state')).toBe('unsupported_kind')
    expect(screen.getByText('不适用')).toBeTruthy()
  })
})
