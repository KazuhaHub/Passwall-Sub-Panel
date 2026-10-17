// Browser-only fixture: every API request is intercepted; no live panel is used.
import assert from 'node:assert/strict'
import { mkdtemp } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

const base = process.env.PSP_GROUP_PREVIEW_URL || 'http://127.0.0.1:19881'
assert.equal(base, 'http://127.0.0.1:19881')
assert.ok(process.env.PLAYWRIGHT_MODULE, 'set PLAYWRIGHT_MODULE to the local playwright entry point')
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href)
const browser = await chromium.launch({ headless: true })
const context = await browser.newContext({ locale: 'zh-CN', viewport: { width: 1440, height: 1100 } })
const output = await mkdtemp(join(tmpdir(), 'psp-group-preview-browser-'))
const page = await context.newPage(), errors = [], writes = [], previews = []
page.on('pageerror', e => errors.push(e.message))
page.on('requestfailed', request => errors.push(`${request.url()}: ${request.failure()?.errorText}`))
const draft = { slug: 'test', name: '预览验收', sort: 1, enabled: true, direct_subscription_domain: false,
  content: '- MATCH,Pick', proxy_group_order: ['Pick'], proxy_group_members: { Pick: [{ kind: 'line', node_id: 3, line_id: 'route' }] }, proxy_group_options: {}, mihomo_sub_rules: [], mihomo_rematch_outbounds: [] }
const layout = { separators: [], sort: [], default_sort_strategy: 'by_region_then_id' }
const groups = [{ id: 1, slug: 'first', name: '其他分组', tag_filter: { all: true, tags: [] }, layout }, { id: 2, slug: 'admin', name: '管理员所属分组', tag_filter: { all: true, tags: [] }, layout }]
const envelope = items => ({ items, total: items.length, page: 1, page_size: 200 })
const inspection = { groups: [{ name: 'Pick', configured: true, options_configured: false, options: { type: 'select' }, default_members: [], members: draft.proxy_group_members.Pick }], builtins: ['DIRECT'],
  nodes: [{ id: 3, display_name: '日本节点', region: 'JP', tags: [], enabled: true, server_address: 'fixture.invalid' }],
  lines: [{ node_id: 3, line_id: 'route', kind: 'vless_route', name: '专线', display_name: '专线', enabled: true, available: true }], regions: ['JP'], tags: [], issues: [] }
let fail = false
await context.route('**/api/**', async route => {
  const request = route.request(), path = new URL(request.url()).pathname
  let data = {}
  if (request.method() !== 'GET') writes.push(path)
  if (path.endsWith('/preview-proxy-groups')) {
    const req = request.postDataJSON(); previews.push(req)
    if (fail) return route.fulfill({ status: 400, json: { error: 'fixture ambiguous line' } })
    const id = req.preview_group_id || 2
    data = { group_id: id, rule_set: 'test', status: 'ready', groups: [{ name: 'Pick', members: [id === 2 ? '🇯🇵专线' : '🇯🇵专线 [3:route]'] }] }
  } else if (path.endsWith('/inspect-proxy-groups')) data = inspection
  else if (path.endsWith('/admin/rules')) data = envelope([draft])
  else if (path.endsWith('/admin/groups')) data = envelope(groups)
  else if (path.endsWith('/admin/templates')) data = envelope([{ slug: 'mihomo', name: 'Mihomo', client_type: 'mihomo', is_default: true, rule_sets: ['test'], content: '' }])
  else if (path.endsWith('/version')) data = { version: 'group-preview-browser-fixture' }
  else if (path.endsWith('/i18n/langs')) data = []
  else if (path.includes('/notifications')) data = { items: [], total: 0, unread: 0 }
  return route.fulfill({ json: data })
})
// The production Go host injects this base tag for deep SPA routes; Vite's
// static preview host does not. Reproduce that host contract in the fixture.
await context.route(`${base}/admin/rules`, async route => {
  if (route.request().resourceType() !== 'document') return route.fallback()
  const response = await route.fetch()
  return route.fulfill({ response, body: (await response.text()).replace('<!-- PSP_PANEL_BASE -->', '<base href="/">') })
})
await context.addInitScript(() => {
  localStorage.setItem('psp_access', 'browser-fixture-only')
  localStorage.setItem('psp_user', JSON.stringify({ userId: 1, upn: 'fixture', displayName: 'Fixture', role: 'admin', authEpoch: 1 }))
  localStorage.setItem('i18nextLng', 'zh-CN')
})
try {
  await page.goto(`${base}/admin/rules`)
  await page.getByRole('row').filter({ has: page.getByText('预览验收', { exact: true }) }).getByRole('button').first().click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('tab', { name: '代理组成员', exact: true }).click()
  await dialog.getByText('1. 🇯🇵专线', { exact: true }).waitFor()
  const scope = dialog.getByText('分组：', { exact: true }).locator('..').getByRole('combobox')
  assert.equal(await scope.textContent(), '管理员所属分组')
  assert.equal(previews[0].preview_group_id, undefined)
  assert.equal(previews[0].draft.slug, 'test')
  await page.screenshot({ path: join(output, 'default-no-suffix.png'), fullPage: true })
  await scope.click()
  assert.equal(await page.getByRole('option', { name: '全部启用节点', exact: true }).count(), 0)
  await page.getByRole('option', { name: '其他分组', exact: true }).click()
  await dialog.getByText('1. 🇯🇵专线 [3:route]', { exact: true }).waitFor()
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 1100 })
    await dialog.getByText('成员顺序预览', { exact: true }).scrollIntoViewIfNeeded()
    await page.screenshot({ path: join(output, `${width}.png`), fullPage: true })
    const overflow = await dialog.evaluate(el => el.scrollWidth > el.clientWidth + 1)
    assert.equal(overflow, false, `dialog overflow at ${width}`)
  }
  fail = true
  await scope.click(); await page.getByRole('option', { name: '管理员所属分组', exact: true }).click()
  await dialog.getByText('fixture ambiguous line', { exact: true }).waitFor()
  assert.equal(await dialog.getByText('1. 🇯🇵专线 [3:route]', { exact: true }).count(), 0)
  assert.equal(await dialog.getByText('1. 🇯🇵专线', { exact: true }).count(), 0)
  assert.deepEqual(errors, [])
  assert.ok(writes.every(path => path.endsWith('/inspect-proxy-groups') || path.endsWith('/preview-proxy-groups')), JSON.stringify(writes))
  console.log(JSON.stringify({ output, previews: previews.length, errors, writes, viewports: [390, 1440] }))
} catch (error) {
  console.error(JSON.stringify({ errors, url: page.url(), body: await page.locator('body').innerText(), output }))
  await page.screenshot({ path: join(output, 'failure.png'), fullPage: true })
  throw error
} finally {
  await browser.close()
}
