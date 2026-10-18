// Browser-only acceptance: all API calls are mocked; no live data is changed.
import assert from 'node:assert/strict'
import { join } from 'node:path'
import { mockBrowserFixture } from './browser-fixture.mjs'

const { base, browser, context, page, errors, output } = await mockBrowserFixture('psp-member-prefix-browser-', '/admin/rules')
const requests = []
const members = [
  { kind: 'node', node_id: 3 },
  ...['direct', 'relay', 'route'].map(line_id => ({ kind: 'line', node_id: 3, line_id })),
  { kind: 'node_set', value: 'remaining' }, { kind: 'builtin', value: 'DIRECT' },
  { kind: 'node_set', value: 'region:HK' }, { kind: 'node_set', value: 'tag:fast' },
  { kind: 'proxy_group', value: 'Other' }, { kind: 'rematch', value: 'Mark' },
]
const draft = { slug: 'test', name: '前缀验收', sort: 1, enabled: true, content: '- MATCH,Pick', proxy_group_order: ['Pick'], proxy_group_members: { Pick: members }, proxy_group_options: {}, mihomo_sub_rules: [], mihomo_rematch_outbounds: [] }
const group = { name: 'Pick', configured: true, options_configured: false, options: { type: 'select' }, default_members: [], members }
const inspection = { groups: [group], builtins: ['DIRECT'], nodes: [{ id: 3, display_name: '日本节点', region: 'JP', tags: [], enabled: true }],
  lines: [
    { node_id: 3, line_id: 'direct', kind: 'direct', name: '日本节点', display_name: '日本节点', available: true },
    { node_id: 3, line_id: 'relay', kind: 'relay', name: '中转专线', display_name: '日本节点 中转专线', available: true },
    { node_id: 3, line_id: 'route', kind: 'vless_route', name: '路由专线', display_name: '路由专线', available: true },
  ], regions: ['HK'], tags: ['fast'], issues: [] }
const envelope = items => ({ items, total: items.length, page: 1, page_size: 200 })
await context.route('**/api/**', async route => {
  const request = route.request(), path = new URL(request.url()).pathname
  let data = {}
  if (request.method() !== 'GET') requests.push({ path, body: request.postDataJSON() })
  if (path.endsWith('/preview-proxy-groups')) data = { group_id: 2, status: 'ready', groups: [{ name: 'Pick', members: ['日本节点', '路由专线', 'DIRECT'] }] }
  else if (path.endsWith('/inspect-proxy-groups')) data = inspection
  else if (path.endsWith('/admin/rules')) data = envelope([draft])
  else if (path.endsWith('/admin/groups')) data = envelope([{ id: 2, slug: 'default', name: 'Default', tag_filter: { all: true, tags: [] } }])
  else if (path.endsWith('/admin/templates')) data = envelope([])
  else if (path.endsWith('/version')) data = { version: 'member-prefix-fixture' }
  else if (path.endsWith('/i18n/langs')) data = []
  else if (path.includes('/notifications')) data = { items: [], total: 0, unread: 0 }
  return route.fulfill({ json: data })
})
const labels = {
  'zh-CN': ['节点：日本节点', '直连：日本节点', '中转：日本节点 中转专线', 'VLESS 路由：路由专线', '其余线路（自动去重）', '内置：DIRECT', '区域：HK', '标签：fast', '代理组：Other', 'Rematch：Mark'],
  'en-US': ['Node: 日本节点', 'Direct: 日本节点', 'Relay: 日本节点 中转专线', 'VLESS routing: 路由专线', 'Remaining lines (deduplicated)', 'Built-in: DIRECT', 'Region: HK', 'Tag: fast', 'Proxy group: Other', 'Rematch: Mark'],
}
try {
  for (const language of ['zh-CN', 'en-US']) {
    if (language === 'en-US') await page.evaluate(() => localStorage.setItem('psp-lang', 'en-US'))
    await page.goto(`${base}/admin/rules`)
    await page.getByRole('row').filter({ has: page.getByText('前缀验收', { exact: true }) }).getByRole('button').first().click()
    const dialog = page.getByRole('dialog')
    await dialog.getByRole('tab').nth(1).click()
    for (const label of labels[language]) await dialog.getByText(label, { exact: true }).waitFor()
    assert.equal(await dialog.getByText(labels[language][3], { exact: true }).getAttribute('title'), 'line:3:route')
    for (const raw of ['1. 日本节点', '2. 路由专线', '3. DIRECT']) await dialog.getByText(raw, { exact: true }).waitFor()
    if (language === 'zh-CN') {
      for (const width of [390, 1440]) {
        await page.setViewportSize({ width, height: 1100 })
        await dialog.getByText(labels[language][3], { exact: true }).scrollIntoViewIfNeeded()
        assert.equal(await dialog.evaluate(el => el.scrollWidth > el.clientWidth + 1), false)
        await page.screenshot({ path: join(output, `${width}.png`), fullPage: true })
      }
    }
  }
  for (const request of requests) {
    assert.ok(request.path.endsWith('/inspect-proxy-groups') || request.path.endsWith('/preview-proxy-groups'))
    assert.deepEqual(request.body.draft?.proxy_group_members || request.body.proxy_group_members, { Pick: members })
  }
  assert.deepEqual(errors, [])
  console.log(JSON.stringify({ output, languages: Object.keys(labels), widths: [390, 1440], errors, requests: requests.length }))
} catch (error) {
  console.error(JSON.stringify({ output, errors, body: await page.locator('body').innerText() }))
  await page.screenshot({ path: join(output, 'failure.png'), fullPage: true })
  throw error
} finally { await browser.close() }
