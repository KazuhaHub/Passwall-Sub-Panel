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
