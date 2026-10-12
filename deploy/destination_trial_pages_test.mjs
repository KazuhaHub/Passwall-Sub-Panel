import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'

const report = () => ({ schema_version: 1, window_ms: 300000, queries: ['hits_first_page', 'policies_overview'].map((name, i) => ({ name, samples: Array.from({ length: 149 }, (_, j) => ({ elapsed_ms: j * 2000, duration_us: 1250 + j, items: i ? 2 : 50, total: i ? 2 : 20001 })) })) })

test('first-screen evidence requires real distributed samples and record volume in the original flood window', async () => {
  const { checkDestinationTrialPages } = await import('./destination-trial-pages.mjs')
  assert.deepEqual(checkDestinationTrialPages(JSON.stringify(report()), 300000).map(q => q.name), ['hits_first_page', 'policies_overview'])
  for (const change of [
    r => { r.window_ms-- }, r => { r.schema_version = 2 }, r => { r.queries.pop() },
    r => { r.queries[1].name = 'hits_first_page' }, r => { r.queries[0].samples.length = 99 },
    r => { r.queries[0].samples[2].elapsed_ms = 0 }, r => { r.queries[0].samples[0].elapsed_ms = 9000 },
    r => { r.queries[0].samples.at(-1).elapsed_ms = 300001 },
    r => { r.queries[0].samples[20].duration_us = -1 },
    r => { r.queries[0].samples[20].items = 51 }, r => { r.queries[0].samples[20].total = 1 },
    r => { r.queries[0].samples.forEach(s => { s.total = 19999 }) },
    r => { r.queries[1].samples[20].items = 1 }, r => { r.queries[0].samples[1].account = 'private' },
    r => { r.queries[0].samples.at(-1).duration_us = 5000000 }, r => { r.token = 'private' },
  ]) { const value = report(); change(value); assert.throws(() => checkDestinationTrialPages(JSON.stringify(value), 300000)) }
})

test('page sampling preserves existing trial and latency gates with separate owned numeric evidence', () => {
  const workflow = readFileSync(new URL('../.github/workflows/destination-load.yml', import.meta.url), 'utf8')
  for (const text of ['destination-trial-load-pages.json', 'deploy/destination-trial-pages.mjs', 'deploy/destination_trial_pages_test.mjs', 'TestNativePageSamplesRejectSPAStalePolicyAndPrivateTrialIdentity', 'deploy/destination-trial-load.mjs', 'deploy/destination-trial-latency.mjs']) assert(workflow.includes(text), text)
  const driver = readFileSync(new URL('../internal/app/destination_native_pages_test.go', import.meta.url), 'utf8')
  for (const text of ['/api/admin/dest/hits?group_by=none&include_trial=1&page=1&page_size=50&panel_id=', '/api/admin/dest/policies', 'nativeValidatePage', 'response.Body.Close()', 'Authorization', 'context.WithDeadline']) assert(driver.includes(text), text)
})
