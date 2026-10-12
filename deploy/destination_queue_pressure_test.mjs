import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'
import { checkDestinationQueuePressureEvidence, checkDestinationQueuePressureMetrics, sanitizeDestinationQueueResults } from './destination-queue-pressure.mjs'

const good = () => {
  const lanes = Object.entries({ block: 32, observe: 8, trial: 8, usage: 16 }).map(([kind, mib]) => ({ kind, batches: Math.floor(mib * 1024 * 1024 / 600000), canonical_bytes: Math.floor(mib * 1024 * 1024 / 600000) * 600000, batch_wire_bytes: 600000, queue_full: 1 }))
  return { schema_version: 1, iterations: 1, batches: lanes.reduce((n, lane) => n + lane.batches, 0), canonical_bytes: lanes.reduce((n, lane) => n + lane.canonical_bytes, 0), retained_heap_bytes: 90000000, sampled_peak_heap_bytes: 140000000, lanes }
}
const Package = 'github.com/KazuhaHub/passwall-sub-panel/internal/service/destaudit'
const Test = 'TestNativeDestinationQueuePressure'
const events = () => [{ Action: 'start', Package }, { Action: 'run', Package, Test }, { Action: 'pass', Package, Test }, { Action: 'pass', Package }]
const encode = rows => rows.map(row => JSON.stringify(row)).join('\n') + '\n'

test('queue pressure requires separately saturated production lanes and exact byte/heap measurements', () => {
  const report = good()
  assert.equal(checkDestinationQueuePressureMetrics(JSON.stringify(report)).canonical_bytes, report.canonical_bytes)
  for (const change of [
    r => { r.iterations = 2 }, r => { r.batches++ }, r => { r.canonical_bytes-- },
    r => { r.retained_heap_bytes = 0 }, r => { r.sampled_peak_heap_bytes = r.retained_heap_bytes - 1 },
    r => { r.lanes.pop() }, r => { r.lanes[1] = r.lanes[0] }, r => { r.lanes[0].kind = 'private' },
    r => { r.lanes[0].queue_full = 0 }, r => { r.lanes[0].batch_wire_bytes = 2 ** 20 + 1 },
    r => { r.lanes[0].canonical_bytes-- }, r => { r.lanes[0].batches = 129 },
    r => { r.lanes[0].batches--; r.lanes[0].canonical_bytes -= r.lanes[0].batch_wire_bytes; r.batches--; r.canonical_bytes -= r.lanes[0].batch_wire_bytes },
    r => { r.lanes[0].credential = 'private' }, r => { r.candidate = 'private' },
    r => { r.sampled_peak_heap_bytes = Number.MAX_SAFE_INTEGER + 1 },
  ]) { const value = good(); change(value); assert.throws(() => checkDestinationQueuePressureMetrics(JSON.stringify(value))) }
  assert.throws(() => checkDestinationQueuePressureMetrics(' '.repeat(16385)))
})

test('queue execution evidence binds one real root and package pass and drops private output', () => {
  const report = JSON.stringify(good())
  const rows = events()
  const retained = sanitizeDestinationQueueResults(encode([...rows, { Action: 'output', Package, Test, Output: 'private credential email' }]))
  assert(!retained.includes('private'))
  checkDestinationQueuePressureEvidence(retained, report)
  for (const rejected of [
    rows.filter(row => row.Action !== 'run'), rows.slice(0, -1),
    [...rows, rows[1]], [...rows, rows[2]], [...rows, rows[3]],
    rows.map(row => row.Action === 'pass' && row.Test ? { ...row, Action: 'skip' } : row),
    rows.map(row => row.Action === 'pass' && row.Test ? { ...row, Action: 'fail' } : row),
    rows.map(row => ({ ...row, Package: 'private' })),
    rows.map(row => row.Test ? { ...row, Test: 'Unknown' } : row),
    [...rows, { Action: 'run', Package, Test: 'Unknown' }],
    [...rows.slice(0, -1), { Action: 'pass', Package, Test: 'Unknown' }],
    [rows[0], ...rows],
  ]) {
    assert.throws(() => checkDestinationQueuePressureEvidence(sanitizeDestinationQueueResults(encode(rejected)), report))
  }
  checkDestinationQueuePressureEvidence(sanitizeDestinationQueueResults(encode(rows.slice(1))), report)
  assert.throws(() => checkDestinationQueuePressureEvidence(encode([...rows, { Action: 'output', Package, Output: 'private' }]), report))
})

test('hosted queue gate runs the production decoded benchmark once with owned bounded evidence', () => {
  const workflow = readFileSync(new URL('../.github/workflows/destination-load.yml', import.meta.url), 'utf8')
  const job = workflow.slice(workflow.indexOf('  queue-pressure:'))
  for (const required of ['  queue-pressure:', 'ubuntu-24.04', 'destination_pressure', '-test.benchtime=1x', '^TestNativeDestinationQueuePressure$', 'PSP_NATIVE_QUEUE_METRICS', 'destination-queue-pressure.mjs sanitize', 'native-destination-queue-pressure-${{ github.sha }}', 'destination-queue-pressure-tests.json', 'destination-queue-pressure-metrics.json']) assert(job.includes(required), required)
  const source = readFileSync(new URL('../internal/service/destaudit/queue_pressure_native_test.go', import.meta.url), 'utf8')
  for (const required of ['testing.Benchmark(BenchmarkQueueDecodedHeap)', 'result.N != 1', 'filepath.EvalSymlinks', 'github-hosted', 'sampled_peak_heap_bytes', 'laneEvidence']) assert(source.includes(required), required)
})
