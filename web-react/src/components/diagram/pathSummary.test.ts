import '@/test/initI18n'
import i18n from 'i18next'
import { describe, expect, it } from 'vitest'
import { describePath, type PathEdge, type PathNode, type PathTone } from './pathSummary'

const en = i18n.getFixedT('en-US')
const zh = i18n.getFixedT('zh-CN')

const user: PathNode = { id: 'u', kind: 'user' }
const entry: PathNode = { id: 'e', kind: 'entry', label: 'Taiwan HiNet Dynamic' }
const landing: PathNode = { id: 'l', kind: 'landing', label: 'Irvine Home' }
const internet: PathNode = { id: 'i', kind: 'internet' }
const solid: PathEdge = { style: 'solid' }
const dashed: PathEdge = { style: 'dashed' }

describe('describePath grammar', () => {
  it('reads a port-forwarding line as one sentence', () => {
    expect(describePath([user, entry, landing], [solid, solid], zh)).toBe('用户经 Taiwan HiNet Dynamic 端口转发到 Irvine Home')
    expect(describePath([user, entry, landing], [solid, solid], en))
      .toBe('User through Taiwan HiNet Dynamic by port forwarding to Irvine Home')
  })

  it('names the dashed hop an encrypted tunnel', () => {
    expect(describePath([user, entry, landing], [solid, dashed], zh)).toBe('用户经 Taiwan HiNet Dynamic 加密隧道到 Irvine Home')
  })

  it('includes a port and an edge label', () => {
    expect(describePath([user, { ...entry, sublabel: ':10443' }, landing], [solid, { style: 'solid', label: '48 ms' }], zh))
      .toBe('用户经 Taiwan HiNet Dynamic（:10443）端口转发（48 ms）到 Irvine Home')
  })

  it('lets an edge carry its own name, and says a CDN or an external entry is one', () => {
    const cdn: PathNode = { id: 'c', kind: 'cdn', label: 'Cloudflare' }
    expect(describePath([user, cdn, landing], [solid, { style: 'solid', role: 'origin' }], zh)).toBe('用户经 Cloudflare（CDN）回源到 Irvine Home')
    const ext: PathNode = { id: 'x', kind: 'external', label: 'Relay A', sublabel: ':8443' }
    expect(describePath([user, ext, landing], [solid, { style: 'solid', name: '' }], zh)).toBe('用户经 Relay A（外部入口，:8443）到 Irvine Home')
    expect(describePath([user, cdn, landing], [solid, { style: 'solid', role: 'origin' }], en))
      .toBe('User through Cloudflare (CDN) by origin pull to Irvine Home')
  })

  it('tells a direct line from a relay: the user meets the landing node itself', () => {
    expect(describePath([user, { ...landing, sublabel: ':443' }], [{ style: 'solid', name: '' }], zh)).toBe('用户直连 Irvine Home（:443）')
    expect(describePath([user, landing], [{ style: 'solid', name: '' }], en)).toBe('User connects directly to Irvine Home')
  })

  it('tells the exit from the entry: after the landing node it is "出网", not another relay', () => {
    const exit: PathNode = { id: 'x', kind: 'exit', label: 'Tokyo Exit' }
    expect(describePath([user, { ...landing, sublabel: ':443' }, exit, internet], [{ style: 'solid', name: '' }, solid, solid], zh))
      .toBe('用户直连 Irvine Home（:443），再经出口服务器 Tokyo Exit 出网')
    expect(describePath([user, entry, landing, exit, internet], [solid, solid, dashed, solid], zh))
      .toBe('用户经 Taiwan HiNet Dynamic 端口转发到 Irvine Home，再经加密隧道到出口服务器 Tokyo Exit 出网')
    expect(describePath([user, entry, landing, internet], [solid, dashed, { style: 'solid', name: '' }], zh))
      .toBe('用户经 Taiwan HiNet Dynamic 加密隧道到 Irvine Home，再到互联网')
  })

  it('attaches the label of an unnamed hop to the node it leads to', () => {
    expect(describePath([user, landing], [{ style: 'solid', name: '', label: '142 ms' }], zh)).toBe('用户直连 Irvine Home（142 ms）')
    expect(describePath([user, entry, landing], [solid, { style: 'solid', name: '', label: '48 ms' }], zh))
      .toBe('用户经 Taiwan HiNet Dynamic 到 Irvine Home（48 ms）')
  })

  it('handles the short cases', () => {
    expect(describePath([], [], zh)).toBe('没有路径')
    expect(describePath([landing], [], zh)).toBe('Irvine Home')
    expect(describePath([user, entry], [dashed], zh)).toBe('用户经加密隧道到 Taiwan HiNet Dynamic')
  })
})

describe('status is spoken once, not inside the brackets', () => {
  const tag = (nodes: PathNode[], status: PathTone, which: number[]) => nodes.map((n, i) => (which.includes(i) ? { ...n, status } : n))

  it('says the whole line when every element between the ends has the status', () => {
    const nodes = tag([user, entry, landing], 'pending', [1, 2])
    expect(describePath(nodes, [solid, { style: 'solid', status: 'pending' }], zh)).toBe('用户经 Taiwan HiNet Dynamic 端口转发到 Irvine Home；整条线路等待生效')
    expect(describePath(nodes, [solid, { style: 'solid', status: 'pending' }], en))
      .toBe('User through Taiwan HiNet Dynamic by port forwarding to Irvine Home; the whole line: waiting to take effect')
  })

  it('lists only the elements that have it, nodes by name and edges as a hop', () => {
    expect(describePath(tag([user, entry, landing], 'danger', [2]), [solid, solid], zh))
      .toBe('用户经 Taiwan HiNet Dynamic 端口转发到 Irvine Home；Irvine Home 已中断')
    expect(describePath([user, entry, landing], [{ style: 'solid', status: 'danger' }, solid], zh))
      .toBe('用户经 Taiwan HiNet Dynamic 端口转发到 Irvine Home；用户到 Taiwan HiNet Dynamic 这一段已中断')
    expect(describePath([user, entry, landing], [solid, { style: 'solid', status: 'warning' }], zh))
      .toBe('用户经 Taiwan HiNet Dynamic 端口转发到 Irvine Home；Taiwan HiNet Dynamic 到 Irvine Home 这一段需要留意')
  })

  it('does not repeat a node\'s own brackets in the status clause, and reads as English', () => {
    const nodes = tag([user, { ...entry, sublabel: ':1' }, { ...landing, sublabel: ':443' }], 'warning', [2])
    const s = describePath(nodes, [{ style: 'solid', status: 'warning' }, solid], en)
    expect(s).toBe('User through Taiwan HiNet Dynamic (:1) by port forwarding to Irvine Home (:443); Irvine Home, the hop from User to Taiwan HiNet Dynamic: needs attention')
    expect(s.match(/needs attention/g)).toHaveLength(1)
    expect(s).not.toContain('is needs')
  })

  it('treats success and neutral as normal: nothing is added', () => {
    expect(describePath(tag([user, entry, landing], 'success', [1, 2]), [solid, { style: 'solid', status: 'neutral' }], zh))
      .toBe('用户经 Taiwan HiNet Dynamic 端口转发到 Irvine Home')
  })

  it('says the same thing in both languages: 已中断 and "interrupted"', () => {
    expect(describePath(tag([user, entry, landing], 'danger', [1, 2]), [solid, { style: 'solid', status: 'danger' }], zh)).toContain('整条线路已中断')
    expect(describePath(tag([user, entry, landing], 'danger', [1, 2]), [solid, { style: 'solid', status: 'danger' }], en)).toContain('interrupted')
  })
})

// The whole sentence table: every line kind in every state, with and without an exit
// server. A wording change shows up here as a diff and has to be made on purpose.
describe('the sentence matrix', () => {
  const exit: PathNode = { id: 'x', kind: 'exit', label: 'Tokyo Exit' }
  const kinds = ['direct', 'forward', 'tunnel', 'cdn', 'external'] as const
  const states: (PathTone | 'normal')[] = ['normal', 'pending', 'warning', 'danger']

  function build(kind: (typeof kinds)[number], state: PathTone | 'normal', withExit: boolean) {
    const status = state === 'normal' ? undefined : state
    const mark = (n: PathNode): PathNode => ({ ...n, status })
    const e = (edge: PathEdge): PathEdge => ({ ...edge, status })
    let nodes: PathNode[]
    let edges: PathEdge[]
    if (kind === 'direct') {
      nodes = [user, mark({ ...landing, sublabel: ':443' })]
      edges = [e({ style: 'solid', name: '', label: '142 ms' })]
    } else if (kind === 'forward') {
      nodes = [user, mark({ ...entry, sublabel: ':10443' }), mark(landing)]
      edges = [solid, e({ style: 'solid', label: '48 ms' })]
    } else if (kind === 'tunnel') {
      nodes = [user, mark({ ...entry, sublabel: ':20443' }), mark(landing)]
      edges = [solid, e({ style: 'dashed', label: '61 ms' })]
    } else if (kind === 'cdn') {
      nodes = [user, mark({ id: 'c', kind: 'cdn', label: 'Cloudflare' }), mark(landing)]
      edges = [solid, e({ style: 'solid', role: 'origin', label: '88 ms' })]
    } else {
      nodes = [user, mark({ id: 'x', kind: 'external', label: 'External entry', sublabel: ':8443' }), mark(landing)]
      edges = [solid, e({ style: 'solid', name: '', label: '210 ms' })]
    }
    nodes = nodes
    if (withExit) {
      nodes = [...nodes, mark(exit), internet]
      edges = [...edges, e({ style: 'solid', name: '' }), solid]
    } else {
      nodes = [...nodes, internet]
      edges = [...edges, { style: 'solid', name: '' }]
    }
    return { nodes, edges }
  }

  const table: Record<string, { zh: string; en: string }> = {}
  for (const kind of kinds) for (const state of states) for (const withExit of [false, true]) {
    const { nodes, edges } = build(kind, state, withExit)
    table[`${kind} / ${state} / ${withExit ? 'exit' : 'no exit'}`] = {
      zh: describePath(nodes, edges, zh),
      en: describePath(nodes, edges, en),
    }
  }

  it('covers 40 lines', () => expect(Object.keys(table)).toHaveLength(40))
  it('matches the reviewed wording', () => expect(table).toMatchInlineSnapshot(`
    {
      "cdn / danger / exit": {
        "en": "User through Cloudflare (CDN) by origin pull (88 ms) to Irvine Home, then out through exit server Tokyo Exit; the whole line: interrupted",
        "zh": "用户经 Cloudflare（CDN）回源（88 ms）到 Irvine Home，再经出口服务器 Tokyo Exit 出网；整条线路已中断",
      },
      "cdn / danger / no exit": {
        "en": "User through Cloudflare (CDN) by origin pull (88 ms) to Irvine Home, then to Internet; the whole line: interrupted",
        "zh": "用户经 Cloudflare（CDN）回源（88 ms）到 Irvine Home，再到互联网；整条线路已中断",
      },
      "cdn / normal / exit": {
        "en": "User through Cloudflare (CDN) by origin pull (88 ms) to Irvine Home, then out through exit server Tokyo Exit",
        "zh": "用户经 Cloudflare（CDN）回源（88 ms）到 Irvine Home，再经出口服务器 Tokyo Exit 出网",
      },
      "cdn / normal / no exit": {
        "en": "User through Cloudflare (CDN) by origin pull (88 ms) to Irvine Home, then to Internet",
        "zh": "用户经 Cloudflare（CDN）回源（88 ms）到 Irvine Home，再到互联网",
      },
      "cdn / pending / exit": {
        "en": "User through Cloudflare (CDN) by origin pull (88 ms) to Irvine Home, then out through exit server Tokyo Exit; the whole line: waiting to take effect",
        "zh": "用户经 Cloudflare（CDN）回源（88 ms）到 Irvine Home，再经出口服务器 Tokyo Exit 出网；整条线路等待生效",
      },
      "cdn / pending / no exit": {
        "en": "User through Cloudflare (CDN) by origin pull (88 ms) to Irvine Home, then to Internet; the whole line: waiting to take effect",
        "zh": "用户经 Cloudflare（CDN）回源（88 ms）到 Irvine Home，再到互联网；整条线路等待生效",
      },
      "cdn / warning / exit": {
        "en": "User through Cloudflare (CDN) by origin pull (88 ms) to Irvine Home, then out through exit server Tokyo Exit; the whole line: needs attention",
        "zh": "用户经 Cloudflare（CDN）回源（88 ms）到 Irvine Home，再经出口服务器 Tokyo Exit 出网；整条线路需要留意",
      },
      "cdn / warning / no exit": {
        "en": "User through Cloudflare (CDN) by origin pull (88 ms) to Irvine Home, then to Internet; the whole line: needs attention",
        "zh": "用户经 Cloudflare（CDN）回源（88 ms）到 Irvine Home，再到互联网；整条线路需要留意",
      },
      "direct / danger / exit": {
        "en": "User connects directly to Irvine Home (:443, 142 ms), then out through exit server Tokyo Exit; the whole line: interrupted",
        "zh": "用户直连 Irvine Home（:443，142 ms），再经出口服务器 Tokyo Exit 出网；整条线路已中断",
      },
      "direct / danger / no exit": {
        "en": "User connects directly to Irvine Home (:443, 142 ms), then to Internet; the whole line: interrupted",
        "zh": "用户直连 Irvine Home（:443，142 ms），再到互联网；整条线路已中断",
      },
      "direct / normal / exit": {
        "en": "User connects directly to Irvine Home (:443, 142 ms), then out through exit server Tokyo Exit",
        "zh": "用户直连 Irvine Home（:443，142 ms），再经出口服务器 Tokyo Exit 出网",
      },
      "direct / normal / no exit": {
        "en": "User connects directly to Irvine Home (:443, 142 ms), then to Internet",
        "zh": "用户直连 Irvine Home（:443，142 ms），再到互联网",
      },
      "direct / pending / exit": {
        "en": "User connects directly to Irvine Home (:443, 142 ms), then out through exit server Tokyo Exit; the whole line: waiting to take effect",
        "zh": "用户直连 Irvine Home（:443，142 ms），再经出口服务器 Tokyo Exit 出网；整条线路等待生效",
      },
      "direct / pending / no exit": {
        "en": "User connects directly to Irvine Home (:443, 142 ms), then to Internet; the whole line: waiting to take effect",
        "zh": "用户直连 Irvine Home（:443，142 ms），再到互联网；整条线路等待生效",
      },
      "direct / warning / exit": {
        "en": "User connects directly to Irvine Home (:443, 142 ms), then out through exit server Tokyo Exit; the whole line: needs attention",
        "zh": "用户直连 Irvine Home（:443，142 ms），再经出口服务器 Tokyo Exit 出网；整条线路需要留意",
      },
      "direct / warning / no exit": {
        "en": "User connects directly to Irvine Home (:443, 142 ms), then to Internet; the whole line: needs attention",
        "zh": "用户直连 Irvine Home（:443，142 ms），再到互联网；整条线路需要留意",
      },
      "external / danger / exit": {
        "en": "User through External entry (external entry, :8443) to Irvine Home (210 ms), then out through exit server Tokyo Exit; the whole line: interrupted",
        "zh": "用户经 External entry（外部入口，:8443）到 Irvine Home（210 ms），再经出口服务器 Tokyo Exit 出网；整条线路已中断",
      },
      "external / danger / no exit": {
        "en": "User through External entry (external entry, :8443) to Irvine Home (210 ms), then to Internet; the whole line: interrupted",
        "zh": "用户经 External entry（外部入口，:8443）到 Irvine Home（210 ms），再到互联网；整条线路已中断",
      },
      "external / normal / exit": {
        "en": "User through External entry (external entry, :8443) to Irvine Home (210 ms), then out through exit server Tokyo Exit",
        "zh": "用户经 External entry（外部入口，:8443）到 Irvine Home（210 ms），再经出口服务器 Tokyo Exit 出网",
      },
      "external / normal / no exit": {
        "en": "User through External entry (external entry, :8443) to Irvine Home (210 ms), then to Internet",
        "zh": "用户经 External entry（外部入口，:8443）到 Irvine Home（210 ms），再到互联网",
      },
      "external / pending / exit": {
        "en": "User through External entry (external entry, :8443) to Irvine Home (210 ms), then out through exit server Tokyo Exit; the whole line: waiting to take effect",
        "zh": "用户经 External entry（外部入口，:8443）到 Irvine Home（210 ms），再经出口服务器 Tokyo Exit 出网；整条线路等待生效",
      },
      "external / pending / no exit": {
        "en": "User through External entry (external entry, :8443) to Irvine Home (210 ms), then to Internet; the whole line: waiting to take effect",
        "zh": "用户经 External entry（外部入口，:8443）到 Irvine Home（210 ms），再到互联网；整条线路等待生效",
      },
      "external / warning / exit": {
        "en": "User through External entry (external entry, :8443) to Irvine Home (210 ms), then out through exit server Tokyo Exit; the whole line: needs attention",
        "zh": "用户经 External entry（外部入口，:8443）到 Irvine Home（210 ms），再经出口服务器 Tokyo Exit 出网；整条线路需要留意",
      },
      "external / warning / no exit": {
        "en": "User through External entry (external entry, :8443) to Irvine Home (210 ms), then to Internet; the whole line: needs attention",
        "zh": "用户经 External entry（外部入口，:8443）到 Irvine Home（210 ms），再到互联网；整条线路需要留意",
      },
      "forward / danger / exit": {
        "en": "User through Taiwan HiNet Dynamic (:10443) by port forwarding (48 ms) to Irvine Home, then out through exit server Tokyo Exit; the whole line: interrupted",
        "zh": "用户经 Taiwan HiNet Dynamic（:10443）端口转发（48 ms）到 Irvine Home，再经出口服务器 Tokyo Exit 出网；整条线路已中断",
      },
      "forward / danger / no exit": {
        "en": "User through Taiwan HiNet Dynamic (:10443) by port forwarding (48 ms) to Irvine Home, then to Internet; the whole line: interrupted",
        "zh": "用户经 Taiwan HiNet Dynamic（:10443）端口转发（48 ms）到 Irvine Home，再到互联网；整条线路已中断",
      },
      "forward / normal / exit": {
        "en": "User through Taiwan HiNet Dynamic (:10443) by port forwarding (48 ms) to Irvine Home, then out through exit server Tokyo Exit",
        "zh": "用户经 Taiwan HiNet Dynamic（:10443）端口转发（48 ms）到 Irvine Home，再经出口服务器 Tokyo Exit 出网",
      },
      "forward / normal / no exit": {
        "en": "User through Taiwan HiNet Dynamic (:10443) by port forwarding (48 ms) to Irvine Home, then to Internet",
        "zh": "用户经 Taiwan HiNet Dynamic（:10443）端口转发（48 ms）到 Irvine Home，再到互联网",
      },
      "forward / pending / exit": {
        "en": "User through Taiwan HiNet Dynamic (:10443) by port forwarding (48 ms) to Irvine Home, then out through exit server Tokyo Exit; the whole line: waiting to take effect",
        "zh": "用户经 Taiwan HiNet Dynamic（:10443）端口转发（48 ms）到 Irvine Home，再经出口服务器 Tokyo Exit 出网；整条线路等待生效",
      },
      "forward / pending / no exit": {
        "en": "User through Taiwan HiNet Dynamic (:10443) by port forwarding (48 ms) to Irvine Home, then to Internet; the whole line: waiting to take effect",
        "zh": "用户经 Taiwan HiNet Dynamic（:10443）端口转发（48 ms）到 Irvine Home，再到互联网；整条线路等待生效",
      },
      "forward / warning / exit": {
        "en": "User through Taiwan HiNet Dynamic (:10443) by port forwarding (48 ms) to Irvine Home, then out through exit server Tokyo Exit; the whole line: needs attention",
        "zh": "用户经 Taiwan HiNet Dynamic（:10443）端口转发（48 ms）到 Irvine Home，再经出口服务器 Tokyo Exit 出网；整条线路需要留意",
      },
      "forward / warning / no exit": {
        "en": "User through Taiwan HiNet Dynamic (:10443) by port forwarding (48 ms) to Irvine Home, then to Internet; the whole line: needs attention",
        "zh": "用户经 Taiwan HiNet Dynamic（:10443）端口转发（48 ms）到 Irvine Home，再到互联网；整条线路需要留意",
      },
      "tunnel / danger / exit": {
        "en": "User through Taiwan HiNet Dynamic (:20443) by encrypted tunnel (61 ms) to Irvine Home, then out through exit server Tokyo Exit; the whole line: interrupted",
        "zh": "用户经 Taiwan HiNet Dynamic（:20443）加密隧道（61 ms）到 Irvine Home，再经出口服务器 Tokyo Exit 出网；整条线路已中断",
      },
      "tunnel / danger / no exit": {
        "en": "User through Taiwan HiNet Dynamic (:20443) by encrypted tunnel (61 ms) to Irvine Home, then to Internet; the whole line: interrupted",
        "zh": "用户经 Taiwan HiNet Dynamic（:20443）加密隧道（61 ms）到 Irvine Home，再到互联网；整条线路已中断",
      },
      "tunnel / normal / exit": {
        "en": "User through Taiwan HiNet Dynamic (:20443) by encrypted tunnel (61 ms) to Irvine Home, then out through exit server Tokyo Exit",
        "zh": "用户经 Taiwan HiNet Dynamic（:20443）加密隧道（61 ms）到 Irvine Home，再经出口服务器 Tokyo Exit 出网",
      },
      "tunnel / normal / no exit": {
        "en": "User through Taiwan HiNet Dynamic (:20443) by encrypted tunnel (61 ms) to Irvine Home, then to Internet",
        "zh": "用户经 Taiwan HiNet Dynamic（:20443）加密隧道（61 ms）到 Irvine Home，再到互联网",
      },
      "tunnel / pending / exit": {
        "en": "User through Taiwan HiNet Dynamic (:20443) by encrypted tunnel (61 ms) to Irvine Home, then out through exit server Tokyo Exit; the whole line: waiting to take effect",
        "zh": "用户经 Taiwan HiNet Dynamic（:20443）加密隧道（61 ms）到 Irvine Home，再经出口服务器 Tokyo Exit 出网；整条线路等待生效",
      },
      "tunnel / pending / no exit": {
        "en": "User through Taiwan HiNet Dynamic (:20443) by encrypted tunnel (61 ms) to Irvine Home, then to Internet; the whole line: waiting to take effect",
        "zh": "用户经 Taiwan HiNet Dynamic（:20443）加密隧道（61 ms）到 Irvine Home，再到互联网；整条线路等待生效",
      },
      "tunnel / warning / exit": {
        "en": "User through Taiwan HiNet Dynamic (:20443) by encrypted tunnel (61 ms) to Irvine Home, then out through exit server Tokyo Exit; the whole line: needs attention",
        "zh": "用户经 Taiwan HiNet Dynamic（:20443）加密隧道（61 ms）到 Irvine Home，再经出口服务器 Tokyo Exit 出网；整条线路需要留意",
      },
      "tunnel / warning / no exit": {
        "en": "User through Taiwan HiNet Dynamic (:20443) by encrypted tunnel (61 ms) to Irvine Home, then to Internet; the whole line: needs attention",
        "zh": "用户经 Taiwan HiNet Dynamic（:20443）加密隧道（61 ms）到 Irvine Home，再到互联网；整条线路需要留意",
      },
    }
  `))
})
