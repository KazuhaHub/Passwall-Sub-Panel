// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'

import { translate } from '@/test/accessControlHarness'
import { allowlistBackToReportConfirm, allowlistNoNodesConfirm, allowlistOffConfirm } from './confirmCopy'

const t = (k: string, o?: Record<string, unknown>) => translate(k, o)

describe('the allowlist confirmations', () => {
  it('back to report-only only loosens: a plain button, the minutes named', () => {
    const c = allowlistBackToReportConfirm({ t, minutes: 2 }, 'Guests')
    expect(c.title).toBe('让「Guests」回到仅报告？')
    expect(c.message).toBe('成员访问名单外的目的地不再被拒绝，只记录。约 2 分钟内生效。')
    expect(c.confirmText).toBe('改回仅报告')
    expect(c.destructive).toBeUndefined()
  })

  it('turning it off says what comes back and that the lists stay', () => {
    const c = allowlistOffConfirm({ t, minutes: null }, 'Guests', 4)
    expect(c.title).toBe('关闭「Guests」的白名单？')
    expect(c.message).toBe('本组成员恢复访问所有目的地（仍受全局策略约束），并在几分钟内重新获得 4 台此前不开放的节点。名单保留，以后可以再次开启。')
    expect(c.confirmText).toBe('关闭白名单')
    expect(c.destructive).toBeUndefined()
  })

  it('no node left is the one destructive question, and names who is locked out', () => {
    const c = allowlistNoNodesConfirm(t, 'Guests', 51, 12)
    expect(c.title).toBe('开启后「Guests」的成员将没有任何可用节点？')
    expect(c.message).toBe('本组 51 人在开启后就拿不到任何节点：12 台节点都不执行白名单（3X-UI、S-UI 或版本过旧）。通常应先升级原生节点。')
    expect(c.confirmText).toBe('仍然开启')
    expect(c.destructive).toBe(true)
  })
})
