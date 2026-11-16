// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'

import { translate } from '@/test/accessControlHarness'
import { allowlistBackToReportConfirm, allowlistNoNodesConfirm, allowlistOffConfirm, allowlistRemoveListsConfirm } from './confirmCopy'

const t = (k: string, o?: Record<string, unknown>) => translate(k, o)

describe('the allowlist confirmations', () => {
  it('back to report-only only loosens: a plain button, the minutes named', () => {
    const c = allowlistBackToReportConfirm({ t, minutes: 2 }, 'Guests')
    expect(c.title).toBe('让「Guests」回到仅报告？')
    expect(c.message).toBe('成员访问名单外的目的地不再被拒绝，只记录。约 2 分钟内生效。')
    expect(c.confirmText).toBe('改回仅报告')
    expect(c.destructive).toBeUndefined()
  })

  it('turning off an allowlist that is on says what comes back and that the lists stay', () => {
    const c = allowlistOffConfirm({ t, minutes: null }, 'Guests', 4, 'on')
    expect(c.title).toBe('关闭「Guests」的白名单？')
    expect(c.message).toBe('本组成员恢复访问所有目的地（仍受全局策略约束），并在几分钟内重新获得 4 台此前不开放的节点。名单保留，以后可以再次开启。')
    expect(c.confirmText).toBe('关闭白名单')
    expect(c.destructive).toBeUndefined()
  })

  it('no node comes back, no "0 nodes" is promised', () => {
    expect(allowlistOffConfirm({ t, minutes: 2 }, 'Guests', 0, 'on').message).toBe('本组成员恢复访问所有目的地（仍受全局策略约束），约 2 分钟内生效。名单保留，以后可以再次开启。')
  })

  it('turning off a report-only allowlist loosens nothing: it only stops the recording', () => {
    const c = allowlistOffConfirm({ t, minutes: 2 }, 'Guests', 3, 'report')
    expect(c.message).toBe('本组成员本来就能访问所有目的地；关闭后不再记录本应拒绝的目的地，并在约 2 分钟内重新获得 3 台此前不开放的节点。名单保留，以后可以再次开启。')
    expect(allowlistOffConfirm({ t, minutes: 2 }, 'Guests', 0, 'report').message).toBe('本组成员本来就能访问所有目的地；关闭后不再记录本应拒绝的目的地。约 2 分钟内生效。名单保留，以后可以再次开启。')
  })

  it('no usable node is worded for the state it is asked in', () => {
    const on = allowlistNoNodesConfirm(t, 'Guests', 51, 12, 'on')
    expect(on.title).toBe('开启后「Guests」的成员将没有任何可用节点？')
    expect(on.message).toBe('本组 51 人在开启后就拿不到任何节点：12 台节点都不能执行白名单。通常应先升级或修好节点。')
    expect(on.confirmText).toBe('仍然开启')
    expect(on.destructive).toBe(true)
    const report = allowlistNoNodesConfirm(t, 'Guests', 51, 12, 'report')
    expect(report.title).toBe('开始仅报告后「Guests」的成员将没有任何可用节点？')
    expect(report.message).toBe('本组 51 人在开始仅报告后就拿不到任何节点（仅报告同样不对不能执行的节点开放）：12 台节点都不能执行白名单。通常应先升级或修好节点。')
    expect(report.confirmText).toBe('仍然开始')
  })

  it('taking lists out of an allowlist that is on names them', () => {
    const c = allowlistRemoveListsConfirm(t, 'Ops', ['Remote feed', 'Mine'])
    expect(c.title).toBe('从「Ops」的名单里去掉 Remote feed、Mine？')
    expect(c.message).toBe('它们覆盖的目的地中，不在其他名单里的会被拒绝。')
    expect(c.confirmText).toBe('去掉')
    expect(c.destructive).toBe(true)
  })
})
