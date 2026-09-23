// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, editRow, installReads, list, mount, snack } from '@/test/adminSaveHarness'
import RuleSetsView from './RuleSetsView'

const confirmMock = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmMock }))

const row = { slug: 'custom', name: 'old-name', sort: 1, enabled: true, direct_subscription_domain: false, proxy_group_order: [], content: 'rules: []' }

it('reopens with the saved rule without reloading the stale list', async () => {
  installReads({ '/admin/rules': list([row]) })
  api.post.mockResolvedValue({ data: { groups: [], builtins: [], nodes: [], regions: [], tags: [], issues: [] } })
  api.put.mockImplementation(async (_url, body) => ({ data: { ...body, name: 'server-normalized-name' } }))
  mount(<RuleSetsView />)
  const dialog = await editRow()
  fireEvent.change(within(dialog).getByDisplayValue('old-name'), { target: { value: 'new-name' } })

  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))

  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  const reopened = await editRow('server-normalized-name')
  expect(within(reopened).getByDisplayValue('server-normalized-name')).toBeTruthy()
})

it('confirms and removes metadata for a group deleted from rule content', async () => {
  const orphanRow = {
    ...row,
    content: '- DOMAIN,a,Keep\n- GEOSITE,googlefcm,📣 谷歌FCM',
    proxy_group_order: ['📣 谷歌FCM', 'Keep'],
    proxy_group_members: {
      '📣 谷歌FCM': [{ kind: 'builtin' as const, value: 'DIRECT' }],
      Keep: [{ kind: 'node_set' as const, value: 'remaining' }],
    },
    proxy_group_options: { '📣 谷歌FCM': { type: 'fallback' as const } },
  }
  const inspection = {
    groups: [{ name: 'Keep', configured: true, options_configured: false, options: { type: 'select' }, default_members: [], members: [], preview: [] }],
    builtins: [], nodes: [], regions: [], tags: [], issues: [],
  }
  installReads({ '/admin/rules': list([orphanRow]) })
  api.post.mockResolvedValue({ data: inspection })
  api.put.mockImplementation(async (_url, body) => ({ data: body }))
  confirmMock.mockResolvedValue(true)
  mount(<RuleSetsView />)

  const dialog = await editRow()
  fireEvent.change(within(dialog).getByLabelText('code'), { target: { value: '- DOMAIN,a,Keep' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))

  await waitFor(() => expect(confirmMock).toHaveBeenCalledTimes(1))
  expect(confirmMock.mock.calls[0][0]).toMatchObject({
    title: 'admin:rules.confirm.cleanup_groups_title', destructive: true,
  })
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  const saved = api.put.mock.calls[0][1]
  expect(saved.proxy_group_order).toEqual(['Keep'])
  expect(saved.proxy_group_members).toEqual({ Keep: [{ kind: 'node_set', value: 'remaining' }] })
  expect(saved.proxy_group_options).toEqual({})
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})

it('keeps the edited draft and does not save when orphan cleanup is canceled', async () => {
  const orphanRow = {
    ...row,
    content: '- DOMAIN,a,Keep\n- MATCH,Removed',
    proxy_group_order: ['Removed', 'Keep'],
    proxy_group_members: { Removed: [{ kind: 'builtin' as const, value: 'DIRECT' }] },
  }
  const inspection = {
    groups: [{ name: 'Keep', configured: false, options_configured: false, options: { type: 'select' }, default_members: [], members: [], preview: [] }],
    builtins: [], nodes: [], regions: [], tags: [], issues: [],
  }
  installReads({ '/admin/rules': list([orphanRow]) })
  api.post.mockResolvedValue({ data: inspection })
  confirmMock.mockResolvedValue(false)
  mount(<RuleSetsView />)

  const dialog = await editRow()
  fireEvent.change(within(dialog).getByLabelText('code'), { target: { value: '- DOMAIN,a,Keep' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))

  await waitFor(() => expect(confirmMock).toHaveBeenCalledTimes(1))
  expect(api.put).not.toHaveBeenCalled()
  expect((within(dialog).getByLabelText('code') as HTMLTextAreaElement).value).toBe('- DOMAIN,a,Keep')
})

it('still blocks saving when a surviving group has an error after cleanup', async () => {
  const invalidRow = {
    ...row,
    content: '- DOMAIN,a,Keep\n- MATCH,Removed',
    proxy_group_order: ['Removed', 'Keep'],
    proxy_group_members: {
      Removed: [{ kind: 'builtin' as const, value: 'DIRECT' }],
      Keep: [{ kind: 'proxy_group' as const, value: 'Missing' }],
    },
  }
  const group = { name: 'Keep', configured: true, options_configured: false, options: { type: 'select' }, default_members: [], members: [], preview: [] }
  const beforeCleanup = { groups: [group], builtins: [], nodes: [], regions: [], tags: [], issues: [] }
  const afterCleanup = {
    ...beforeCleanup,
    issues: [{ level: 'error', group: 'Keep', code: 'missing_group', message: 'missing' }],
  }
  installReads({ '/admin/rules': list([invalidRow]) })
  api.post.mockResolvedValueOnce({ data: beforeCleanup }).mockResolvedValue({ data: afterCleanup })
  confirmMock.mockResolvedValue(true)
  mount(<RuleSetsView />)

  const dialog = await editRow()
  fireEvent.change(within(dialog).getByLabelText('code'), { target: { value: '- DOMAIN,a,Keep' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))

  await waitFor(() => expect(snack).toHaveBeenCalledWith('admin:rules.validate.proxy_group_members', 'warning'))
  expect(api.put).not.toHaveBeenCalled()
  expect(within(dialog).getByRole('tab', { name: 'admin:rules.tabs.members' }).getAttribute('aria-selected')).toBe('true')
})

it('reports a failed read instead of showing an empty rule-set list', async () => {
  // The loader had no catch, so a failure raised an unhandled rejection and the
  // table rendered with no rule sets — indistinguishable from "none exist".
  api.get.mockRejectedValue(new Error('offline'))
  mount(<RuleSetsView />)

  await waitFor(() => expect(screen.getByText('admin:rules.load_failed')).toBeTruthy())
})

it('edits and saves Mihomo main rules, sub-rules, and Rematch outbounds', async () => {
  const advancedRow = {
    ...row,
    mihomo_rules: '- DOMAIN,ai.example,AI Rematch',
    mihomo_sub_rules: [{ name: 'ai-rules', content: '- MATCH,DIRECT' }],
    mihomo_rematch_outbounds: [{ name: 'AI Rematch', target_rematch_name: 'ai', target_sub_rule: 'ai-rules' }],
  }
  installReads({ '/admin/rules': list([advancedRow]) })
  api.post.mockResolvedValue({ data: { groups: [], builtins: [], nodes: [], regions: [], tags: [], issues: [] } })
  api.put.mockImplementation(async (_url, body) => ({ data: body }))
  mount(<RuleSetsView />)

  const dialog = await editRow()
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:rules.tabs.mihomo' }))
  await waitFor(() => expect(within(dialog).getAllByLabelText('code')).toHaveLength(2))
  expect(within(dialog).getByDisplayValue('AI Rematch')).toBeTruthy()
  expect((within(dialog).getByLabelText('admin:rules.mihomo.sub_rule_name') as HTMLInputElement).value).toBe('ai-rules')

  fireEvent.change(within(dialog).getAllByLabelText('code')[0], { target: { value: '- DOMAIN,new.example,AI Rematch' } })
  fireEvent.change(within(dialog).getByLabelText('admin:rules.mihomo.target_rematch_name'), { target: { value: 'ai-v2' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put.mock.calls[0][1]).toMatchObject({
    mihomo_rules: '- DOMAIN,new.example,AI Rematch',
    mihomo_sub_rules: [{ name: 'ai-rules', content: '- MATCH,DIRECT' }],
    mihomo_rematch_outbounds: [{ name: 'AI Rematch', target_rematch_name: 'ai-v2', target_sub_rule: 'ai-rules' }],
  })
})
