import assert from 'node:assert/strict'
import { mkdtemp } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

async function launch(prefix, viewport) {
  assert.ok(process.env.PLAYWRIGHT_MODULE, 'set PLAYWRIGHT_MODULE to a local playwright entry point')
  const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href)
  const browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ locale: 'zh-CN', viewport })
  const page = await context.newPage(), errors = []
  const output = await mkdtemp(join(tmpdir(), prefix))
  page.on('pageerror', error => errors.push(error.message))
  return { browser, context, page, errors, output }
}

// Browser-only fixtures always claim the dedicated static host. Their API
// callback fulfills every request; unhandled paths never reach a live server.
export async function mockBrowserFixture(prefix, entry) {
  const base = 'http://127.0.0.1:19881'
  assert.equal(process.env.PSP_GROUP_PREVIEW_URL || base, base)
  const fixture = await launch(prefix, { width: 1440, height: 1100 })
  const { context } = fixture
  await context.route('**/api/**', route => route.fulfill({ status: 500, json: { error: 'Unhandled mocked API request' } }))
  await context.route(`${base}${entry}`, async route => {
    if (route.request().resourceType() !== 'document') return route.fallback()
    const response = await route.fetch()
    return route.fulfill({ response, body: (await response.text()).replace('<!-- PSP_PANEL_BASE -->', '<base href="/">') })
  })
  await context.addInitScript(() => {
    localStorage.setItem('psp_access', 'browser-fixture-only')
    localStorage.setItem('psp_user', JSON.stringify({ userId: 1, upn: 'fixture', displayName: 'Fixture', role: 'admin', authEpoch: 1 }))
    if (!localStorage.getItem('psp-lang')) localStorage.setItem('psp-lang', 'zh-CN')
  })
  return { ...fixture, base }
}

// Mutating fixtures must match all three independent safety guards before a
// browser starts: explicit mode, exact loopback URL and fixture build identity.
export async function liveBrowserFixture(prefix) {
  assert.equal(process.env.PSP_V2_UI_TEST, 'isolated-acceptance-fixture')
  const base = process.env.PSP_V2_PANEL_URL
  assert.equal(base, 'http://127.0.0.1:19788')
  const password = process.env.PSP_V2_ADMIN_PASSWORD
  assert.ok(password, 'isolated fixture password is required')
  const response = await fetch(`${base}/api/version`)
  assert.equal(response.ok, true)
  assert.equal((await response.json()).version, 'vlessroute-v2-acceptance')
  return { ...await launch(prefix, { width: 1440, height: 1000 }), base, password }
}

export async function loginLiveFixture({ page, base, password }) {
  await page.goto(`${base}/login`)
  await page.getByRole('textbox').first().fill('admin')
  await page.locator('input[type="password"]').fill(password)
  await page.locator('button[type="submit"]').first().click()
  await page.waitForURL(url => !url.pathname.includes('login'))
  return page.evaluate(() => localStorage.getItem('psp_access'))
}
