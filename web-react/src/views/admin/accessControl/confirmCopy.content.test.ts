import { createInstance, type TFunction } from 'i18next'
import { expect, it, vi } from 'vitest'
import enAdmin from '@/locales/en-US/admin.json'
import enCommon from '@/locales/en-US/common.json'
import zhAdmin from '@/locales/zh-CN/admin.json'
import zhCommon from '@/locales/zh-CN/common.json'
import { deleteListCopy, deletePolicyCopy, pauseExecutionCopy } from './confirmCopy'

async function translator(lng: 'en-US' | 'zh-CN') {
  const instance = createInstance()
  await instance.init({ lng, fallbackLng: false, resources: { 'en-US': { admin: enAdmin, common: enCommon }, 'zh-CN': { admin: zhAdmin, common: zhCommon } }, interpolation: { escapeValue: false } })
  return instance.t
}

it.each(['en-US', 'zh-CN'] as const)('explains preserved definitions and whitelist membership when pausing in %s', async lng => {
  const copy = pauseExecutionCopy(await translator(lng), true, 60001)
  for (const text of lng === 'zh-CN' ? ['策略和白名单', '定义全部保留', '不会因此获得更多节点', '约 2 分钟'] : ['policies and allowlists', 'Definitions are retained', 'do not gain more nodes', 'about 2 minutes']) expect(copy.message).toContain(text)
  expect(copy.destructive).toBe(true)
  expect(copy.message).not.toContain('{{')
})

it.each(['en-US', 'zh-CN'] as const)('explains both policy and whitelist resumption with server ETA in %s', async lng => {
  const copy = pauseExecutionCopy(await translator(lng), false, 60001)
  for (const text of lng === 'zh-CN' ? ['策略和白名单', '约 2 分钟'] : ['policies and allowlists', 'about 2 minutes']) expect(copy.message).toContain(text)
  expect(copy.destructive).toBe(false)
})

it.each(['en-US', 'zh-CN'] as const)('explains retained historical hits after policy deletion in %s', async lng => {
  const copy = deletePolicyCopy(await translator(lng), 'Finance', 60001)
  for (const text of lng === 'zh-CN' ? ['历史命中保留', '已删除的策略', '约 2 分钟'] : ['Historical hits are retained', 'Deleted policy', 'about 2 minutes']) expect(copy.message).toContain(text)
  expect(copy.title).toContain('Finance')
  expect(copy.destructive).toBe(true)
})

it.each(['en-US', 'zh-CN'] as const)('explains why deleting an unused list does not affect nodes in %s', async lng => {
  const copy = deleteListCopy(await translator(lng), 'Unused')
  for (const text of lng === 'zh-CN' ? ['没有被使用', '不会影响节点'] : ['not in use', 'does not affect nodes']) expect(copy.message).toContain(text)
})

it.each(['delete', 'pause', 'resume'] as const)('uses a server ETA or the explicit unknown fallback for %s', action => {
  for (const etaMs of [undefined, 60001]) {
    const t = vi.fn((key: string) => key)
    const translate = t as unknown as TFunction
    if (action === 'delete') deletePolicyCopy(translate, 'Finance', etaMs)
    else pauseExecutionCopy(translate, action === 'pause', etaMs)
    const key = action === 'delete' ? 'admin:access_control.policies.delete_message' : `admin:access_control.confirm.${action}_message`
    expect(t).toHaveBeenCalledWith(key, { eta: `admin:access_control.confirm.${etaMs === undefined ? 'eta_unknown' : 'eta_minutes'}` })
    if (etaMs !== undefined) expect(t).toHaveBeenCalledWith('admin:access_control.confirm.eta_minutes', { minutes: 2 })
  }
})
