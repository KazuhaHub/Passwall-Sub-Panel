// All API traffic is mocked: this fixture never writes to a live PSP or panel.
import assert from 'node:assert/strict'
import { mkdtemp } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

const base = 'http://127.0.0.1:19881'
assert.ok(process.env.PLAYWRIGHT_MODULE, 'set PLAYWRIGHT_MODULE to a local playwright entry point')
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href)
const browser = await chromium.launch({ headless: true })
const context = await browser.newContext({ locale: 'zh-CN', viewport: { width: 1440, height: 1100 } })
const output = await mkdtemp(join(tmpdir(), 'psp-line-inbound-browser-'))
const page = await context.newPage(), errors = [], writes = []
page.on('pageerror', error => errors.push(error.message))
const envelope = items => ({ items, total: items.length, page: 1, page_size: 200 })
const node = { id: 1, panel_id: 1, inbound_id: 1, panel_name: 'Fixture', display_name: '入站解耦验收', server_address: 'fixture.invalid', region: 'JP', tags: [], sort_order: 0, enabled: true, protocol: 'vless', desired_protocol: 'vless', desired_port: 443, observed_protocol: 'vless', observed_port: 443, endpoint_in_sync: true, flow: '', relays: [], hide_direct: true, cert_source: 'manual', cert_id: 0 }
const view = { node_id: 1, supported: true, state: { revision: 2, status: 'applied', lines: [{ id: 'route', route: 1001, name: '直连入站派生', outbound_tag: 'direct', enabled: true }] }, warnings: [] }
const defaults = {
  '/api/admin/servers': envelope([{ id: 1, name: 'Fixture', url: 'https://fixture.invalid', panel_type: '3xui', enabled: true, capabilities: ['inbound.read', 'inbound.update'] }]),
  '/api/admin/groups': envelope([]), '/api/admin/nodes/separator': envelope([]), '/api/admin/settings/ui': {},
  '/api/admin/certs': { certs: [] }, '/api/admin/dns-credentials': { credentials: [] }, '/api/admin/acme-accounts': { accounts: [] },
  '/api/admin/dns-providers': { providers: [] }, '/api/admin/acme-key-types': { key_types: ['EC256'] },
  '/api/admin/nodes/1/vless-route-outbounds': { items: [{ tag: 'direct', protocol: 'freedom', source: 'template' }] },
  '/api/version': { version: 'line-inbound-browser-fixture' }, '/api/i18n/langs': [],
}
await context.route('**/api/**', async route => {
  const request = route.request(), path = new URL(request.url()).pathname
  let data = defaults[path] || {}
  if (path === '/api/admin/nodes') data = envelope([node])
  else if (path.endsWith('/vless-route-lines')) {
    if (request.method() === 'PUT') {
      const body = request.postDataJSON()
      view.state = { ...view.state, revision: view.state.revision + 2, lines: body.lines.map(line => ({ ...line, id: line.id || 'created', route: line.route || 1001 })) }
      writes.push({ path, body })
    }
    data = view
  } else if (path.endsWith('/metadata') && request.method() === 'PUT') {
    const body = request.postDataJSON(); writes.push({ path, body }); Object.assign(node, body); data = node
  } else if (path.includes('/notifications')) data = { items: [], total: 0, unread: 0 }
  return route.fulfill({ json: data })
})
await context.route(`${base}/admin/nodes`, async route => {
  if (route.request().resourceType() !== 'document') return route.fallback()
  const response = await route.fetch()
  return route.fulfill({ response, body: (await response.text()).replace('<!-- PSP_PANEL_BASE -->', '<base href="/">') })
})
await context.addInitScript(() => {
  localStorage.setItem('psp_access', 'browser-fixture-only')
  localStorage.setItem('psp_user', JSON.stringify({ userId: 1, upn: 'fixture', displayName: 'Fixture', role: 'admin', authEpoch: 1 }))
  if (!localStorage.getItem('psp-lang')) localStorage.setItem('psp-lang', 'zh-CN')
})
const open = async () => {
  await page.getByRole('row').filter({ has: page.getByText('入站解耦验收', { exact: true }) }).getByRole('button').first().click()
  const dialog = page.getByRole('dialog')
  assert.equal(await dialog.getByRole('switch', { name: '隐藏直连线路', exact: true }).isChecked(), true)
  await dialog.getByRole('tab', { name: 'VLESS 路由线路', exact: true }).click()
  await dialog.getByRole('combobox', { name: '线路入站', exact: true }).waitFor()
  return dialog
}
try {
  await page.goto(`${base}/admin/nodes`)
  let dialog = await open()
  let toggle = dialog.getByRole('switch', { name: '启用', exact: true })
  assert.equal(await toggle.isChecked(), true)
  await toggle.uncheck(); await toggle.check()
  await dialog.getByRole('combobox', { name: '线路入站', exact: true }).click()
  const direct = page.getByRole('option', { name: '直连', exact: true })
  assert.notEqual(await direct.getAttribute('aria-disabled'), 'true')
  await direct.click()
  await dialog.getByRole('textbox', { name: '线路名称', exact: true }).fill('保存后仍启用')
  await dialog.locator('button[type="submit"][form="node-form"]').click()
  await dialog.waitFor({ state: 'hidden' })
  assert.equal(view.state.lines[0].enabled, true)
  assert.equal(view.state.lines[0].entry_relay_id || '', '')
  assert.equal(node.hide_direct, true)
  dialog = await open()
  assert.equal(await dialog.getByRole('textbox', { name: '线路名称', exact: true }).inputValue(), '保存后仍启用')
  toggle = dialog.getByRole('switch', { name: '启用', exact: true })
  assert.equal(await toggle.isChecked(), true)
  assert.equal(await toggle.isEnabled(), true)
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 1100 })
    const inbound = dialog.getByRole('combobox', { name: '线路入站', exact: true })
    await inbound.scrollIntoViewIfNeeded()
    const left = await inbound.boundingBox(), right = await dialog.getByRole('combobox', { name: '线路出站', exact: true }).boundingBox()
    assert.ok(Math.abs(left.width - right.width) < 2 && Math.abs(left.y - right.y) < 2, `inbound/outbound layout at ${width}`)
    assert.equal(await dialog.evaluate(el => el.scrollWidth > el.clientWidth + 1), false)
    await page.screenshot({ path: join(output, `${width}.png`), fullPage: true })
  }
  assert.equal(await dialog.getByText('所选中转线路已停用或删除，该路由线路暂不启用和发布。', { exact: true }).count(), 0)
  await page.evaluate(() => localStorage.setItem('psp-lang', 'en-US'))
  await page.reload()
  await page.getByRole('row').filter({ has: page.getByText('入站解耦验收', { exact: true }) }).getByRole('button').first().click()
  dialog = page.getByRole('dialog')
  assert.equal(await dialog.getByRole('switch', { name: 'Hide direct line', exact: true }).isChecked(), true)
  await dialog.getByRole('tab', { name: 'VLESS routing lines', exact: true }).click()
  await dialog.getByRole('combobox', { name: 'Line inbound', exact: true }).waitFor()
  assert.equal(await dialog.getByRole('combobox', { name: 'Line inbound', exact: true }).textContent(), 'Direct')
  assert.equal(await dialog.getByRole('combobox', { name: 'Line outbound', exact: true }).textContent(), 'direct [freedom]')
  assert.deepEqual(errors, [])
  assert.ok(writes.some(write => write.path.endsWith('/vless-route-lines')))
  console.log(JSON.stringify({ output, errors, writes: writes.map(write => write.path), widths: [390, 1440], hiddenDirect: node.hide_direct, enabled: view.state.lines[0].enabled }))
} catch (error) {
  console.error(JSON.stringify({ output, errors, body: await page.locator('body').innerText() }))
  await page.screenshot({ path: join(output, 'failure.png'), fullPage: true })
  throw error
} finally { await browser.close() }
