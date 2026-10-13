import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'
import { checkDestinationMixedPressureResults, moduleNodeCandidate } from './destination-acceptance.mjs'

const require = value => { if (!value) throw new Error('Mixed pressure evidence invalid; private content withheld') }
const fields = (value, names) => require(value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).sort().join() === [...names].sort().join())
const count = value => Number.isSafeInteger(value) && value >= 0
const kinds = ['block', 'observe', 'trial', 'usage']
const keys = [20000, 10000, 10000, 50000]
const budgets = [20000, 20000, 20000, 40000]
const reasons = ['node_dropped', 'node_unmatched', 'queue_full', 'collect_off', 'stale_collect_revision', 'ingest_error', 'unknown_subject', 'out_of_range', 'over_budget']
const bounds = [0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 0]
const names = ['fixture_destination_sync_handler_ms', 'psp_node_control_sync_ms', 'psp_poll_ms', 'psp_poll_stage_ms{stage=sink_flush}', ...kinds.flatMap(kind => [`psp_dest_audit_begin_ms{kind=${kind}}`, `psp_dest_audit_chunk_ms{kind=${kind}}`])]

function queue(lanes) {
  require(Array.isArray(lanes) && lanes.length === 4)
  let totalBatches = 0, totalBytes = 0
  for (const [i, lane] of lanes.entries()) {
    fields(lane, ['kind', 'waiting', 'active', 'batches', 'canonical_bytes', 'batch_limit', 'byte_limit', 'owner_limit', 'owners', 'max_owner_batches'])
    require(lane.kind === kinds[i] && Object.entries(lane).filter(([k]) => k !== 'kind').every(([, v]) => count(v)))
    require(lane.batch_limit === [128, 32, 32, 64][i] && lane.byte_limit === [32, 8, 8, 16][i] * 1024 * 1024 && lane.owner_limit === [4, 1, 1, 1][i])
    require(lane.active <= 1 && lane.waiting + lane.active === lane.batches && lane.batches <= lane.batch_limit && lane.canonical_bytes <= lane.byte_limit)
    require(lane.owners <= 1 && lane.max_owner_batches <= lane.owner_limit && lane.batches <= lane.owner_limit)
    require(lane.batches ? lane.canonical_bytes > 0 && lane.owners === 1 && lane.max_owner_batches === lane.batches : lane.canonical_bytes === 0 && lane.owners === 0 && lane.max_owner_batches === 0)
    totalBatches += lane.batches; totalBytes += lane.canonical_bytes
  }
  require(totalBatches <= 256 && totalBytes <= 64 * 1024 * 1024)
  return [totalBatches, totalBytes]
}

function node(reading) {
  fields(reading, ['schema_version', 'go_heap_alloc_bytes', 'go_heap_inuse_bytes', 'go_heap_objects', 'go_goroutines', 'lanes', 'access_log'])
  require(reading.schema_version === 2 && ['go_heap_alloc_bytes', 'go_heap_inuse_bytes', 'go_heap_objects', 'go_goroutines'].every(k => count(reading[k]) && reading[k] > 0) && reading.go_heap_inuse_bytes >= reading.go_heap_alloc_bytes)
  access(reading.access_log)
  require(Array.isArray(reading.lanes) && reading.lanes.length === 4)
  for (const [i, lane] of reading.lanes.entries()) {
    fields(lane, ['kind', 'keys', 'key_limit', 'hours', 'pending', 'pending_rows', 'pending_bytes', 'retained_dropped_events', 'retained_unmatched_events'])
    require(lane.kind === kinds[i] && Object.entries(lane).filter(([k]) => k !== 'kind').every(([, v]) => count(v)))
    require(lane.key_limit === keys[i] && lane.keys <= keys[i] && lane.hours <= 50 && lane.pending <= 1 && lane.pending_rows <= (i === 3 ? 8192 : 4096) && lane.pending_bytes <= 1024 * 1024)
    require(lane.pending ? lane.pending_bytes > 0 : lane.pending_bytes === 0 && lane.pending_rows === 0)
  }
}

export function checkNativeMixedNodeReading(reading) { node(reading) }

const accessCounters = ['private_lines', 'usage_events', 'denied_events', 'unmatched_events', 'dropped_events', 'stale_events']
function access(value, quiescent = false) {
  fields(value, ['available', ...accessCounters, 'parse_queued', 'parse_limit', 'pending_dropped', 'pending_unmatched'])
  require(Object.values(value).every(count) && value.available === 1 && value.parse_limit === 4096 && value.parse_queued <= 4096)
  if (quiescent) require(value.parse_queued === 0 && value.pending_dropped === 0 && value.pending_unmatched === 0 && value.private_lines === accessCounters.slice(1).reduce((sum, key) => sum + value[key], 0))
}

function ingress(value, report) {
  fields(value, ['before', 'after', 'allowed_expected', 'allowed_received', 'allowed_not_received', 'denied_expected', 'denied_received', 'denied_not_received'])
  access(value.before, true); access(value.after, true)
  require(Object.entries(value).filter(([key]) => !['before', 'after'].includes(key)).every(([, n]) => count(n)))
  require(accessCounters.every(key => value.after[key] >= value.before[key]) && value.before.stale_events === value.after.stale_events)
  require(value.before.private_lines === 1 && value.before.usage_events === 1)
  require(value.allowed_expected === report.delivered[1] + report.delivered[2] + report.delivered[3] + report.control_successes && value.denied_expected === report.delivered[0])
  const delta = key => value.after[key] - value.before[key]
  require(value.allowed_received === delta('usage_events') + delta('dropped_events') + delta('unmatched_events') && value.denied_received === delta('denied_events'))
  require(value.allowed_received > 0 && value.allowed_received + value.allowed_not_received === value.allowed_expected && value.denied_received + value.denied_not_received === value.denied_expected)
  require(delta('private_lines') + value.allowed_not_received + value.denied_not_received === value.allowed_expected + value.denied_expected)
  const last = report.node_samples.slice(-3)
  require(last.length === 3 && last[0].elapsed_ms >= report.traffic_ms && last[2].elapsed_ms - last[0].elapsed_ms >= 1800)
  require(last.every(sample => Object.keys(value.after).every(key => sample.reading.access_log[key] === value.after[key])))
}

export function checkDestinationMixedPressureMetrics(input, database = 'sqlite') {
  require(['sqlite', 'mysql', 'postgres'].includes(database))
  require(typeof input === 'string' && Buffer.byteLength(input) <= 2 * 1024 * 1024)
  let report
  try { report = JSON.parse(input) } catch { require(false) }
  fields(report, ['schema_version', 'database', 'reserved_loss_column_reproduced', 'candidate', 'core_version', 'traffic_ms', 'partition_ms', 'native_owners', 'delivered', 'transport_failures', 'late_delivered', 'delivery_windows', 'control_successes', 'user_deliveries', 'target_requests', 'partition_sync_failures', 'recovered_sync_successes', 'frozen_transmissions', 'verified_canonical_retries', 'queue_reads', 'queue_bound_violations', 'max_total_batches', 'max_total_canonical_bytes', 'queue_peaks', 'node_samples', 'psp_samples', 'ledgers', 'access_ingress', 'observation_ms', 'histograms'])
  require(report.database === database)
  require(report.reserved_loss_column_reproduced === (database === 'mysql' ? 1 : 0))
  require(report.schema_version === 2 && report.candidate === moduleNodeCandidate.commit && report.core_version === '26.6.27' && report.native_owners === 1)
  for (const key of ['traffic_ms', 'partition_ms', 'transport_failures', 'control_successes', 'partition_sync_failures', 'recovered_sync_successes', 'queue_reads', 'queue_bound_violations', 'max_total_batches', 'max_total_canonical_bytes', 'observation_ms']) require(count(report[key]))
  require(report.traffic_ms >= 90000 && report.traffic_ms <= 95000 && report.partition_ms === 75000 && report.observation_ms >= report.traffic_ms && report.observation_ms <= report.traffic_ms + 150000)
  require(report.transport_failures === 0 && report.late_delivered === 0 && report.control_successes === 90 && report.partition_sync_failures >= 7 && report.recovered_sync_successes >= 7)
  require(Array.isArray(report.delivered) && report.delivered.length === 4 && report.delivered.every((n, i) => n === [72000, 36000, 99000, 90000][i]))
  require(Array.isArray(report.delivery_windows) && report.delivery_windows.length === 4)
  report.delivery_windows.forEach((windows, i) => require(Array.isArray(windows) && windows.length === 90 && windows.every(n => count(n) && n >= [700, 300, 1000, 900][i]) && windows.reduce((a, b) => a + b, 0) === report.delivered[i]))
  require(Array.isArray(report.user_deliveries) && report.user_deliveries.length === 200 && report.user_deliveries.every(n => count(n) && n > 0) && report.user_deliveries.reduce((a, b) => a + b, 0) === report.delivered[2])
  require(Array.isArray(report.target_requests) && report.target_requests.length === 3 && report.target_requests.every((n, i) => n === [report.delivered[1], report.delivered[2] + report.delivered[3], 0][i]))
  for (const key of ['frozen_transmissions', 'verified_canonical_retries']) require(Array.isArray(report[key]) && report[key].length === 4 && report[key].every(n => count(n) && n > 0))
  require(report.verified_canonical_retries.every(n => n === 1))
  require(report.queue_reads >= 1000 && report.queue_bound_violations === 0 && report.max_total_batches > 0 && report.max_total_batches <= 7 && report.max_total_canonical_bytes > 0 && report.max_total_canonical_bytes <= 7 * 1024 * 1024)
  queue(report.queue_peaks)
  require(report.queue_peaks.every(l => l.batches > 0))
  require(Array.isArray(report.node_samples) && report.node_samples.length >= 85 && report.node_samples.length <= 250)
  let previous = -1, saturated = false
  for (const sample of report.node_samples) {
    fields(sample, ['elapsed_ms', 'reading'])
    require(count(sample.elapsed_ms) && sample.elapsed_ms > previous && sample.elapsed_ms <= report.observation_ms + 2000 && sample.elapsed_ms - previous <= 5000)
    previous = sample.elapsed_ms; node(sample.reading)
    if (sample.elapsed_ms < report.partition_ms && sample.reading.lanes.every(l => l.keys === l.key_limit && l.pending === 1 && l.retained_dropped_events > 0)) saturated = true
  }
  require(report.node_samples[0].elapsed_ms <= 2000 && previous >= report.traffic_ms - 2000 && saturated)
  ingress(report.access_ingress, report)
  for (const sample of report.node_samples) require(accessCounters.every(key => sample.reading.access_log[key] >= report.access_ingress.before[key] && sample.reading.access_log[key] <= report.access_ingress.after[key]))
  const finalNode = report.node_samples.at(-1).reading
  require(finalNode.lanes.every(l => l.keys === 0 && l.pending === 0 && l.retained_dropped_events === 0 && l.retained_unmatched_events === 0))
  require(Array.isArray(report.psp_samples) && report.psp_samples.length >= 85 && report.psp_samples.length <= 250)
  previous = -1
  for (const sample of report.psp_samples) {
    fields(sample, ['elapsed_ms', 'go_heap_alloc_bytes', 'go_heap_inuse_bytes', 'queue'])
    require(count(sample.elapsed_ms) && sample.elapsed_ms > previous && sample.elapsed_ms <= report.observation_ms + 2000 && sample.elapsed_ms - previous <= 5000)
    previous = sample.elapsed_ms
    require(count(sample.go_heap_alloc_bytes) && sample.go_heap_alloc_bytes > 0 && count(sample.go_heap_inuse_bytes) && sample.go_heap_inuse_bytes >= sample.go_heap_alloc_bytes)
    const [batches, bytes] = queue(sample.queue)
    require(batches <= report.max_total_batches && bytes <= report.max_total_canonical_bytes)
  }
  require(report.psp_samples[0].elapsed_ms <= 1000 && previous >= report.traffic_ms - 2000)
  require(report.psp_samples.at(-1).queue.every(l => l.batches === 0))
  require(Array.isArray(report.ledgers) && report.ledgers.length === 4)
  for (const [i, ledger] of report.ledgers.entries()) {
    fields(ledger, ['kind', 'expected_events', 'stored_rows', 'stored_events', 'trial_identified_rows', 'budget_max_rows', 'losses'])
    require(ledger.kind === kinds[i] && ['expected_events', 'stored_rows', 'stored_events', 'trial_identified_rows', 'budget_max_rows'].every(k => count(ledger[k])))
    const expected = i === 3 ? report.access_ingress.allowed_received : report.delivered[i]
    require(ledger.expected_events === expected && ledger.stored_rows > 0 && ledger.stored_events === ledger.stored_rows && ledger.budget_max_rows > 0 && ledger.budget_max_rows <= budgets[i] && ledger.trial_identified_rows === 0)
    require(Array.isArray(ledger.losses) && ledger.losses.length > 0 && ledger.losses.length <= reasons.length)
    let accounted = ledger.stored_events
    const seen = new Set()
    for (const loss of ledger.losses) {
      fields(loss, ['reason', 'rows', 'events', 'unmatched'])
      require(reasons.includes(loss.reason) && !seen.has(loss.reason)); seen.add(loss.reason)
      require(['rows', 'events', 'unmatched'].every(k => count(loss[k])) && loss.rows + loss.events + loss.unmatched > 0)
      accounted += loss.rows + loss.events + loss.unmatched
    }
    require(Number.isSafeInteger(accounted) && accounted === expected && seen.has('node_dropped'))
    require(!seen.has('ingest_error') && !seen.has('unknown_subject') && !seen.has('out_of_range') && !seen.has('stale_collect_revision') && !seen.has('collect_off'))
  }
  require(Array.isArray(report.histograms) && report.histograms.length === names.length)
  const latency = report.histograms.map((histogram, i) => {
    fields(histogram, ['name', 'count', 'buckets'])
    require(histogram.name === names[i] && count(histogram.count) && histogram.count > 0 && Array.isArray(histogram.buckets) && histogram.buckets.length === bounds.length)
    let last = 0
    for (const [j, bucket] of histogram.buckets.entries()) {
      fields(bucket, ['upper_ms', 'inf', 'count'])
      require(bucket.upper_ms === bounds[j] && bucket.inf === (j === bounds.length - 1) && count(bucket.count) && bucket.count >= last && bucket.count <= histogram.count)
      last = bucket.count
    }
    require(last === histogram.count)
    const p95 = histogram.buckets.find(b => b.count >= Math.ceil(histogram.count * 0.95))
    return { name: histogram.name, count: histogram.count, p95_upper_ms: p95.inf ? null : p95.upper_ms }
  })
  return { database, native_owners: 1, delivered: report.delivered, allowed_not_received_from_core: report.access_ingress.allowed_not_received, denied_not_received_from_core: report.access_ingress.denied_not_received, sampled_node_heap_alloc_max: Math.max(...report.node_samples.map(s => s.reading.go_heap_alloc_bytes)), sampled_psp_heap_alloc_max: Math.max(...report.psp_samples.map(s => s.go_heap_alloc_bytes)), latency }
}

export function checkDestinationMixedPressureEvidence(results, metrics, database = 'sqlite') {
  require(typeof results === 'string' && Buffer.byteLength(results) <= 128 * 1024)
  checkDestinationMixedPressureResults(results)
  for (const line of results.split(/\r?\n/).filter(l => l.trim())) {
    let event
    try { event = JSON.parse(line) } catch { require(false) }
    require(event && Object.keys(event).every(k => ['Action', 'Package', 'Test', 'Elapsed'].includes(k)))
    require(event.Package === 'github.com/KazuhaHub/passwall-sub-panel/internal/app' && ['start', 'run', 'pass'].includes(event.Action))
    require(event.Test === undefined || event.Test === 'TestNativeDestinationMixedPressure')
    require(event.Action !== 'run' || event.Test === 'TestNativeDestinationMixedPressure')
    require(event.Elapsed === undefined || Number.isFinite(event.Elapsed) && event.Elapsed >= 0)
  }
  return checkDestinationMixedPressureMetrics(metrics, database)
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    require(process.argv.length === 4 || process.argv.length === 5)
    console.log(JSON.stringify(checkDestinationMixedPressureEvidence(readFileSync(process.argv[2], 'utf8'), readFileSync(process.argv[3], 'utf8'), process.argv[4])))
  } catch {
    console.error('Mixed pressure evidence invalid; private content withheld')
    process.exitCode = 1
  }
}
