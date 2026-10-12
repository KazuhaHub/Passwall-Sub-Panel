import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'

const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore'
const root = 'TestNativeDestinationSQLPressure'
const require = value => { if (!value) throw new Error('SQL pressure evidence invalid; private content withheld') }
const fields = (value, names) => require(value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).length === names.length && names.every(name => Object.hasOwn(value, name)))
const count = value => Number.isSafeInteger(value) && value >= 0

export function sanitizeSQLPressureResults(input) {
  require(typeof input === 'string' && Buffer.byteLength(input) <= 128 * 1024)
  return input.split(/\r?\n/).filter(line => line.trim()).map(line => {
    const event = JSON.parse(line)
    if (event.Action === 'output') {
      if (event.Package !== pkg || event.Test !== root || typeof event.Output !== 'string') return ''
      const stage = event.Output.match(/^\s*dest_pressure_native_test\.go:\d+: sql_pressure_failed_stage=(owned|database|principals|controls|wires|queues|drain|schedule|ledger|evidence)\r?\n?$/)?.[1]
      return stage ? JSON.stringify({ Action: 'output', Package: pkg, Test: root, Diagnostic: 'sql_pressure_failed_stage', Stage: stage }) : ''
    }
    require(['start', 'run', 'pass', 'fail', 'skip', 'pause', 'cont'].includes(event.Action))
    return JSON.stringify({ Action: event.Action, Package: event.Package === pkg ? pkg : '[other package]',
      ...(Object.hasOwn(event, 'Test') ? { Test: event.Test === root ? root : '[unknown test]' } : {}),
      ...(Number.isFinite(event.Elapsed) && event.Elapsed >= 0 ? { Elapsed: event.Elapsed } : {}),
    })
  }).filter(Boolean).join('\n') + '\n'
}

export function checkSQLPressureMetrics(input, database = 'sqlite') {
  require(typeof input === 'string' && Buffer.byteLength(input) <= 1024 * 1024)
  const value = JSON.parse(input)
  fields(value, ['schema_version', 'database', 'rounds', 'registered_agents', 'drain_ns', 'first_cycle', 'lanes'])
  require(['sqlite', 'mysql', 'postgres'].includes(database) && value.schema_version === 1 && value.database === database)
  require(count(value.rounds) && value.rounds >= 20 && value.rounds <= 64 && value.registered_agents === 129)
  require(count(value.drain_ns) && value.drain_ns > 0 && value.drain_ns <= 8 * 60 * 1e9)
  require(JSON.stringify(value.first_cycle) === JSON.stringify(['block', 'block', 'block', 'block', 'observe', 'trial', 'usage']))
  require(Array.isArray(value.lanes) && value.lanes.length === 4)
  const caps = { block: [128, 32 * 2 ** 20], observe: [32, 8 * 2 ** 20], trial: [32, 8 * 2 ** 20], usage: [64, 16 * 2 ** 20] }
  const seen = new Set()
  let batches = 0, bytes = 0, measured = 0, stored = 0
  for (const lane of value.lanes) {
    fields(lane, ['kind', 'batches', 'canonical_bytes', 'peak_batches', 'peak_canonical_bytes', 'batch_wire_bytes', 'offered_rows', 'stored_events', 'budget_rows', 'budget_bucket_rows', 'full_budget_agents', 'queue_full_rows', 'over_budget_rows', 'transactions', 'max_chunk_rows', 'transaction_ns'])
    require(Object.hasOwn(caps, lane.kind) && !seen.has(lane.kind)); seen.add(lane.kind)
    require(Object.entries(lane).every(([name, value]) => name === 'kind' || name === 'transaction_ns' || name === 'budget_bucket_rows' || count(value)))
    const [batchCap, byteCap] = caps[lane.kind], budget = lane.kind === 'usage' ? 40000 : 20000
    require(lane.peak_batches > 0 && lane.peak_batches <= batchCap && lane.batch_wire_bytes > 0 && lane.batch_wire_bytes <= 2 ** 20)
    require(lane.batches === lane.peak_batches * value.rounds && lane.canonical_bytes === lane.batches * lane.batch_wire_bytes)
    require(lane.peak_canonical_bytes === lane.peak_batches * lane.batch_wire_bytes && lane.peak_canonical_bytes <= byteCap)
    require(lane.peak_batches + 1 > batchCap || lane.peak_canonical_bytes + lane.batch_wire_bytes > byteCap)
    require(lane.offered_rows === (lane.batches + value.rounds) * 2001 && lane.queue_full_rows === value.rounds * 2001)
    require(Array.isArray(lane.budget_bucket_rows) && lane.budget_bucket_rows.length >= lane.peak_batches && lane.budget_bucket_rows.length <= 2 * lane.peak_batches)
    require(lane.full_budget_agents === lane.peak_batches)
    require(lane.budget_bucket_rows.every(rows => count(rows) && rows > 0 && rows <= budget))
    require(lane.budget_bucket_rows.filter(rows => rows === budget).length >= lane.full_budget_agents)
    require(lane.budget_rows === lane.budget_bucket_rows.reduce((sum, rows) => sum + rows, 0))
    require(lane.stored_events === lane.budget_rows && lane.over_budget_rows > 0)
    require(lane.stored_events + lane.over_budget_rows + lane.queue_full_rows === lane.offered_rows)
    require(lane.max_chunk_rows === 1000 && Array.isArray(lane.transaction_ns) && lane.transaction_ns.length === lane.transactions)
    const additional = lane.budget_bucket_rows.reduce((sum, rows) => sum + 2 * Math.floor(rows / 2001) + Math.max(0, Math.ceil(rows % 2001 / 1000) - 1), 0)
    require(lane.transactions === lane.batches + additional)
    for (const duration of lane.transaction_ns) { require(count(duration) && duration > 0 && duration <= value.drain_ns); measured += duration }
    batches += lane.peak_batches; bytes += lane.peak_canonical_bytes; stored += lane.stored_events
  }
  require(batches <= 256 && bytes <= 64 * 2 ** 20 && measured <= value.drain_ns)
  return { peak_batches: batches, peak_canonical_bytes: bytes, stored_events: stored, measured_transaction_ns: measured }
}

export function checkSQLPressureEvidence(results, metrics, database = 'sqlite') {
  require(typeof results === 'string' && Buffer.byteLength(results) <= 128 * 1024)
  let runs = 0, passes = 0, packages = 0, starts = 0
  for (const line of results.split(/\r?\n/).filter(line => line.trim())) {
    const event = JSON.parse(line)
    require(packages === 0 && event && typeof event === 'object' && Object.keys(event).every(key => ['Action', 'Package', 'Test', 'Elapsed'].includes(key)))
    require(event.Package === pkg && ['start', 'run', 'pass'].includes(event.Action))
    require(event.Test === undefined || event.Test === root)
    require(event.Elapsed === undefined || Number.isFinite(event.Elapsed) && event.Elapsed >= 0)
    if (event.Action === 'run') { require(event.Test === root); runs++ }
    if (event.Action === 'pass' && event.Test === root) { require(runs === 1); passes++ }
    if (event.Action === 'pass' && event.Test === undefined) { require(passes === 1); packages++ }
    if (event.Action === 'start') { require(event.Test === undefined && runs === 0 && starts === 0); starts++ }
  }
  require(runs === 1 && passes === 1 && packages === 1)
  return checkSQLPressureMetrics(metrics, database)
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    if (process.argv[2] === 'sanitize') process.stdout.write(sanitizeSQLPressureResults(readFileSync(0, 'utf8')))
    else {
      checkSQLPressureEvidence(readFileSync(process.argv[2], 'utf8'), readFileSync(process.argv[3], 'utf8'), process.argv[4] ?? 'sqlite')
      console.log('Four global queue reservations and original per-agent hourly budgets exhausted through the exact requested SQL driver; all stored events, losses and bounded SQL chunks reconciled')
    }
  } catch {
    console.error('SQL pressure evidence failed; private content withheld')
    process.exitCode = 1
  }
}
