import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'
import { moduleNodeCandidate, checkDestinationGlobalBatchResults } from './destination-acceptance.mjs'
import { checkNativeMixedNodeReading } from './destination-mixed-pressure.mjs'

const need = value => { if (!value) throw new Error('Global batch evidence invalid; private content withheld') }
const count = value => Number.isSafeInteger(value) && value >= 0
const fields = (value, keys) => need(value && !Array.isArray(value) && Object.keys(value).sort().join() === [...keys].sort().join())
const kinds = ['block', 'observe', 'trial', 'usage']
const limits = [128, 32, 32, 64], bytes = [32, 8, 8, 16], owners = [4, 1, 1, 1]
const accessKeys = ['private_lines', 'usage_events', 'denied_events', 'unmatched_events', 'dropped_events', 'stale_events']

function queue(value) {
  need(Array.isArray(value) && value.length === 4)
  let batches = 0, size = 0
  for (const [i, lane] of value.entries()) {
    fields(lane, ['kind', 'waiting', 'active', 'batches', 'canonical_bytes', 'batch_limit', 'byte_limit', 'owner_limit', 'owners', 'max_owner_batches'])
    need(lane.kind === kinds[i] && Object.entries(lane).filter(([key]) => key !== 'kind').every(([, n]) => count(n)))
    need(lane.batch_limit === limits[i] && lane.byte_limit === bytes[i] * 1024 * 1024 && lane.owner_limit === owners[i])
    need(lane.active <= 1 && lane.waiting + lane.active === lane.batches && lane.batches <= lane.batch_limit && lane.canonical_bytes <= lane.byte_limit && lane.owners <= 65 && lane.max_owner_batches <= lane.owner_limit)
    need(lane.batches === 0 ? lane.canonical_bytes === 0 && lane.owners === 0 && lane.max_owner_batches === 0 : lane.canonical_bytes > 0 && lane.owners > 0 && lane.owners <= lane.batches && lane.batches <= lane.owners * lane.owner_limit)
    batches += lane.batches; size += lane.canonical_bytes
  }
  need(batches <= 256 && size <= 64 * 1024 * 1024)
  return [batches, size]
}

function access(value) {
  fields(value, ['available', ...accessKeys, 'parse_queued', 'parse_limit', 'pending_dropped', 'pending_unmatched'])
  need(Object.values(value).every(count) && value.available === 1 && value.parse_limit === 4096 && value.parse_queued === 0 && value.pending_dropped === 0 && value.pending_unmatched === 0)
  need(value.private_lines === accessKeys.slice(1).reduce((sum, key) => sum + value[key], 0))
}

export function checkGlobalBatchEvidence(tests, metrics, bootstrap, database = 'sqlite') {
  checkDestinationGlobalBatchResults(tests)
  need(typeof tests === 'string' && Buffer.byteLength(tests) <= 128 * 1024)
  let starts = 0, runs = 0, passes = 0, packages = 0
  for (const line of tests.trim().split(/\r?\n/)) {
    let event
    try { event = JSON.parse(line) } catch { need(false) }
    need(event && Object.keys(event).every(key => ['Action', 'Package', 'Test', 'Elapsed'].includes(key)))
    need(event.Package === 'github.com/KazuhaHub/passwall-sub-panel/internal/app' && ['start', 'run', 'pass'].includes(event.Action))
    need(event.Test === undefined || event.Test === 'TestNativeDestinationGlobalBatchPressure')
    need(event.Elapsed === undefined || Number.isFinite(event.Elapsed) && event.Elapsed >= 0)
    if (event.Action === 'start') { need(event.Test === undefined); starts++ }
    if (event.Action === 'run') { need(event.Test !== undefined && starts === 1); runs++ }
    if (event.Action === 'pass' && event.Test) { need(runs === 1); passes++ }
    if (event.Action === 'pass' && !event.Test) { need(passes === 1); packages++ }
  }
  need(starts === 1 && runs === 1 && passes === 1 && packages === 1)
  need(typeof bootstrap === 'string' && Buffer.byteLength(bootstrap) <= 4096)
  let boot, report
  try { boot = JSON.parse(bootstrap) } catch { need(false) }
  fields(boot, ['schema_version', 'candidate', 'core_version', 'database', 'requested_owners', 'bootstrap_stage', 'started_native_processes', 'unique_live_parents', 'unique_live_cores', 'independent_fresh_states', 'public_cache_copies', 'complete_stream_pairs', 'nonce_controls', 'shared_users', 'pressure_complete'])
  need(boot.schema_version === 1 && boot.candidate === moduleNodeCandidate.commit && boot.core_version === '26.6.27' && boot.database === database && boot.bootstrap_stage === 9)
  for (const key of ['requested_owners', 'started_native_processes', 'unique_live_parents', 'unique_live_cores', 'independent_fresh_states', 'complete_stream_pairs', 'nonce_controls']) need(boot[key] === 65)
  need(boot.public_cache_copies === 64 && boot.shared_users === 1 && boot.pressure_complete === 0)
  need(typeof metrics === 'string' && Buffer.byteLength(metrics) <= 2 * 1024 * 1024)
  try { report = JSON.parse(metrics) } catch { need(false) }
  fields(report, ['schema_version', 'candidate', 'core_version', 'database', 'stage', 'native_owners', 'shared_trial_users', 'trial_user_deliveries', 'traffic_ms', 'observation_ms', 'transport_failures', 'queue_reads', 'queue_bound_violations', 'simultaneous_batch_saturation_samples', 'max_total_batches', 'max_total_canonical_bytes', 'queue_peaks', 'held_sql_entries', 'delegated_begins_while_held', 'owners', 'psp_samples', 'histograms', 'global_batch_complete', 'byte_saturation_complete', 'key_saturation_complete', 'pressure_complete'])
  need(['sqlite', 'mysql', 'postgres'].includes(database) && report.database === database && report.schema_version === 1 && report.candidate === moduleNodeCandidate.commit && report.core_version === '26.6.27')
  need(report.stage === 5 && report.native_owners === 65 && report.shared_trial_users === 200 && report.global_batch_complete === 1)
  need(report.byte_saturation_complete === 0 && report.key_saturation_complete === 0 && report.pressure_complete === 0)
  for (const key of ['traffic_ms', 'observation_ms', 'queue_reads', 'simultaneous_batch_saturation_samples', 'max_total_canonical_bytes']) need(count(report[key]) && report[key] > 0)
  need(report.traffic_ms >= 120000 && report.traffic_ms <= 130000 && report.observation_ms >= report.traffic_ms && report.observation_ms <= report.traffic_ms + 180000)
  need(report.transport_failures === 0 && report.queue_bound_violations === 0 && report.held_sql_entries === 1 && report.delegated_begins_while_held === 0 && report.max_total_batches === 256 && report.max_total_canonical_bytes <= 64 * 1024 * 1024)
  need(report.queue_reads >= 100 && report.simultaneous_batch_saturation_samples <= report.queue_reads)
  queue(report.queue_peaks)
  need(report.queue_peaks.every((lane, i) => lane.batches === limits[i]))
  need(Array.isArray(report.trial_user_deliveries) && report.trial_user_deliveries.length === 200 && report.trial_user_deliveries.every(n => n === 390))
  need(Array.isArray(report.psp_samples) && report.psp_samples.length >= 3 && report.psp_samples.length <= 400)
  let last = -1, saturation = 0
  for (const sample of report.psp_samples) {
    fields(sample, ['elapsed_ms', 'go_heap_alloc_bytes', 'go_heap_inuse_bytes', 'queue'])
    need(count(sample.elapsed_ms) && sample.elapsed_ms > last && sample.elapsed_ms <= report.observation_ms)
    last = sample.elapsed_ms
    need(count(sample.go_heap_alloc_bytes) && sample.go_heap_alloc_bytes > 0 && count(sample.go_heap_inuse_bytes) && sample.go_heap_inuse_bytes >= sample.go_heap_alloc_bytes)
    const [total, size] = queue(sample.queue)
    need(total <= report.max_total_batches && size <= report.max_total_canonical_bytes)
    if (total === 256) saturation++
  }
  need(saturation > 0)
  need(Array.isArray(report.owners) && report.owners.length === 65)
  const rejected = [0, 0, 0, 0], reasons = ['node_dropped', 'node_unmatched', 'queue_full', 'collect_off', 'stale_collect_revision', 'ingest_error', 'unknown_subject', 'out_of_range', 'over_budget']
  for (const [owner, detail] of report.owners.entries()) {
    fields(detail, ['owner', 'delivered', 'control_successes', 'frozen_transmissions', 'verified_canonical_retries', 'access_ingress', 'final_node', 'ledgers'])
    need(detail.owner === owner && detail.control_successes === 120 && detail.delivered.length === 4 && detail.delivered.every(n => n === 1200))
    need(detail.frozen_transmissions.length === 4 && detail.frozen_transmissions.every(n => count(n) && n > 0) && detail.verified_canonical_retries.length === 4 && detail.verified_canonical_retries.every(n => n === 1))
    checkNativeMixedNodeReading(detail.final_node)
    need(detail.final_node.lanes.every(lane => lane.keys === 0 && lane.pending === 0 && lane.retained_dropped_events === 0 && lane.retained_unmatched_events === 0))
    const input = detail.access_ingress
    fields(input, ['before', 'after', 'allowed_expected', 'allowed_received', 'allowed_not_received', 'denied_expected', 'denied_received', 'denied_not_received'])
    access(input.before); access(input.after)
    need(JSON.stringify(input.after) === JSON.stringify(detail.final_node.access_log))
    need(Object.entries(input).filter(([key]) => !['before', 'after'].includes(key)).every(([, value]) => count(value)))
    need(accessKeys.every(key => input.after[key] >= input.before[key]) && input.after.stale_events === input.before.stale_events)
    const delta = key => input.after[key] - input.before[key]
    need(input.allowed_expected === 3720 && input.denied_expected === 1200 && input.allowed_received === delta('usage_events') + delta('dropped_events') + delta('unmatched_events') && input.denied_received === delta('denied_events'))
    need(input.allowed_received + input.allowed_not_received === 3720 && input.denied_received + input.denied_not_received === 1200)
    need(delta('private_lines') + input.allowed_not_received + input.denied_not_received === 4920)
    need(detail.ledgers.length === 4)
    for (const [i, ledger] of detail.ledgers.entries()) {
      fields(ledger, ['kind', 'expected_events', 'stored_rows', 'stored_events', 'trial_identified_rows', 'budget_max_rows', 'losses'])
      need(ledger.kind === kinds[i] && ledger.expected_events === (i === 3 ? input.allowed_received : 1200))
      need(['stored_rows', 'stored_events', 'trial_identified_rows', 'budget_max_rows'].every(key => count(ledger[key])))
      need(ledger.stored_rows <= ledger.stored_events && ledger.trial_identified_rows === 0 && ledger.budget_max_rows <= (i === 3 ? 40000 : 20000))
      let accounted = ledger.stored_events
      const seen = new Set()
      for (const loss of ledger.losses) {
        fields(loss, ['reason', 'rows', 'events', 'unmatched'])
        need(reasons.includes(loss.reason) && !seen.has(loss.reason)); seen.add(loss.reason)
        need([loss.rows, loss.events, loss.unmatched].every(count) && loss.rows + loss.events + loss.unmatched > 0)
        accounted += loss.rows + loss.events + loss.unmatched
        if (loss.reason === 'queue_full') rejected[i] += loss.rows
      }
      need(accounted === ledger.expected_events)
    }
  }
  need(rejected.every(n => n > 0))
  const names = ['fixture_destination_sync_handler_ms', 'psp_node_control_sync_ms', 'psp_poll_ms', 'psp_poll_stage_ms{stage=sink_flush}', ...kinds.flatMap(kind => [`psp_dest_audit_begin_ms{kind=${kind}}`, `psp_dest_audit_chunk_ms{kind=${kind}}`])]
  const bounds = [0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 0]
  need(report.histograms.length === names.length)
  for (const [i, histogram] of report.histograms.entries()) {
    fields(histogram, ['name', 'count', 'buckets'])
    need(histogram.name === names[i] && count(histogram.count) && histogram.buckets.length === bounds.length)
    let previous = 0
    for (const [j, bucket] of histogram.buckets.entries()) {
      fields(bucket, ['upper_ms', 'inf', 'count'])
      need(bucket.upper_ms === bounds[j] && bucket.inf === (j === bounds.length - 1) && count(bucket.count) && bucket.count >= previous && bucket.count <= histogram.count)
      previous = bucket.count
    }
    need(previous === histogram.count && (i < 4 || names[i].includes('_begin_') ? histogram.count > 0 : true))
  }
  return { actual_native_owners: 65, actual_trial_users: 200, max_total_batches: 256, global_batch_complete: true, byte_saturation_complete: false, key_saturation_complete: false, pressure_complete: false }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    if (![5, 6].includes(process.argv.length)) throw new Error()
    console.log(JSON.stringify(checkGlobalBatchEvidence(readFileSync(process.argv[2], 'utf8'), readFileSync(process.argv[3], 'utf8'), readFileSync(process.argv[4], 'utf8'), process.argv[5])))
  } catch { console.error('Global batch evidence invalid; private content withheld'); process.exitCode = 1 }
}
