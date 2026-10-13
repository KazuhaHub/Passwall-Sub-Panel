import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'

const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/service/destaudit'
const root = 'TestNativeDestinationQueuePressure'
const require = value => { if (!value) throw new Error('Queue pressure evidence invalid; private content withheld') }
const fields = (value, names) => require(value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).length === names.length && names.every(name => Object.hasOwn(value, name)))
const count = value => Number.isSafeInteger(value) && value >= 0

export function sanitizeDestinationQueueResults(input) {
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

export function checkDestinationQueuePressureMetrics(input) {
  require(typeof input === 'string' && Buffer.byteLength(input) <= 16 * 1024)
  const value = JSON.parse(input)
  fields(value, ['schema_version', 'iterations', 'batches', 'canonical_bytes', 'retained_heap_bytes', 'sampled_peak_heap_bytes', 'lanes'])
  require(value.schema_version === 1 && value.iterations === 1)
  require(['batches', 'canonical_bytes', 'retained_heap_bytes', 'sampled_peak_heap_bytes'].every(name => count(value[name]) && value[name] > 0))
  require(value.sampled_peak_heap_bytes >= value.retained_heap_bytes)
  require(Array.isArray(value.lanes) && value.lanes.length === 4)
  const limits = { block: [128, 32 << 20], observe: [32, 8 << 20], trial: [32, 8 << 20], usage: [64, 16 << 20] }
  let batches = 0, bytes = 0
  const seen = new Set()
  for (const lane of value.lanes) {
    fields(lane, ['kind', 'batches', 'canonical_bytes', 'batch_wire_bytes', 'queue_full'])
    require(Object.hasOwn(limits, lane.kind) && !seen.has(lane.kind)); seen.add(lane.kind)
    require(['batches', 'canonical_bytes', 'batch_wire_bytes', 'queue_full'].every(name => count(lane[name])))
    const [batchLimit, byteLimit] = limits[lane.kind]
    require(lane.queue_full === 1 && lane.batches > 0 && lane.batches <= batchLimit)
    require(lane.batch_wire_bytes > 0 && lane.batch_wire_bytes <= 1024 * 1024)
    require(lane.canonical_bytes === lane.batches * lane.batch_wire_bytes && lane.canonical_bytes <= byteLimit)
    require(lane.batches === batchLimit || lane.canonical_bytes + lane.batch_wire_bytes > byteLimit)
    batches += lane.batches; bytes += lane.canonical_bytes
  }
  require(batches === value.batches && batches <= 256 && bytes === value.canonical_bytes && bytes <= 64 * 1024 * 1024)
  return { batches, canonical_bytes: bytes, retained_heap_bytes: value.retained_heap_bytes, sampled_peak_heap_bytes: value.sampled_peak_heap_bytes }
}

export function checkDestinationQueuePressureEvidence(results, metrics) {
  require(typeof results === 'string' && Buffer.byteLength(results) <= 128 * 1024)
  let runs = 0, passes = 0, packages = 0, starts = 0
  for (const line of results.split(/\r?\n/).filter(line => line.trim())) {
    const event = JSON.parse(line)
    require(packages === 0)
    require(event && typeof event === 'object' && Object.keys(event).every(key => ['Action', 'Package', 'Test', 'Elapsed'].includes(key)))
    require(event.Package === pkg && ['start', 'run', 'pass'].includes(event.Action))
    require(event.Test === undefined || event.Test === root)
    require(event.Elapsed === undefined || Number.isFinite(event.Elapsed) && event.Elapsed >= 0)
    if (event.Action === 'run') { require(event.Test === root); runs++ }
    if (event.Action === 'pass' && event.Test === root) { require(runs === 1); passes++ }
    if (event.Action === 'pass' && event.Test === undefined) { require(passes === 1); packages++ }
    if (event.Action === 'start') { require(event.Test === undefined && runs === 0 && starts === 0); starts++ }
  }
  require(runs === 1 && passes === 1 && packages === 1)
  return checkDestinationQueuePressureMetrics(metrics)
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    if (process.argv[2] === 'sanitize') process.stdout.write(sanitizeDestinationQueueResults(readFileSync(0, 'utf8')))
    else {
      checkDestinationQueuePressureEvidence(readFileSync(process.argv[2], 'utf8'), readFileSync(process.argv[3], 'utf8'))
      console.log('Four production queue lanes saturated; canonical bytes and decoded heap measured')
    }
  } catch {
    console.error('Queue pressure evidence failed; private content withheld')
    process.exitCode = 1
  }
}
