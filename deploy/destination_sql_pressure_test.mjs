import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { checkSQLPressureEvidence, checkSQLPressureMetrics, sanitizeSQLPressureResults } from './destination-sql-pressure.mjs'

const Package = 'github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore', Test = 'TestNativeDestinationSQLPressure'
const encode = rows => rows.map(row => JSON.stringify(row)).join('\n') + '\n'
const events = () => [{ Action: 'start', Package }, { Action: 'run', Package, Test }, { Action: 'pass', Package, Test }, { Action: 'pass', Package }]
const good = () => ({ schema_version: 1, database: 'sqlite', rounds: 20, registered_agents: 129, drain_ns: 1e9,
  first_cycle: ['block', 'block', 'block', 'block', 'observe', 'trial', 'usage'],
  lanes: ['block', 'observe', 'trial', 'usage'].map(kind => {
    const cap = { block: 32, observe: 8, trial: 8, usage: 16 }[kind] * 2 ** 20
    const wire = 700000, batches = Math.floor(cap / wire), budget = kind === 'usage' ? 40000 : 20000
    const total = batches * 20, buckets = Array(batches).fill(budget)
    const transactions = total + batches * (2 * Math.floor(budget / 2001) + Math.max(0, Math.ceil(budget % 2001 / 1000) - 1))
    return { kind, batches: total, canonical_bytes: total * wire, peak_batches: batches, peak_canonical_bytes: batches * wire, batch_wire_bytes: wire, offered_rows: (total + 20) * 2001,
      stored_events: batches * budget, budget_rows: batches * budget, budget_bucket_rows: buckets, full_budget_agents: batches, queue_full_rows: 20 * 2001, over_budget_rows: total * 2001 - batches * budget,
      transactions, max_chunk_rows: 1000, transaction_ns: Array(transactions).fill(1000) }
  }) })

test('SQL pressure rejects incomplete saturation, false durable losses and unbounded chunk measurements', () => {
  checkSQLPressureMetrics(JSON.stringify(good()))
  const crossed = good()
  crossed.rounds = 25
  for (const lane of crossed.lanes) {
    lane.batches = lane.peak_batches * 25
    lane.canonical_bytes = lane.batches * lane.batch_wire_bytes
    lane.budget_bucket_rows.push(...Array(lane.peak_batches).fill(10005))
    lane.budget_rows = lane.stored_events = lane.budget_bucket_rows.reduce((sum, rows) => sum + rows, 0)
    lane.offered_rows = (lane.batches + 25) * 2001
    lane.queue_full_rows = 25 * 2001
    lane.over_budget_rows = lane.batches * 2001 - lane.stored_events
    lane.transactions = lane.batches + lane.budget_bucket_rows.reduce((sum, rows) => sum + 2 * Math.floor(rows / 2001) + Math.max(0, Math.ceil(rows % 2001 / 1000) - 1), 0)
    lane.transaction_ns = Array(lane.transactions).fill(1000)
  }
  checkSQLPressureMetrics(JSON.stringify(crossed))
  for (const change of [
    r => { r.database = 'postgres' }, r => { r.schema_version++ }, r => { r.drain_ns = 0 },
    r => { r.rounds = 19 }, r => { r.rounds = 65 }, r => { r.registered_agents = 1 },
    r => { r.drain_ns = Number.MAX_SAFE_INTEGER + 1 }, r => { r.first_cycle.pop() }, r => { r.first_cycle.reverse() },
    r => { r.lanes.pop() }, r => { r.lanes[1] = r.lanes[0] }, r => { r.lanes[0].kind = 'private' },
    r => { r.lanes[0].peak_batches = 129 }, r => { r.lanes[0].canonical_bytes-- }, r => { r.lanes[0].peak_canonical_bytes-- },
    r => { r.lanes[0].batch_wire_bytes = 2 ** 20 + 1 }, r => { r.lanes[0].offered_rows-- },
    r => { r.lanes[0].budget_rows-- }, r => { r.lanes[0].stored_events-- },
    r => { r.lanes[0].budget_bucket_rows[0]++ }, r => { r.lanes[0].budget_bucket_rows[0] = 19999 },
    r => { r.lanes[0].full_budget_agents-- }, r => { r.lanes[0].budget_bucket_rows.pop() },
    r => { r.lanes[0].over_budget_rows = 0 }, r => { r.lanes[0].queue_full_rows = 0 },
    r => { r.lanes[0].max_chunk_rows = 1001 }, r => { r.lanes[0].transactions++ },
    r => { r.lanes[0].transaction_ns.pop() }, r => { r.lanes[0].transaction_ns[0] = -1 },
    r => { r.lanes[0].transaction_ns[0] = r.drain_ns + 1 },
    r => { r.lanes.forEach(l => { l.transaction_ns.fill(r.drain_ns) }) },
    r => { r.credential = 'private' }, r => { r.lanes[0].sql = 'private' },
    r => { const l = r.lanes[0]; l.batches -= 20; l.peak_batches--; l.peak_canonical_bytes -= l.batch_wire_bytes; l.canonical_bytes -= 20 * l.batch_wire_bytes; l.offered_rows -= 20 * 2001; l.over_budget_rows -= 20 * 2001 },
  ]) { const r = good(); change(r); assert.throws(() => checkSQLPressureMetrics(JSON.stringify(r))) }
  assert.throws(() => checkSQLPressureMetrics(' '.repeat(1048577)))
})

test('SQL pressure execution keeps only its unique actual identity and rejects skipped or private evidence', () => {
  const metrics = JSON.stringify(good()), rows = events()
  const safe = sanitizeSQLPressureResults(encode([...rows, { Action: 'output', Package, Test, Output: 'private SQL, email and destination' }]))
  assert(!safe.includes('private')); checkSQLPressureEvidence(safe, metrics)
  checkSQLPressureEvidence(encode(rows.slice(1)), metrics)
  for (const rejected of [rows.filter(r => r.Action !== 'run'), rows.slice(0, -1),
    [...rows, rows[1]], [...rows, rows[2]], [...rows, rows[3]],
    rows.map(r => r.Test && r.Action === 'pass' ? { ...r, Action: 'skip' } : r),
    rows.map(r => r.Test && r.Action === 'pass' ? { ...r, Action: 'fail' } : r),
    rows.map(r => ({ ...r, Package: 'private' })), rows.map(r => r.Test ? { ...r, Test: 'Unknown' } : r),
  ]) {
    assert.throws(() => checkSQLPressureEvidence(sanitizeSQLPressureResults(encode(rejected)), metrics))
  }
  assert.throws(() => checkSQLPressureEvidence(encode([...rows, { Action: 'output', Package, Output: 'private' }]), metrics))
  const diagnostic = Output => sanitizeSQLPressureResults(encode([{ Action: 'output', Package, Test, Output }]))
  const failed = diagnostic('    dest_pressure_native_test.go:77: sql_pressure_failed_stage=wires\n')
  assert.equal(JSON.parse(failed).Stage, 'wires')
  assert.throws(() => checkSQLPressureEvidence(failed, metrics))
  for (const output of ['    dest_pressure_native_test.go:77: sql_pressure_failed_stage=private\n', '    dest_pressure_native_test.go:77: sql_pressure_failed_stage=wires private\n', '    other.go:77: sql_pressure_failed_stage=wires\n']) assert.equal(diagnostic(output), '\n')
})

test('hosted SQL pressure exercises real file storage, unchanged worker and always tagged vet', () => {
  const workflow = readFileSync(new URL('../.github/workflows/destination-load.yml', import.meta.url), 'utf8')
  const source = readFileSync(new URL('../internal/adapters/sqlstore/dest_pressure_native_test.go', import.meta.url), 'utf8')
  const checks = readFileSync(new URL('../.github/workflows/test.yml', import.meta.url), 'utf8')
  for (const required of ['  sql-pressure:', '^TestNativeDestinationSQLPressure$', 'PSP_NATIVE_SQL_PRESSURE_METRICS', 'destination-sql-pressure.mjs sanitize', 'native-destination-sql-pressure-${{ github.sha }}', 'destination-sql-pressure-tests.json', 'destination-sql-pressure-metrics.json']) assert(workflow.includes(required), required)
  assert(checks.includes('go vet -tags destination_pressure ./internal/service/destaudit'))
  assert(checks.includes('go vet -tags destination_pressure ./internal/adapters/sqlstore'))
  assert.match(checks, /^      - name: The real destination SQL pressure still compiles\r?\n        run: go vet -tags destination_pressure \.\/internal\/adapters\/sqlstore$/m)
  for (const required of ['Open("sqlite", filepath.Join(t.TempDir(), "pressure.db"))', 'EnsureSchema(db)', 'destaudit.New', 's.Offer', 's.Run(ctx)', 'r.DestAuditRepo.BeginDestinationAudit', 'r.DestAuditRepo.WriteDestinationAuditChunk', 'time.Since(started).Nanoseconds()', 'filepath.EvalSymlinks']) {
    assert(source.includes(required), required)
  }
  for (const required of ['make([]peer, 129)', 'peers[batch-1].agent', 'peers[batch-1].panel', 'time.Now().UTC()', 'round < 64', 'fullOwners[row.Kind][row.AgentID] = true', 'lane.FullAgents == lane.PeakBatches']) assert(source.includes(required), required)
  assert(!source.includes('RowsReserved:'))
})
