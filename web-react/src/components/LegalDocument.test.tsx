// @vitest-environment jsdom
import { screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import type { DataCollection } from '@/api/legal'
import { renderAccess, setTestLang } from '@/test/accessControlHarness'
import LegalDocument from './LegalDocument'

const base: DataCollection = {
  sub_log: { enabled: true, retention_days: 0, includes_ip: true },
  auth_event: { enabled: true, retention_days: 90, includes_ip: true },
  connection_history: { enabled: true, retention_days: 30, includes_ip: true },
  hwid: { enabled: true, includes_ip: false },
  flag_records: { enabled: true, retention_days: 90, includes_ip: false },
  risk_signals: { enabled: true, includes_ip: false },
  risk_reviews: { enabled: true, includes_ip: false },
  dest_hits: { enabled: true, nodes: 4, retention_days: 30, includes_ip: false },
  dest_trial: { enabled: false, nodes: 4, retention_days: 7, includes_ip: false },
}

const doc = '前言\n\n[[data-collection]]\n\n## 你的权利\n\n后文'

function card() { return screen.getByRole('region', { name: '我们收集什么' }) }

describe('LegalDocument', () => {
  it('cuts the text at the marker line and puts the generated list between the two halves', () => {
    renderAccess(<LegalDocument content={doc} dataCollection={base} />)
    const text = document.body.textContent ?? ''
    expect(text.indexOf('前言')).toBeLessThan(text.indexOf('我们收集什么'))
    expect(text.indexOf('我们收集什么')).toBeLessThan(text.indexOf('你的权利'))
    expect(text).not.toContain('[[data-collection]]')
    expect(screen.getByRole('heading', { name: '你的权利' })).toBeTruthy()
  })

  it('shows no list when the text has no marker', () => {
    renderAccess(<LegalDocument content={'只有正文'} dataCollection={base} />)
    expect(screen.queryByRole('region', { name: '我们收集什么' })).toBeNull()
  })

  it('states each retention from the data: 0 is kept for ever, the rest interpolate', () => {
    renderAccess(<LegalDocument content={doc} dataCollection={base} />)
    const rows = within(card()).getAllByRole('listitem').map(li => li.textContent)
    expect(rows).toContain('订阅访问日志（含 IP）永久保留')
    expect(rows).toContain('登录记录（含 IP）保留 90 天')
    expect(rows).toContain('连接历史（含 IP）保留 30 天')
    expect(rows).toContain('访问规则命中4 台节点 · 保留 30 天')
    // The words never carry a number of their own: another retention is another sentence.
    const other = { ...base, connection_history: { enabled: true, retention_days: 14, includes_ip: true } }
    renderAccess(<LegalDocument content={doc} dataCollection={other} />)
    expect(screen.getAllByText('保留 14 天').length).toBe(1)
  })

  it('omits what is not being recorded and lists nothing the server did not send', () => {
    renderAccess(<LegalDocument content={doc} dataCollection={base} />)
    const text = card().textContent ?? ''
    expect(text).not.toContain('白名单试运行')
    expect(text).not.toContain('按网站用量')
    const noNodes = { ...base, dest_hits: { enabled: false, nodes: 0, retention_days: 30, includes_ip: false } }
    renderAccess(<LegalDocument content={doc} dataCollection={noNodes} />)
    expect(screen.queryAllByText(/访问规则命中/).length).toBe(1)
  })

  it('lists per-site usage only when the server sends it (the administrator opted in)', () => {
    const withUsage = { ...base, dest_usage: { enabled: true, nodes: 1, retention_days: 7, includes_ip: false } }
    renderAccess(<LegalDocument content={doc} dataCollection={withUsage} />)
    expect(within(card()).getByText('按网站用量')).toBeTruthy()
    expect(within(card()).getByText('1 台节点 · 保留 7 天')).toBeTruthy()
  })

  it('says how many groups turned a panel-wide-off signal back on, never which', () => {
    const groups = { ...base, risk_signals: { enabled: true, enabled_groups: 2, includes_ip: false } }
    renderAccess(<LegalDocument content={doc} dataCollection={groups} />)
    expect(within(card()).getByText('2 个分组开启')).toBeTruthy()
  })

  it('shows a placeholder instead of the list while there is no data (the editor preview before anything is on)', () => {
    renderAccess(<LegalDocument content={doc} dataCollection={null} />)
    expect(screen.getByText(/此处会显示/)).toBeTruthy()
  })

  it('renders only safe Markdown: raw HTML is dropped, a script link is not a link, an image is its alt text', () => {
    const md = '<script>window.hacked=1</script><b>raw</b>\n\n[bad](javascript:alert(1)) [ok](https://example.test/a)\n\n![logo](https://tracker.test/x.png)'
    renderAccess(<LegalDocument content={md} dataCollection={base} />)
    expect(document.querySelector('script')).toBeNull()
    expect(document.querySelector('b')).toBeNull()
    expect(document.querySelector('img')).toBeNull()
    expect(screen.getByText(/logo/)).toBeTruthy()
    const links = Array.from(document.querySelectorAll('a'))
    expect(links.map(a => a.getAttribute('href'))).toEqual(['https://example.test/a'])
    expect(links[0].getAttribute('target')).toBe('_blank')
    expect(links[0].getAttribute('rel')).toContain('noopener')
  })

  it('makes a mailto link clickable on a legal page, and nothing script-like', () => {
    const md = '[write to us](mailto:privacy@example.test) [bad](javascript:alert(1)) [data](data:text/html,x) [vb](vbscript:x)'
    renderAccess(<LegalDocument content={md} dataCollection={base} />)
    const links = Array.from(document.querySelectorAll('a'))
    expect(links.map(a => a.getAttribute('href'))).toEqual(['mailto:privacy@example.test'])
    expect(links[0].getAttribute('target')).toBeNull()
    for (const word of ['bad', 'data', 'vb']) expect(screen.getByText(new RegExp(word))).toBeTruthy()
  })

  it('in English', () => {
    setTestLang('en-US')
    renderAccess(<LegalDocument content={doc} dataCollection={base} />)
    const rows = within(screen.getByRole('region', { name: 'What we collect' })).getAllByRole('listitem').map(li => li.textContent)
    expect(rows).toContain('Subscription access log (with IP)kept for ever')
    expect(rows).toContain('Sign-in log (with IP)kept 90 days')
  })
})
