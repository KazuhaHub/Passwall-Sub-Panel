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
  it('reads a port-forwarding line as one sentence (zh), spaced only where Chinese meets Latin', () => {
    expect(describePath([user, entry, landing], [solid, solid], zh))
      .toBe('用户经 Taiwan HiNet Dynamic 端口转发到 Irvine Home')
  })

  it('names the dashed hop an encrypted tunnel (zh)', () => {
    expect(describePath([user, entry, landing], [solid, dashed], zh))
      .toBe('用户经 Taiwan HiNet Dynamic 加密隧道到 Irvine Home')
  })

  it('reads in English too', () => {
    expect(describePath([{ ...user, label: 'User' }, entry, landing], [solid, solid], en))
      .toBe('User through Taiwan HiNet Dynamic by port forwarding to Irvine Home')
  })

  it('includes a port and an edge label, so what is on screen is also what is announced', () => {
    expect(describePath([user, { ...entry, sublabel: ':10443' }, landing], [solid, { style: 'solid', label: '等待生效' }], zh))
      .toBe('用户经 Taiwan HiNet Dynamic（:10443）端口转发（等待生效）到 Irvine Home')
  })

  it('lets an edge carry its own name, e.g. a CDN back-to-origin hop', () => {
    const cdn: PathNode = { id: 'c', kind: 'cdn', label: 'Cloudflare' }
    expect(describePath([user, cdn, landing], [solid, { style: 'solid', name: '回源' }], zh))
      .toBe('用户经 Cloudflare 回源到 Irvine Home')
  })

  it('continues past the landing node with 再经 / 再到, and skips the phrase for an unnamed hop', () => {
    expect(describePath([user, entry, landing, internet], [solid, dashed, { style: 'solid', name: '' }], zh))
      .toBe('用户经 Taiwan HiNet Dynamic 加密隧道到 Irvine Home，再到互联网')
    expect(describePath([user, entry, landing, internet], [solid, solid, dashed], zh))
      .toBe('用户经 Taiwan HiNet Dynamic 端口转发到 Irvine Home，再经加密隧道到互联网')
  })

  it('handles the short cases', () => {
    expect(describePath([], [], zh)).toBe('没有路径')
    expect(describePath([landing], [], zh)).toBe('Irvine Home')
    expect(describePath([user, landing], [dashed], zh)).toBe('用户经加密隧道到 Irvine Home')
    expect(describePath([user, landing], [{ style: 'solid', name: '' }], zh)).toBe('用户到 Irvine Home')
  })

  describe('status (a broken line must not be announced as healthy)', () => {
    it('states a node that is down or needs attention', () => {
      expect(describePath([user, entry, { ...landing, status: 'danger' }], [solid, solid], zh))
        .toBe('用户经 Taiwan HiNet Dynamic 端口转发到 Irvine Home（已中断）')
      expect(describePath([user, { ...entry, sublabel: ':10443', status: 'warning' }, landing], [solid, solid], zh))
        .toBe('用户经 Taiwan HiNet Dynamic（:10443，需要留意）端口转发到 Irvine Home')
    })

    it('states an edge status even when the edge has no label', () => {
      expect(describePath([user, entry, landing], [solid, { style: 'solid', status: 'danger' }], zh))
        .toBe('用户经 Taiwan HiNet Dynamic 端口转发（已中断）到 Irvine Home')
    })

    it('states both the label and the status of an edge', () => {
      expect(describePath([user, entry, landing], [solid, { style: 'solid', label: '等待生效', status: 'warning' }], zh))
        .toBe('用户经 Taiwan HiNet Dynamic 端口转发（等待生效，需要留意）到 Irvine Home')
    })

    it('does not leave out a bad first hop that the sentence structure skips', () => {
      expect(describePath([user, entry, landing], [{ style: 'solid', status: 'danger' }, solid], zh))
        .toBe('用户经 Taiwan HiNet Dynamic 端口转发到 Irvine Home；用户到 Taiwan HiNet Dynamic 这一段已中断')
    })

    it('says it in English too', () => {
      expect(describePath([{ ...user, label: 'User' }, entry, { ...landing, status: 'danger' }], [solid, solid], en))
        .toBe('User through Taiwan HiNet Dynamic by port forwarding to Irvine Home (failing)')
    })

    it('treats success and neutral as normal: nothing is added', () => {
      expect(describePath([user, { ...entry, status: 'success' }, landing], [solid, { style: 'solid', status: 'neutral' }], zh))
        .toBe('用户经 Taiwan HiNet Dynamic 端口转发到 Irvine Home')
    })
  })

  describe('an edge with a label but no name (a bare latency)', () => {
    it('attaches the label to the destination instead of reading "经 142 ms 到"', () => {
      expect(describePath([user, landing], [{ style: 'solid', name: '', label: '142 ms' }], zh))
        .toBe('用户到 Irvine Home（142 ms）')
      expect(describePath([user, entry, landing], [solid, { style: 'solid', name: '', label: '48 ms' }], zh))
        .toBe('用户经 Taiwan HiNet Dynamic 到 Irvine Home（48 ms）')
      expect(describePath([{ ...user, label: 'User' }, landing], [{ style: 'solid', name: '', label: '142 ms' }], en))
        .toBe('User to Irvine Home (142 ms)')
    })
  })
})
