// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'

import { translate } from '@/test/accessControlHarness'
import { enablePolicyConfirm } from './enableConfirm'

const t = (key: string, opts?: Record<string, unknown>) => translate(key, opts)
const base = { name: '禁止 BT', action: 'block' as const, state: 'on' as const }

describe('enablePolicyConfirm', () => {
  // The block and report-only rules sit behind the exempt list on the node, so exempt accounts are not reached; an allow policy is ahead of it and the count would mislead.
  it('says how many exempt accounts a block policy does not reach, and says nothing of it for an allow policy', () => {
    const scope = { scope: 'all' as const, groups: 1, users: 10, panels: 0, names: '', exempt: 2 }
    expect(enablePolicyConfirm({ t, minutes: 2, nodes: 1 }, base, scope).message).toContain('其中 2 个豁免账号不受影响')
    expect(enablePolicyConfirm({ t, minutes: 2, nodes: 1 }, { ...base, action: 'allow' }, scope).message).not.toContain('豁免')
  })

  it('does not claim a node count it cannot know', () => {
    const c = enablePolicyConfirm({ t, minutes: 2, nodes: null }, base, { scope: 'all', groups: 1, users: 1, panels: 0, names: '', exempt: 0 })
    expect(c.message).toContain('它所作用的节点会在')
  })

  it('names a policy for everyone by its groups, users and nodes, in words a person can weigh', () => {
    const c = enablePolicyConfirm({ t, minutes: 2, nodes: 3 }, base, { scope: 'all', groups: 4, users: 120, panels: 0, names: '', exempt: 0 })
    expect(c.title).toBe('启用策略「禁止 BT」？')
    expect(c.message).toContain('它会开始拦截匹配的连接')
    expect(c.message).toContain('所有用户，共 4 个分组、120 人')
    expect(c.message).toContain('最多 3 台节点会在约 2 分钟内')
    expect(c.confirmText).toBe('启用')
  })

  it('names the groups when the policy is scoped to some', () => {
    const c = enablePolicyConfirm({ t, minutes: 2, nodes: 1 }, base, { scope: 'groups', groups: 2, users: 50, panels: 0, names: '学生、员工', exempt: 0 })
    expect(c.message).toContain('2 个分组（学生、员工），共 50 人')
  })

  it('a policy scoped to servers names the servers, not users it cannot count', () => {
    const c = enablePolicyConfirm({ t, minutes: null, nodes: null }, base, { scope: 'panels', groups: 0, users: null, panels: 2, names: '东京 01、大阪', exempt: 0 })
    expect(c.message).toContain('经由所选 2 台服务器（东京 01、大阪）出口的连接')
    expect(c.message).not.toContain('人')
  })

  it('says report only does not block, and that an allow policy lifts the later blocks', () => {
    const scope = { scope: 'all' as const, groups: 1, users: 1, panels: 0, names: '', exempt: 0 }
    expect(enablePolicyConfirm({ t, minutes: 2, nodes: 1 }, { ...base, state: 'report' }, scope).message).toContain('不拦截任何连接')
    expect(enablePolicyConfirm({ t, minutes: 2, nodes: 1 }, { ...base, action: 'allow' }, scope).message).toContain('排在它后面的拦截规则和白名单拒绝不再对这些连接生效')
  })

  it('always starts on Cancel', () => {
    expect(enablePolicyConfirm({ t, minutes: 2, nodes: 1 }, base, { scope: 'all', groups: 1, users: 1, panels: 0, names: '', exempt: 0 }).initialFocus).toBe('cancel')
  })
})

describe('what the confirmation cannot know', () => {
  const none = { scope: 'all' as const, groups: null, users: null, panels: 0, names: '', exempt: null }

  // A read that has not answered must not read as "0 groups and 0 people".
  it('says the groups and people could not be counted instead of stating zero', () => {
    const c = enablePolicyConfirm({ t, minutes: 2, nodes: 1 }, base, none)
    expect(c.message).toContain('分组数和人数暂时无法确认')
    expect(c.message).not.toContain('0 个分组')
    expect(c.message).not.toContain('0 人')
  })

  it('a group-scoped policy still knows its own groups, but not their people', () => {
    const c = enablePolicyConfirm({ t, minutes: 2, nodes: 1 }, base, { ...none, scope: 'groups', groups: 2, names: '学生、员工' })
    expect(c.message).toContain('2 个分组（学生、员工），人数暂时无法确认')
  })

  it('says the exempt count could not be read for a block policy, and nothing of it for an allow policy', () => {
    expect(enablePolicyConfirm({ t, minutes: 2, nodes: 1 }, base, none).message).toContain('豁免账号数暂时无法确认')
    expect(enablePolicyConfirm({ t, minutes: 2, nodes: 1 }, { ...base, action: 'allow' }, none).message).not.toContain('豁免')
  })

  // The exempt count is of every exempt account, so a policy that reaches some groups says it as a total.
  it('words the exempt count as a total when the policy reaches only some groups', () => {
    const c = enablePolicyConfirm({ t, minutes: 2, nodes: 1 }, base, { scope: 'groups', groups: 1, users: 5, panels: 0, names: '学生', exempt: 3 })
    expect(c.message).toContain('这些分组里的豁免账号不受影响（豁免账号共 3 个）')
  })
})
