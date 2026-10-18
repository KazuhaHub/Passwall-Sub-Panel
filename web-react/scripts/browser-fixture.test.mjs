import assert from 'node:assert/strict'
import { test } from 'node:test'
import { liveBrowserFixture, mockBrowserFixture } from './browser-fixture.mjs'

test('browser harness refuses unsafe or unidentified live targets before launch', async () => {
  const keys = ['PSP_V2_UI_TEST', 'PSP_V2_PANEL_URL', 'PSP_V2_ADMIN_PASSWORD', 'PSP_GROUP_PREVIEW_URL']
  const saved = keys.map(key => process.env[key]), fetch = globalThis.fetch
  try {
    delete process.env.PSP_V2_UI_TEST
    await assert.rejects(liveBrowserFixture('guard-test-'))
    process.env.PSP_V2_UI_TEST = 'isolated-acceptance-fixture'
    process.env.PSP_V2_PANEL_URL = 'http://127.0.0.1:18788'
    await assert.rejects(liveBrowserFixture('guard-test-'))
    process.env.PSP_V2_PANEL_URL = 'http://127.0.0.1:19788'
    delete process.env.PSP_V2_ADMIN_PASSWORD
    await assert.rejects(liveBrowserFixture('guard-test-'))
    process.env.PSP_V2_ADMIN_PASSWORD = 'fixture-only'
    globalThis.fetch = async () => ({ ok: true, json: async () => ({ version: 'not-the-isolated-fixture' }) })
    await assert.rejects(liveBrowserFixture('guard-test-'))
    process.env.PSP_GROUP_PREVIEW_URL = 'http://127.0.0.1:18788'
    await assert.rejects(mockBrowserFixture('guard-test-', '/admin/nodes'))
  } finally {
    globalThis.fetch = fetch
    keys.forEach((key, index) => {
      if (saved[index] === undefined) delete process.env[key]
      else process.env[key] = saved[index]
    })
  }
})
