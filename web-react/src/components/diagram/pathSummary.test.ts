import '@/test/initI18n'
import i18n from 'i18next'
import { describe, expect, it } from 'vitest'
import { describePath, type PathEdge, type PathNode } from './pathSummary'

const en = i18n.getFixedT('en-US')
const zh = i18n.getFixedT('zh-CN')

const user: PathNode = { id: 'u', kind: 'user', label: '用户' }
const entry: PathNode = { id: 'e', kind: 'entry', label: 'Taiwan HiNet Dynamic' }
const landing: PathNode = { id: 'l', kind: 'landing', label: 'Irvine Home' }
const internet: PathNode = { id: 'i', kind: 'internet', label: '互联网' }
const solid: PathEdge = { style: 'solid' }
const dashed: PathEdge = { style: 'dashed' }

describe('describePath', () => {
  it('reads a port-forwarding line as one sentence (zh)', () => {
    expect(describePath([user, entry, landing], [solid, solid], zh))
      .toBe('用户 经 Taiwan HiNet Dynamic 端口转发 到 Irvine Home')
  })

  it('names the dashed hop an encrypted tunnel (zh)', () => {
    expect(describePath([user, entry, landing], [solid, dashed], zh))
      .toBe('用户 经 Taiwan HiNet Dynamic 加密隧道 到 Irvine Home')
  })

  it('reads in English too', () => {
    expect(describePath([{ ...user, label: 'User' }, entry, landing], [solid, solid], en))
      .toBe('User through Taiwan HiNet Dynamic by port forwarding to Irvine Home')
  })

  it('includes a port and an edge label, so what is on screen is also what is announced', () => {
    const s = describePath(
      [user, { ...entry, sublabel: ':10443' }, landing],
      [solid, { style: 'solid', label: '等待生效' }],
      zh,
    )
    expect(s).toBe('用户 经 Taiwan HiNet Dynamic（:10443） 端口转发（等待生效） 到 Irvine Home')
  })

  it('lets an edge carry its own name, e.g. a CDN back-to-origin hop', () => {
    const cdn: PathNode = { id: 'c', kind: 'cdn', label: 'Cloudflare' }
    expect(describePath([user, cdn, landing], [solid, { style: 'solid', name: '回源' }], zh))
      .toBe('用户 经 Cloudflare 回源 到 Irvine Home')
  })

  it('continues past the landing node and skips the phrase for an unnamed hop', () => {
    expect(describePath([user, entry, landing, internet], [solid, dashed, { style: 'solid', name: '' }], zh))
      .toBe('用户 经 Taiwan HiNet Dynamic 加密隧道 到 Irvine Home，再到 互联网')
  })

  it('handles the short cases', () => {
    expect(describePath([], [], zh)).toBe('没有路径')
    expect(describePath([landing], [], zh)).toBe('Irvine Home')
    expect(describePath([user, landing], [dashed], zh)).toBe('用户 经 加密隧道 到 Irvine Home')
    expect(describePath([user, landing], [{ style: 'solid', name: '' }], zh)).toBe('用户 到 Irvine Home')
  })
})
