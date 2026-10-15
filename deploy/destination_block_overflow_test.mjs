import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'

const good = () => ({
  schema_version: 1, candidate: 'b35b29780406e4e7272c7b87f80ed2fe2d69bc42', core_version: '26.6.27', cpu_ticks_per_second: 100,
  phases: [{ name: 'block_overflow', rules: 1, duration_ms: 300000, attempted: 330000, delivered: 330000, blocked: 330000,
    transport_failures: 0, target_requests: 0, control_successes: 300, heartbeat_advances: 100,
    window_delivered: Array(300).fill(1100), late_delivered: 0, hit_rules: 1, hit_events: 20000, budget_max_rows: 20000,
    losses: [{ reason: 'over_budget', rows: 310000, events: 0, unmatched: 0 }],
    samples: Array.from({ length: 34 }, (_, i) => ({ elapsed_ms: i * 10000, core_goroutines: 10, core_fds: 8, agent_cpu_ticks: i + 1, core_cpu_ticks: i + 1 })),
  }],
})

test('block overflow requires actual quota exhaustion, exact categorized loss reconciliation and healthy control/resources', async () => {
  const { checkDestinationBlockOverflowMetrics } = await import('./destination-block-overflow.mjs')
  assert.deepEqual(checkDestinationBlockOverflowMetrics(JSON.stringify(good())), { seconds: 300, delivered: 330000, stored: 20000 })
  for (const change of [
    r => { r.phases[0].budget_max_rows = 0 }, r => { r.phases[0].budget_max_rows = 40000 },
    r => { r.phases[0].losses = [] }, r => { r.phases[0].losses[0].rows-- },
    r => { r.phases[0].losses[0].reason = 'private' }, r => { r.phases[0].losses.push(r.phases[0].losses[0]) },
    r => { r.phases[0].blocked-- }, r => { r.phases[0].hit_events-- }, r => { r.phases[0].hit_rules = 0 },
    r => { r.phases[0].window_delivered[3] = 999 }, r => { r.phases[0].duration_ms = 299999 },
    r => { r.phases[0].target_requests = 1 }, r => { r.phases[0].transport_failures = 1 },
    r => { r.phases[0].late_delivered = 1 }, r => { r.phases[0].control_successes = 299 },
    r => { r.phases[0].heartbeat_advances = 0 }, r => { r.phases[0].samples = r.phases[0].samples.slice(0, 3) },
    r => { r.phases[0].samples.at(-1).core_fds = 1000 }, r => { r.phases[0].samples[2].agent_cpu_ticks = 0 },
    r => { r.phases[0].samples[2].elapsed_ms = 1 }, r => { r.phases[0].credential = 'private' },
    r => { r.phases[0].name = 'single_rule' }, r => { r.phases.push(r.phases[0]) },
  ]) { const value = good(); change(value); assert.throws(() => checkDestinationBlockOverflowMetrics(JSON.stringify(value))) }
  const value = good()
  value.phases[0].losses[0].rows -= 10000
  value.phases[0].losses.push({ reason: 'node_dropped', rows: 0, events: 10000, unmatched: 0 })
  checkDestinationBlockOverflowMetrics(JSON.stringify(value))
})

test('block quota evidence requires its own root and phase through the production sanitizer', async () => {
  const { checkDestinationBlockOverflowEvidence } = await import('./destination-block-overflow.mjs')
  const { sanitizeDestinationResults } = await import('./destination-acceptance.mjs')
  const Package = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const names = ['TestNativeDestinationBlockOverflow', 'TestNativeDestinationBlockOverflow/block_overflow']
  const rows = names.flatMap(Test => [{ Action: 'run', Package, Test }, { Action: 'pass', Package, Test }])
  rows.push({ Action: 'pass', Package })
  const encode = events => events.map(event => JSON.stringify(event)).join('\n') + '\n'
  checkDestinationBlockOverflowEvidence(sanitizeDestinationResults(encode(rows)), JSON.stringify(good()))
  for (const Action of ['run', 'pass', 'fail', 'skip']) assert.throws(() => checkDestinationBlockOverflowEvidence(encode([...rows, { Action, Package, Test: names[1] }]), JSON.stringify(good())))
  for (const Test of ['TestNativeDestinationTrialLoad', 'TestNativeDestinationWebhookLoad', 'TestNativeDestinationBlockOverflow/private', undefined]) assert.throws(() => checkDestinationBlockOverflowEvidence(sanitizeDestinationResults(encode([...rows, { Action: 'run', Package, Test }])), JSON.stringify(good())))
  assert.throws(() => checkDestinationBlockOverflowEvidence(encode(rows.filter(row => row.Test !== names[1])), JSON.stringify(good())))
  assert.throws(() => checkDestinationBlockOverflowEvidence(encode([...rows, { Action: 'output', Package, Output: 'private hit' }]), JSON.stringify(good())))
})

test('separate block overflow job preserves webhook and trial jobs with immutable real evidence', () => {
  const workflow = readFileSync(new URL('../.github/workflows/destination-load.yml', import.meta.url), 'utf8')
  for (const required of ['webhook-load:', 'trial-load:', 'block-overflow:', 'TestNativeDestinationBlockOverflow', 'destination-block-overflow-tests.json', 'destination-block-overflow-metrics.json', 'deploy/destination-block-overflow.mjs', 'b35b29780406e4e7272c7b87f80ed2fe2d69bc42']) assert(workflow.includes(required), required)
  assert(!workflow.includes('continue-on-error'))
  const driver = readFileSync(new URL('../internal/app/destination_native_load_test.go', import.meta.url), 'utf8')
  for (const required of ['mutation := method != http.MethodGet && path != "dest/publish"', 'after = time.UnixMilli(time.Now().UnixMilli())', 'nativeMutationReceiptMatches(prior, *runtime', 'nativeCollectionReceiptMatches(*runtime, protocol.CollectHits', 'load_transition']) assert(driver.includes(required), required)
})
