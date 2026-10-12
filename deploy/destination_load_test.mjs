import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'

const good = () => ({
  schema_version: 1, candidate: 'cef54ddba3ee96f9794aaacdc6ac1ed89cc318f9', core_version: '26.6.27', cpu_ticks_per_second: 100,
  phases: ['single_rule', 'fifty_rules'].map((name, i) => ({
    name, rules: i ? 50 : 1, duration_ms: 300000, attempted: 330000, delivered: 330000, blocked: 330000,
    transport_failures: 0, target_requests: 0, control_successes: 300, heartbeat_advances: 100,
    window_delivered: Array(300).fill(1100), late_delivered: 0, hit_rules: i ? 50 : 1, hit_events: 330000,
    losses: [],
    samples: Array.from({ length: 34 }, (_, n) => ({ elapsed_ms: n * 10000, core_goroutines: n && n < 31 ? 1200 : 50, core_fds: n && n < 31 ? 300 : 30, agent_cpu_ticks: n * 50, core_cpu_ticks: n * 80 })),
  })),
})

test('load evidence requires two actual five-minute runs, sustained deliveries and resource recovery', async () => {
  const { checkDestinationLoadMetrics } = await import('./destination-load.mjs')
  assert.deepEqual(checkDestinationLoadMetrics(JSON.stringify(good())), { phases: 2, seconds: 600, delivered: 660000 })
  for (const change of [
    r => { r.candidate = 'different' }, r => { r.core_version = 'latest' }, r => { r.phases.pop() },
    r => { r.phases[1].name = 'single_rule' }, r => { r.phases[1].rules = 49 },
    r => { r.phases[0].duration_ms = 299999 }, r => { r.phases[0].window_delivered[10] = 999 },
    r => { r.phases[0].window_delivered.pop() }, r => { r.phases[0].delivered-- },
    r => { r.phases[0].blocked-- }, r => { r.phases[0].late_delivered = 1 },
    r => { r.phases[0].transport_failures = 1 }, r => { r.phases[0].target_requests = 1 },
    r => { r.phases[0].control_successes = 299 }, r => { r.phases[0].heartbeat_advances = 0 },
    r => { r.phases[0].hit_rules = 0 }, r => { r.phases[0].hit_events = 0 },
    r => { r.phases[0].hit_events-- },
    r => { r.phases[0].samples = r.phases[0].samples.slice(0, 3) },
    r => { r.phases[0].samples[5].elapsed_ms = 1 }, r => { r.phases[0].samples[5].agent_cpu_ticks = 0 },
    r => { r.phases[0].samples.at(-1).core_goroutines = 2000 }, r => { r.phases[0].samples.at(-1).core_fds = 400 },
    r => { r.phases[0].samples[10].core_goroutines = 1e6 }, r => { r.phases[0].samples[10].core_fds = 1e6 },
    r => { r.phases[0].samples[10].credential = 'private' }, r => { r.agent_id = 'private' },
    r => { r.phases[0].losses = [{ reason: 'private body', rows: 1, events: 0, unmatched: 0 }] },
  ]) {
    const report = good(); change(report)
    assert.throws(() => checkDestinationLoadMetrics(JSON.stringify(report)))
  }
})

test('load metrics cannot replace unique successful execution or expose raw private output', async () => {
  const { checkDestinationLoadEvidence } = await import('./destination-load.mjs')
  const Package = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const tests = ['TestNativeDestinationWebhookLoad', 'TestNativeDestinationWebhookLoad/single_rule', 'TestNativeDestinationWebhookLoad/fifty_rules']
  const events = tests.flatMap(Test => [{ Action: 'run', Package, Test }, { Action: 'pass', Package, Test }])
  events.push({ Action: 'pass', Package })
  const input = rows => rows.map(row => JSON.stringify(row)).join('\n') + '\n'
  checkDestinationLoadEvidence(input(events), JSON.stringify(good()))
  for (const Action of ['run', 'pass', 'fail', 'skip']) assert.throws(() => checkDestinationLoadEvidence(input([...events, { Action, Package, Test: tests[1] }]), JSON.stringify(good())))
  assert.throws(() => checkDestinationLoadEvidence(input(events.filter(event => event.Test !== tests[2])), JSON.stringify(good())))
  assert.throws(() => checkDestinationLoadEvidence(input([...events, { Action: 'output', Package, Output: 'private row' }]), JSON.stringify(good())))
})

test('load evidence retains categorized losses without claiming best-effort telemetry is lossless', async () => {
  const { checkDestinationLoadMetrics } = await import('./destination-load.mjs')
  const report = good()
  report.phases[0].hit_events = 329997
  report.phases[0].losses = [{ reason: 'node_dropped', rows: 0, events: 3, unmatched: 0 }]
  checkDestinationLoadMetrics(JSON.stringify(report))
  report.phases[0].losses.push({ reason: 'node_dropped', rows: 0, events: 1, unmatched: 0 })
  assert.throws(() => checkDestinationLoadMetrics(JSON.stringify(report)))
})

test('real load job pins production candidate, complete duration and both independent evidence files', () => {
  const workflow = readFileSync(new URL('../.github/workflows/destination-load.yml', import.meta.url), 'utf8')
  for (const required of ['cef54ddba3ee96f9794aaacdc6ac1ed89cc318f9', 'ubuntu-24.04', 'branches: [main]', 'go-version-file: go.mod', 'GOTOOLCHAIN="$toolchain"', 'PSP_NATIVE_DB_KIND=sqlite', 'PSP_NATIVE_CORE_VERSION=26.6.27', 'PSP_NATIVE_LOAD_METRICS', 'destination-load-tests.json', 'destination-load-metrics.json', 'deploy/destination-load.mjs', 'TestNativeDestinationWebhookLoad', 'TestNativeLoadProcCountersRequireBirthAndExactCPUFields', 'set -euo pipefail']) assert(workflow.includes(required), required)
  assert(!workflow.includes('continue-on-error'))
  assert(/uses: actions\/setup-go@v7\r?\n\s+env:\r?\n\s+GOTOOLCHAIN: auto/.test(workflow))
  assert(workflow.includes('test "$(go env GOVERSION)" = "$build_toolchain"'))
  assert(workflow.includes('test "$(go tool compile -V | awk \'{ print $3 }\')" = "$build_toolchain"'))
  const driver = readFileSync(new URL('../internal/app/destination_native_load_test.go', import.meta.url), 'utf8')
  for (const required of ['a.Run()', '3000', '100 * time.Millisecond', 'count < 1000', 'nativeLoadBlockedRequest', 'nativeLoadSampler', 'nativeBindLiveCore', 'readLosses', 'nativeCollectionReceiptMatches', 'statssys']) assert(driver.includes(required), required)
})
