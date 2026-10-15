import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'

const report = () => ({
  schema_version: 1, candidate: '1ce8f26cfbce8d3decf8855dda4349be10778a23', core_version: '26.6.27',
  users: 200, duration_ms: 300000,
  trial_windows: Array(300).fill(1100), user_deliveries: Array(200).fill(1650),
  trial_delivered: 330000, trial_allowed: 330000, trial_target_requests: 330000,
  trial_stored_rows: 20000, trial_stored_events: 20000, trial_over_budget_rows: 310000,
  trial_identified_rows: 0, trial_budget_max_rows: 20000,
  block_windows: Array(300).fill(110), block_delivered: 33000, block_denied: 33000,
  block_stored_events: 33000, block_loss_rows: 0, block_loss_events: 0,
  transport_failures: 0, late_delivered: 0, control_successes: 300, heartbeat_advances: 100,
  trial_losses: [{ reason: 'over_budget', rows: 310000, events: 0, unmatched: 0 }],
  cpu_ticks_per_second: 100,
  samples: Array.from({ length: 34 }, (_, i) => ({ elapsed_ms: i * 10000, core_goroutines: 10, core_fds: 8, agent_cpu_ticks: i + 1, core_cpu_ticks: i + 1 })),
})

test('trial load needs 200 real user deliveries, sustained allowed traffic and separately intact blocking', async () => {
  const { checkDestinationTrialLoadMetrics } = await import('./destination-trial-load.mjs')
  assert.deepEqual(checkDestinationTrialLoadMetrics(JSON.stringify(report())), { users: 200, seconds: 300, trial: 330000, block: 33000 })
  for (const change of [
    r => { r.users = 199 }, r => { r.user_deliveries.pop() }, r => { r.user_deliveries[30] = 0 },
    r => { r.trial_windows[10] = 999 }, r => { r.trial_windows.pop() }, r => { r.duration_ms = 299999 },
    r => { r.trial_delivered-- }, r => { r.trial_allowed-- }, r => { r.trial_target_requests-- },
    r => { r.trial_over_budget_rows = 0 }, r => { r.trial_budget_max_rows = 20001 },
    r => { r.trial_identified_rows = 1 }, r => { r.trial_budget_max_rows = 0 },
    r => { r.block_windows[10] = 0 }, r => { r.block_stored_events-- }, r => { r.block_denied-- },
    r => { r.block_loss_rows = 1 }, r => { r.block_loss_events = 1 }, r => { r.transport_failures = 1 },
    r => { r.late_delivered = 1 }, r => { r.control_successes = 299 }, r => { r.heartbeat_advances = 0 },
    r => { r.user_emails = ['private'] }, r => { r.candidate = 'different' },
    r => { r.trial_losses = [] }, r => { r.trial_losses[0].rows-- },
    r => { r.trial_losses[0].reason = 'private' }, r => { r.trial_losses.push(r.trial_losses[0]) },
    r => { r.trial_stored_events-- }, r => { r.trial_stored_rows-- },
    r => { r.samples = r.samples.slice(0, 3) }, r => { r.samples[3].elapsed_ms = 1 },
    r => { r.samples[3].agent_cpu_ticks = 0 }, r => { r.samples.at(-1).core_fds = 1000 },
    r => { r.samples[3].core_goroutines = 100000 }, r => { r.cpu_ticks_per_second = 0 },
  ]) {
    const value = report(); change(value)
    assert.throws(() => checkDestinationTrialLoadMetrics(JSON.stringify(value)))
  }
})

test('trial metrics need unique execution through the production sanitizer and cannot borrow webhook identities', async () => {
  const { checkDestinationTrialLoadEvidence } = await import('./destination-trial-load.mjs')
  const { sanitizeDestinationResults } = await import('./destination-acceptance.mjs')
  const Package = 'github.com/KazuhaHub/passwall-sub-panel/internal/app', Test = 'TestNativeDestinationTrialLoad'
  const events = [{ Action: 'run', Package, Test }, { Action: 'pass', Package, Test }, { Action: 'pass', Package }]
  const encode = rows => rows.map(row => JSON.stringify(row)).join('\n') + '\n'
  checkDestinationTrialLoadEvidence(sanitizeDestinationResults(encode(events)), JSON.stringify(report()))
  for (const Action of ['run', 'pass', 'skip', 'fail']) assert.throws(() => checkDestinationTrialLoadEvidence(encode([...events, { Action, Package, Test }]), JSON.stringify(report())))
  for (const Test of ['TestNativeDestinationWebhookLoad', 'TestNativeDestinationBlockOverflow', 'TestNativeDestinationTrialLoad/private', undefined]) assert.throws(() => checkDestinationTrialLoadEvidence(sanitizeDestinationResults(encode([...events, { Action: 'run', Package, Test }])), JSON.stringify(report())))
  assert.throws(() => checkDestinationTrialLoadEvidence(encode([...events, { Action: 'output', Package, Output: 'private row' }]), JSON.stringify(report())))
  assert.throws(() => checkDestinationTrialLoadEvidence(encode(events.slice(1)), JSON.stringify(report())))
  const loss = report()
  loss.trial_losses[0].rows -= 30000
  loss.trial_over_budget_rows -= 30000
  loss.trial_losses.push({ reason: 'node_dropped', rows: 0, events: 30000, unmatched: 0 })
  checkDestinationTrialLoadEvidence(sanitizeDestinationResults(encode(events)), JSON.stringify(loss))
})

test('trial job retains the existing webhook job and requires separate real production evidence', () => {
  const workflow = readFileSync(new URL('../.github/workflows/destination-load.yml', import.meta.url), 'utf8')
  for (const required of ['webhook-load:', 'trial-load:', 'TestNativeDestinationWebhookLoad', 'TestNativeDestinationTrialLoad', '1ce8f26cfbce8d3decf8855dda4349be10778a23', 'destination-trial-load-tests.json', 'destination-trial-load-metrics.json', 'deploy/destination-trial-load.mjs', 'deploy/destination_trial_load_test.mjs', 'set -euo pipefail']) assert(workflow.includes(required), required)
  assert(workflow.includes('TestNativeTrialProvisioningRequiresObservedRuntimeAndInbound'))
  assert(!workflow.includes('continue-on-error'))
})

test('trial setup exposes only closed boolean phases and waits for the actual native inbound before membership', async () => {
  const { sanitizeDestinationResults, isSafeDestinationDiagnostic } = await import('./destination-acceptance.mjs')
  for (const phase of ['capability', 'group', 'principal', 'membership', 'policy', 'mode', 'collection', 'publication', 'receipt', 'resources', 'warmup', 'traffic', 'ingestion']) {
    const input = JSON.stringify({ Action: 'output', Output: `native diagnostic: trial_setup_${phase}=false\n` })
    const event = JSON.parse(sanitizeDestinationResults(input))
    assert.deepEqual(event, { Action: 'output', Package: 'github.com/KazuhaHub/passwall-sub-panel/internal/app', Diagnostic: `trial_setup_${phase}`, Observed: false })
    assert(isSafeDestinationDiagnostic(event))
  }
  for (const phase of ['private-email@example', 'principal_200', 'unknown']) assert.equal(sanitizeDestinationResults(JSON.stringify({ Action: 'output', Output: `native diagnostic: trial_setup_${phase}=false\n` })).trim(), '')
  const driver = readFileSync(new URL('../internal/app/destination_native_trial_load_test.go', import.meta.url), 'utf8')
  for (const required of ['GetServerStatus', 'GetInbound', 'nativeTrialProvisioningReady', 'ResyncMembershipOrEnqueue']) assert(driver.includes(required), required)
  assert(driver.indexOf('nativeTrialProvisioningReady') < driver.indexOf('ResyncMembershipOrEnqueue'))
  assert(!driver.includes('a.user.ResyncMembership('))
})
