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
    if (event.Action === 'output') return ''
    require(['start', 'run', 'pass', 'fail', 'skip', 'pause', 'cont'].includes(event.Action))
    return JSON.stringify({ Action: event.Action, Package: event.Package === pkg ? pkg : '[other package]',
      ...(Object.hasOwn(event, 'Test') ? { Test: event.Test === root ? root : '[unknown test]' } : {}),
      ...(Number.isFinite(event.Elapsed) && event.Elapsed >= 0 ? { Elapsed: event.Elapsed } : {}),
    })
  }).filter(Boolean).join('\n') + '\n'
}

export function checkSQLPressureMetrics(input) {
  require(typeof input === 'string' && Buffer.byteLength(input) <= 32 * 1024)
  const value = JSON.parse(input)
  fields(value, ['schema_version', 'database', 'drain_ns', 'first_cycle', 'lanes'])
  require(value.schema_version === 1 && value.database === 'sqlite')
  require(count(value.drain_ns) && value.drain_ns > 0 && value.drain_ns <= 8 * 60 * 1e9)
  require(JSON.stringify(value.first_cycle) === JSON.stringify(['block', 'block', 'block', 'block', 'observe', 'trial', 'usage']))
  require(Array.isArray(value.lanes) && value.lanes.length === 4)
  const caps = { block: [128, 32 * 2 ** 20], observe: [32, 8 * 2 ** 20], trial: [32, 8 * 2 ** 20], usage: [64, 16 * 2 ** 20] }
  const seen = new Set()
  let batches = 0, bytes = 0, measured = 0
  for (const lane of value.lanes) {
    fields(lane, ['kind', 'batches', 'canonical_bytes', 'batch_wire_bytes', 'offered_rows', 'stored_events', 'budget_rows', 'queue_full_rows', 'over_budget_rows', 'transactions', 'max_chunk_rows', 'transaction_ns'])
    require(Object.hasOwn(caps, lane.kind) && !seen.has(lane.kind)); seen.add(lane.kind)
    require(Object.entries(lane).every(([name, value]) => name === 'kind' || name === 'transaction_ns' || count(value)))
    const [batchCap, byteCap] = caps[lane.kind], budget = lane.kind === 'usage' ? 40000 : 20000
    require(lane.batches > 0 && lane.batches <= batchCap && lane.batch_wire_bytes > 0 && lane.batch_wire_bytes <= 2 ** 20)
    require(lane.canonical_bytes === lane.batches * lane.batch_wire_bytes && lane.canonical_bytes <= byteCap)
    require(lane.batches + 1 > batchCap || lane.canonical_bytes + lane.batch_wire_bytes > byteCap)
    require(lane.offered_rows === (lane.batches + 1) * 2001 && lane.queue_full_rows === 2001)
    require(lane.stored_events === budget && lane.budget_rows === budget && lane.over_budget_rows > 0)
    require(lane.stored_events + lane.over_budget_rows + lane.queue_full_rows === lane.offered_rows)
    require(lane.max_chunk_rows === 1000 && Array.isArray(lane.transaction_ns) && lane.transaction_ns.length === lane.transactions)
    const fullBatches = Math.floor(budget / 2001), remainder = budget % 2001
    require(lane.transactions === lane.batches + 2 * fullBatches + Math.max(0, Math.ceil(remainder / 1000) - 1))
    for (const duration of lane.transaction_ns) { require(count(duration) && duration > 0 && duration <= value.drain_ns); measured += duration }
    batches += lane.batches; bytes += lane.canonical_bytes
  }
  require(batches <= 256 && bytes <= 64 * 2 ** 20 && measured <= value.drain_ns)
  return { batches, canonical_bytes: bytes, stored_events: 100000, measured_transaction_ns: measured }
}

export function checkSQLPressureEvidence(results, metrics) {
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
  return checkSQLPressureMetrics(metrics)
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    if (process.argv[2] === 'sanitize') process.stdout.write(sanitizeSQLPressureResults(readFileSync(0, 'utf8')))
    else {
      checkSQLPressureEvidence(readFileSync(process.argv[2], 'utf8'), readFileSync(process.argv[3], 'utf8'))
      console.log('Four full production lanes drained through real SQLite; 100000 stored events, all quota losses and bounded SQL chunk durations reconciled')
    }
  } catch {
    console.error('SQL pressure evidence failed; private content withheld')
    process.exitCode = 1
  }
}
