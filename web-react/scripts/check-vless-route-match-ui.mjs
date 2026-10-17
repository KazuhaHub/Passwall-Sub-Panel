// Mutating acceptance is restricted to the dedicated local fixture.
import assert from 'node:assert/strict'
import { mkdtemp } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

assert.equal(process.env.PSP_V2_UI_TEST, 'isolated-acceptance-fixture')
const base = process.env.PSP_V2_PANEL_URL
assert.equal(base, 'http://127.0.0.1:19788')
assert.equal((await (await fetch(`${base}/api/version`)).json()).version, 'vlessroute-v2-acceptance')
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href)
const browser = await chromium.launch({ headless: true })
const context = await browser.newContext({ locale: 'zh-CN', viewport: { width: 1440, height: 1000 } })
const page = await context.newPage(), errors = []
page.on('pageerror', error => errors.push(error.message))
const output = await mkdtemp(join(tmpdir(), 'psp-v2-five-match-'))
let original, api
const input = line => ({ id: line.id, name: line.name, enabled: line.enabled, outbound_tag: line.outbound_tag, only_line_name: !!line.only_line_name, entry_relay_id: line.entry_relay_id || '' })
try {
  await page.goto(`${base}/login`)
  await page.getByRole('textbox').first().fill('admin')
  await page.locator('input[type="password"]').fill(process.env.PSP_V2_ADMIN_PASSWORD)
  await page.locator('button[type="submit"]').first().click()
  await page.waitForURL(url => !url.pathname.includes('login'))
  const token = await page.evaluate(() => localStorage.getItem('psp_access'))
  api = async (method, path, data) => {
    const response = await context.request.fetch(`${base}/api${path}`, { method, headers: { Authorization: `Bearer ${token}` }, ...(data ? { data } : {}) })
    assert.ok(response.ok(), `${method} ${path}: ${response.status()} ${await response.text()}`)
    return response.json()
  }
  const initial = await api('GET', '/admin/nodes/1/vless-route-lines')
  assert.equal(initial.state.lines.length, 1)
  for (const key of ['network', 'protocols', 'ip', 'domain', 'port']) assert.equal(initial.state.lines[0][key], undefined)
  // Only this known unconfigured fixture is eligible for automatic restoration.
  original = initial
  const xui = await browser.newContext()
  const xbase = 'http://127.0.0.1:13053'
  await xui.request.get(`${xbase}/`)
  const csrf = async () => (await (await xui.request.get(`${xbase}/csrf-token`)).json()).obj
  await xui.setExtraHTTPHeaders({ 'X-CSRF-Token': await csrf() })
  assert.equal((await (await xui.request.post(`${xbase}/login`, { form: { username: 'admin', password: 'admin' } })).json()).success, true)
  await xui.setExtraHTTPHeaders({ 'X-CSRF-Token': await csrf() })
  const remote = async () => {
    const response = await (await xui.request.post(`${xbase}/panel/api/xray/`)).json()
    assert.equal(response.success, true)
    const wrapper = typeof response.obj === 'string' ? JSON.parse(response.obj) : response.obj
    return { ...wrapper, xraySetting: typeof wrapper.xraySetting === 'string' ? JSON.parse(wrapper.xraySetting) : wrapper.xraySetting }
  }
  const before = await remote()
  const tag = `psp-vlessroute:1:${original.state.lines[0].id}`
  const originalRule = before.xraySetting.routing.rules.find(rule => rule.ruleTag === tag)
  assert.ok(originalRule)
  const edit = async () => {
    await page.getByRole('row').filter({ has: page.getByText('A-false', { exact: true }) }).getByRole('button', { name: '编辑', exact: true }).click()
    const dialog = page.getByRole('dialog')
    await dialog.getByRole('tab', { name: 'VLESS 路由线路', exact: true }).click()
    await dialog.getByRole('textbox', { name: /线路名称/ }).first().waitFor()
    return dialog
  }
  await page.goto(`${base}/admin/nodes`)
  let dialog = await edit()
  const accordion = dialog.getByRole('button', { name: '高级路由规则', exact: true })
  assert.equal(await accordion.getAttribute('aria-expanded'), 'false')
  await accordion.click()
  const fields = dialog.getByRole('group', { name: '高级路由规则', exact: true })
  assert.equal(await fields.getByRole('combobox').count(), 2)
  assert.equal(await fields.getByRole('textbox').count(), 3)
  assert.equal(await fields.getByRole('button').count(), 0)
  assert.equal(await fields.locator('.MuiFormHelperText-root').count(), 0)
  await fields.getByRole('combobox', { name: '网络', exact: true }).click()
  await page.getByRole('option', { name: 'TCP', exact: true }).click()
  await fields.getByRole('combobox', { name: '协议', exact: true }).click()
  await page.getByRole('option', { name: 'tls', exact: true }).click()
  await page.keyboard.press('Escape')
  await fields.getByRole('textbox', { name: 'IP', exact: true }).fill('192.0.2.0/24')
  await fields.getByRole('textbox', { name: '域名', exact: true }).fill('domain:example.com\nregexp:^a{1,3}\\.example$')
  await fields.getByRole('textbox', { name: '端口', exact: true }).fill('443,1000-2000')
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 1000 })
    const entry = await dialog.getByRole('combobox', { name: '线路入口', exact: true }).boundingBox()
    const outbound = await dialog.getByRole('combobox', { name: /^线路出站/ }).boundingBox()
    assert.ok(entry && outbound && Math.abs(entry.y - outbound.y) < 1 && Math.abs(entry.width - outbound.width) < 1 && entry.x < outbound.x, `entry/outbound row at ${width}`)
    await fields.getByRole('textbox', { name: '域名', exact: true }).scrollIntoViewIfNeeded()
    assert.ok(await dialog.locator('.MuiDialogContent-root').evaluate(e => e.scrollWidth <= e.clientWidth + 1), `overflow at ${width}`)
    await page.screenshot({ path: join(output, `five-fields-${width}.png`), animations: 'disabled' })
  }
  const write = page.waitForResponse(r => r.url().endsWith('/vless-route-lines') && r.request().method() === 'PUT')
  await dialog.locator('button[type="submit"][form="node-form"]').click()
  const saved = await (await write).json()
  assert.equal(saved.state.status, 'applied')
  assert.equal(saved.state.lines.length, 1)
  assert.equal(saved.state.lines[0].id, original.state.lines[0].id)
  assert.equal(saved.state.lines[0].route, original.state.lines[0].route)
  await dialog.waitFor({ state: 'hidden' })
  const after = await remote()
  assert.deepEqual(after.xraySetting.outbounds, before.xraySetting.outbounds)
  assert.equal(after.xraySetting.routing.rules.length, before.xraySetting.routing.rules.length)
  const owned = after.xraySetting.routing.rules.filter(rule => rule.ruleTag === tag)
  assert.equal(owned.length, 1)
  assert.deepEqual(owned[0], { ...originalRule, network: 'tcp', protocol: ['tls'], ip: ['192.0.2.0/24'], domain: ['domain:example.com', 'regexp:^a{1,3}\\.example$'], port: '443,1000-2000' })
  assert.equal((await api('GET', '/admin/nodes/1/vless-route-lines')).state.status, 'applied')
  dialog = await edit()
  assert.equal(await dialog.getByRole('button', { name: '高级路由规则', exact: true }).getAttribute('aria-expanded'), 'false')
  await dialog.getByRole('button', { name: '高级路由规则', exact: true }).click()
  assert.equal(await dialog.getByRole('textbox', { name: '端口', exact: true }).inputValue(), '443,1000-2000')
  owned[0].port = '8443'
  const changed = await (await xui.request.post(`${xbase}/panel/api/xray/update`, { form: { xraySetting: JSON.stringify(after.xraySetting), outboundTestUrl: after.outboundTestUrl } })).json()
  assert.equal(changed.success, true)
  assert.equal((await api('GET', '/admin/nodes/1/vless-route-lines')).state.status, 'drift')
  assert.equal((await remote()).xraySetting.routing.rules.find(rule => rule.ruleTag === tag).port, '8443')
  assert.deepEqual(errors, [])
  console.log(JSON.stringify({ passed: true, output, controls: 5, sameRuleID: true, sameRuleCount: true, noFallback: true, readback: true, drift: true, errors }))
} finally {
  if (original && api) {
    const latest = await api('GET', '/admin/nodes/1/vless-route-lines')
    const restored = await api('PUT', '/admin/nodes/1/vless-route-lines', { revision: latest.state.revision, lines: original.state.lines.map(input) })
    assert.equal(restored.state.status, 'applied')
    for (const key of ['network', 'protocols', 'ip', 'domain', 'port']) assert.equal(restored.state.lines[0][key], undefined)
    console.log('fixture restored; original one-rule-per-line configuration retained')
  }
  await browser.close()
}
