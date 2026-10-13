import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { checkDestinationMixedPressureEvidence, checkDestinationMixedPressureMetrics } from './destination-mixed-pressure.mjs'
import { moduleNodeCandidate, sanitizeDestinationResults } from './destination-acceptance.mjs'

// Synthetic verifier inputs only; none of these values is runtime evidence.
const kinds = ['block', 'observe', 'trial', 'usage']
const queue = active => kinds.map((kind, i) => ({ kind, waiting: 0, active: active ? 1 : 0, batches: active ? 1 : 0, canonical_bytes: active ? 1000 : 0, batch_limit: [128, 32, 32, 64][i], byte_limit: [32, 8, 8, 16][i] * 1024 * 1024, owner_limit: [4, 1, 1, 1][i], owners: active ? 1 : 0, max_owner_batches: active ? 1 : 0 }))
const accessAt = i => {
  const fraction = Math.min(i, 118) / 118
  const usage_events = 1 + Math.floor(58192 * fraction), denied_events = Math.floor(70000 * fraction), dropped_events = Math.floor(137852 * fraction)
  return { available: 1, private_lines: usage_events + denied_events + dropped_events, usage_events, denied_events, unmatched_events: 0, dropped_events, stale_events: 0, parse_queued: 0, parse_limit: 4096, pending_dropped: 0, pending_unmatched: 0 }
}
const reading = (saturated, elapsed = 0) => ({ schema_version: 2, go_heap_alloc_bytes: 10000000, go_heap_inuse_bytes: 12000000, go_heap_objects: 50000, go_goroutines: 30, access_log: accessAt(elapsed), lanes: kinds.map((kind, i) => ({ kind, keys: saturated ? [20000, 10000, 10000, 50000][i] : 0, key_limit: [20000, 10000, 10000, 50000][i], hours: 1, pending: saturated ? 1 : 0, pending_rows: saturated ? 10 : 0, pending_bytes: saturated ? 1000 : 0, retained_dropped_events: saturated ? 1000 : 0, retained_unmatched_events: 0 })) })
const bounds = [0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 0]
const fixture = () => ({
  schema_version: 2, database: 'sqlite', reserved_loss_column_reproduced: 0, candidate: moduleNodeCandidate.commit, core_version: '26.6.27', traffic_ms: 90000, partition_ms: 75000, native_owners: 1,
  delivered: [72000, 36000, 99000, 90000], transport_failures: 0, late_delivered: 0, delivery_windows: [800,400,1100,1000].map(n => Array(90).fill(n)), control_successes: 90, user_deliveries: Array(200).fill(495), target_requests: [36000, 189000, 0],
  partition_sync_failures: 8, recovered_sync_successes: 20, frozen_transmissions: [4, 1, 1, 1], verified_canonical_retries: [1, 1, 1, 1],
  queue_reads: 12000, queue_bound_violations: 0, max_total_batches: 4, max_total_canonical_bytes: 4000, queue_peaks: queue(true),
  node_samples: Array.from({ length: 121 }, (_, i) => ({ elapsed_ms: i * 1000 + 50, reading: reading(i >= 50 && i < 75, i) })),
  psp_samples: Array.from({ length: 121 }, (_, i) => ({ elapsed_ms: i * 1000, go_heap_alloc_bytes: 20000000, go_heap_inuse_bytes: 25000000, queue: queue(i === 80) })),
  ledgers: kinds.map((kind, i) => ({ kind, expected_events: [72000, 36000, 99000, 196044][i], stored_rows: 1000, stored_events: 1000, trial_identified_rows: 0, budget_max_rows: 1000, losses: [{ reason: 'node_dropped', rows: 0, events: [71000, 35000, 98000, 195044][i], unmatched: 0 }] })),
  access_ingress: { before: accessAt(0), after: accessAt(118), allowed_expected: 225090, allowed_received: 196044, allowed_not_received: 29046, denied_expected: 72000, denied_received: 70000, denied_not_received: 2000 },
  observation_ms: 121000,
  histograms: ['fixture_destination_sync_handler_ms', 'psp_node_control_sync_ms', 'psp_poll_ms', 'psp_poll_stage_ms{stage=sink_flush}', ...kinds.flatMap(kind => [`psp_dest_audit_begin_ms{kind=${kind}}`, `psp_dest_audit_chunk_ms{kind=${kind}}`])].map(name => ({ name, count: 10, buckets: bounds.map((upper_ms, i) => ({ upper_ms, inf: i === bounds.length - 1, count: 10 })) })),
})
const evidence = () => [
  { Action: 'start' }, { Action: 'run', Test: 'TestNativeDestinationMixedPressure' },
  { Action: 'pass', Test: 'TestNativeDestinationMixedPressure', Elapsed: 121 }, { Action: 'pass', Elapsed: 121 },
].map(e => JSON.stringify({ Package: 'github.com/KazuhaHub/passwall-sub-panel/internal/app', ...e })).join('\n')

test('mixed gate needs same-window simultaneous caps, four canonical retries and complete persisted ledgers', () => {
  const result = checkDestinationMixedPressureEvidence(evidence(), JSON.stringify(fixture()))
  assert.equal(result.native_owners, 1)
  assert.deepEqual(result.delivered, [72000, 36000, 99000, 90000])
  assert.equal(result.latency.length, 12)
  assert.equal(result.allowed_not_received_from_core, 29046)
  assert.equal(result.denied_not_received_from_core, 2000)
  for (const mutate of [
    r => { r.candidate = 'foreign' }, r => { r.native_owners = 129 }, r => { r.transport_failures = 1 },
    r => { r.delivered[0]-- }, r => { r.target_requests[2]++ }, r => { r.control_successes-- }, r => { r.user_deliveries[0] = 0 },
    r => { r.late_delivered = 1 }, r => { r.delivery_windows[2][0] = 999 }, r => { r.delivery_windows[0][0]-- }, r => { r.delivery_windows[3].pop() },
    r => { r.partition_ms = 0 }, r => { r.frozen_transmissions[2] = 0 }, r => { r.verified_canonical_retries[3] = 0 },
    r => { r.partition_sync_failures = 0 }, r => { r.recovered_sync_successes = 0 }, r => { r.queue_bound_violations++ },
    r => { r.max_total_batches = 256 }, r => { r.max_total_canonical_bytes = 64 * 1024 * 1024 },
    r => { r.queue_peaks[2] = queue(false)[2] }, r => { r.queue_peaks[0].owner_limit = 128 },
    r => { r.psp_samples[80].queue[1].batches = 2 }, r => { r.psp_samples[80].queue[2].canonical_bytes = 0 },
    r => { r.psp_samples[10].go_heap_alloc_bytes = 0 }, r => { r.node_samples[10].reading.go_heap_alloc_bytes = 0 },
    r => { r.node_samples[50].reading.lanes[0].key_limit++ }, r => { r.node_samples[50].reading.lanes[3].pending_bytes = 1048577 },
    r => { r.node_samples.forEach(s => { s.reading.lanes[1].pending = 0; s.reading.lanes[1].pending_rows = 0; s.reading.lanes[1].pending_bytes = 0 }) },
    r => { r.node_samples.forEach(s => { s.reading.lanes[2].keys = 0 }) },
    r => { r.node_samples.forEach(s => { s.reading.lanes[0].retained_dropped_events = 0 }) },
    r => { r.node_samples.forEach(s => { if (s.elapsed_ms < 75000) s.reading = reading(false) }) },
    r => { r.node_samples.splice(10, 20) }, r => { r.psp_samples[10].elapsed_ms = 0 },
    r => { r.ledgers[0].stored_events-- }, r => { r.ledgers[2].trial_identified_rows = 1 },
    r => { r.ledgers[1].budget_max_rows = 20001 }, r => { r.ledgers[3].losses[0].events-- },
    r => { r.ledgers[2].losses[0].reason = 'ingest_error' }, r => { r.ledgers[3].losses.push(r.ledgers[3].losses[0]) },
    r => { r.histograms[4].count = 0 }, r => { r.histograms[5].buckets[3].count = 0 },
    r => { r.histograms.pop() }, r => { r.histograms[8].buckets.pop() },
    r => { r.node_samples[0].reading.email = 'private' }, r => { r.ledgers[0].destinations = ['private'] },
    r => { r.observation_ms = 89999 }, r => { r.traffic_ms = 89000 },
    r => { r.schema_version = 1 }, r => { r.node_samples[0].reading.schema_version = 1 },
    r => { r.reserved_loss_column_reproduced = 1 },
    r => { r.access_ingress.allowed_received++ }, r => { r.access_ingress.allowed_not_received-- },
    r => { r.access_ingress.after.private_lines++ }, r => { r.access_ingress.after.stale_events++ },
    r => { r.access_ingress.after.parse_queued = 1 }, r => { r.access_ingress.after.pending_dropped = 1 },
    r => { r.access_ingress.before.usage_events = 0 }, r => { r.access_ingress.denied_not_received-- },
    r => { r.access_ingress.after.destination = 'private' }, r => { r.node_samples.at(-1).reading.access_log.private_lines++ },
    r => { r.node_samples[120].reading.lanes[0].keys = 1 }, r => { r.psp_samples.at(-1).queue = queue(true) },
    r => { r.ledgers[3].expected_events = 225090; r.ledgers[3].losses[0].events += 29046 },
    r => { r.node_samples[119].reading.access_log = accessAt(0) },
  ]) {
    const report = fixture(); mutate(report)
    assert.throws(() => checkDestinationMixedPressureMetrics(JSON.stringify(report)), /private content withheld/)
  }
})

test('mixed evidence sanitization preserves the independent root and rejects skips, caching and private extensions', () => {
  const sanitized = sanitizeDestinationResults(evidence())
  checkDestinationMixedPressureEvidence(sanitized, JSON.stringify(fixture()))
  for (const input of [
    evidence().replace('"run"', '"skip"'), evidence().replace('"run"', '"pass"'),
    evidence().replaceAll('TestNativeDestinationMixedPressure', 'TestNativeDestinationTrialLoad'),
    evidence() + '\n' + JSON.stringify({ Action: 'output', Package: 'github.com/KazuhaHub/passwall-sub-panel/internal/app', Output: '(cached)' }),
    evidence() + '\n' + JSON.stringify({ Action: 'pass', Package: 'github.com/KazuhaHub/passwall-sub-panel/internal/app', destination: 'private' }),
  ]) assert.throws(() => checkDestinationMixedPressureEvidence(input, JSON.stringify(fixture())))
})

test('mixed integration binds each original to the selected owned SQL backend', () => {
  for (const database of ['sqlite', 'mysql', 'postgres']) {
    const report = fixture(); report.database = database
    report.reserved_loss_column_reproduced = database === 'mysql' ? 1 : 0
    assert.equal(checkDestinationMixedPressureEvidence(evidence(), JSON.stringify(report), database).database, database)
    if (database === 'mysql') { const missing = structuredClone(report); missing.reserved_loss_column_reproduced = 0; assert.throws(() => checkDestinationMixedPressureMetrics(JSON.stringify(missing), database)) }
    for (const wrong of ['sqlite', 'mysql', 'postgres', 'foreign'].filter(kind => kind !== database)) assert.throws(() => checkDestinationMixedPressureMetrics(JSON.stringify(report), wrong))
  }
})

test('hosted mixed driver preserves existing gates and uses actual daemon, collector, queue and SQL observations', () => {
  const source = readFileSync(new URL('../internal/app/destination_native_mixed_load_test.go', import.meta.url), 'utf8')
  assert.match(source, /protocol\.NodeReport/)
  assert.match(source, /protocol\.ValidateAuditObservation/)
  assert.match(source, /sha256\.Sum256/)
  assert.match(source, /StatusServiceUnavailable/)
  assert.match(source, /original\.ServeHTTP\(response, r\)/)
  assert.match(source, /runtime\.ReadMemStats/)
  assert.match(source, /queue\.Pressure\(\)/)
  assert.match(source, /dest_audit_loss_hourly/)
  assert.match(source, /dest_usage_hourly/)
  assert.match(source, /restore disposable hosted name mappings/)
  assert.doesNotMatch(source, /runtime\.GC\(|SetSnapshot\(|\.collector\.(Ack|Next)\(/)
  const workflow = readFileSync(new URL('../.github/workflows/destination-load.yml', import.meta.url), 'utf8')
  for (const name of ['trial-load:', 'block-overflow:', 'mixed-pressure:']) assert(workflow.includes(name))
  assert.match(workflow, /\^TestNativeDestinationMixedPressure\$/)
  assert.match(workflow, /destination-mixed-pressure\.mjs/)
  const mixed = workflow.slice(workflow.indexOf('\n  mixed-pressure:'))
  for (const database of ['sqlite', 'mysql', 'postgres']) assert(mixed.includes(`database: ${database}`))
  assert.match(mixed, /PSP_NATIVE_DB_KIND=\$\{\{ matrix\.database \}\}/)
  assert.match(mixed, /TestNativeMixedIngressSeparatesCoreOmissionsFromPersistedUsage/)
  assert.match(mixed, /native-destination-mixed-pressure-mysql/)
  assert.match(mixed, /native-destination-mixed-pressure-postgres/)
})
