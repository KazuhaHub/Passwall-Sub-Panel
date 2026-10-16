import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'
import { moduleNodeCandidate, isSafeDestinationDiagnostic } from './destination-acceptance.mjs'

const need = value => { if (!value) throw new Error('Real batch formation evidence rejected; private content withheld') }
const count = value => Number.isSafeInteger(value) && value >= 0
const fields = (value, keys) => need(value && !Array.isArray(value) && Object.keys(value).sort().join() === [...keys].sort().join())
const vector = (value, predicate = count) => need(Array.isArray(value) && value.length === 4 && value.every(predicate))
const root = 'TestNativeDestinationByteFormation', pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'

export function checkByteFormationEvidence(results, metrics) {
  need(typeof results === 'string' && Buffer.byteLength(results) <= 128 * 1024)
  let started = 0, ran = 0, passed = 0, packages = 0
  for (const line of results.trim().split(/\r?\n/)) {
    let e
    try { e = JSON.parse(line) } catch { need(false) }
    need(e && Object.keys(e).every(k => ['Action', 'Package', 'Test', 'Elapsed', 'Diagnostic', 'Observed'].includes(k)))
    need(e.Package === pkg && ['start', 'run', 'pass', 'output'].includes(e.Action) && (e.Test === undefined || e.Test === root))
    need(e.Elapsed === undefined || Number.isFinite(e.Elapsed) && e.Elapsed >= 0)
    if (e.Action === 'output') {
      need(isSafeDestinationDiagnostic(e) && e.Diagnostic.startsWith('global_setup_stage_') && e.Observed === undefined)
      continue
    }
    need(e.Diagnostic === undefined && e.Observed === undefined)
    if (e.Action === 'start') { need(!e.Test && !started); started++ }
    if (e.Action === 'run') { need(e.Test === root && started === 1 && !ran); ran++ }
    if (e.Action === 'pass' && e.Test) { need(ran === 1 && !passed); passed++ }
    if (e.Action === 'pass' && !e.Test) { need(passed === 1 && !packages); packages++ }
  }
  need(started === 1 && ran === 1 && passed === 1 && packages === 1)
  need(typeof metrics === 'string' && Buffer.byteLength(metrics) <= 64 * 1024)
  let r
  try { r = JSON.parse(metrics) } catch { need(false) }
  fields(r, ['schema_version', 'candidate', 'core_version', 'database', 'stage', 'native_owners', 'target_native_owners', 'required_canonical_bytes_exclusive', 'trial_users', 'attempted', 'delivered', 'transport_failures', 'response_fault_ms', 'durable_first_packets', 'packets', 'held_queue', 'held_sql_entries', 'delegated_begins_while_held', 'access_ingress', 'stored_events', 'loss_rows', 'formation_complete', 'byte_saturation_complete', 'pressure_complete'])
  need(r.schema_version === 2 && r.candidate === moduleNodeCandidate.commit && r.core_version === '26.6.27' && r.database === 'sqlite')
  need(r.stage === 3 && r.native_owners === 1 && r.trial_users === 200 && r.formation_complete === 1)
  need(r.target_native_owners === 65)
  const byteLimits = [32, 8, 8, 16].map(n => n * 1024 * 1024), batchLimits = [128, 32, 32, 64]
  const minimum = byteLimits.map((bytes, i) => Math.floor(bytes / Math.min(r.target_native_owners - 1, batchLimits[i] - 1)))
  vector(r.required_canonical_bytes_exclusive)
  need(r.required_canonical_bytes_exclusive.every((n, i) => n === minimum[i]))
  need(r.byte_saturation_complete === 0 && r.pressure_complete === 0 && r.transport_failures === 0)
  need(count(r.response_fault_ms) && r.response_fault_ms >= 75000 && r.response_fault_ms <= 76000)
  vector(r.attempted, n => n === 2049); vector(r.delivered, n => n === 2049); vector(r.durable_first_packets, n => n === 1)
  vector(r.stored_events); vector(r.loss_rows)
  for (let i = 0; i < 4; i++) need(r.stored_events[i] > 0 && r.stored_events[i] + r.loss_rows[i] === (i === 3 ? 6147 : 2049))
  fields(r.packets, ['suppressed_success_responses', 'verified_frozen_retries', 'recovered_first_packets', 'first_canonical_bytes', 'first_rows', 'new_packets', 'max_new_canonical_bytes', 'rows_in_largest_new_packet', 'original_http_errors'])
  for (const key of ['suppressed_success_responses', 'verified_frozen_retries', 'new_packets']) vector(r.packets[key], n => count(n) && n <= 1000000)
  need(r.packets.suppressed_success_responses.every(n => n > 0) && r.packets.new_packets.every(n => n > 0) && r.packets.original_http_errors === 0)
  need(r.packets.verified_frozen_retries.every((n, i) => n + 1 === r.packets.suppressed_success_responses[i]))
  vector(r.packets.recovered_first_packets, n => n === 1)
  vector(r.packets.first_rows, n => count(n) && n >= 1 && n <= 8)
  vector(r.packets.first_canonical_bytes, n => count(n) && n > 0 && n < 4096)
  vector(r.packets.max_new_canonical_bytes, n => count(n) && n <= 1024 * 1024)
  need(r.packets.max_new_canonical_bytes.every((n, i) => n > minimum[i]))
  vector(r.packets.rows_in_largest_new_packet, n => count(n) && n > 8 && n <= 8192)
  need(r.held_sql_entries === 1 && r.delegated_begins_while_held === 0 && Array.isArray(r.held_queue) && r.held_queue.length === 4)
  for (const [i, lane] of r.held_queue.entries()) {
    fields(lane, ['kind', 'waiting', 'active', 'batches', 'canonical_bytes', 'batch_limit', 'byte_limit', 'owner_limit', 'owners', 'max_owner_batches'])
    need(lane.kind === ['block', 'observe', 'trial', 'usage'][i] && Object.entries(lane).filter(([k]) => k !== 'kind').every(([, n]) => count(n)))
    need(lane.batch_limit === [128, 32, 32, 64][i] && lane.byte_limit === [32, 8, 8, 16][i] * 1024 * 1024 && lane.owner_limit === [4, 1, 1, 1][i])
    need(lane.active <= 1 && lane.waiting + lane.active === lane.batches && lane.batches > 0 && lane.batches <= lane.owner_limit)
    need(lane.canonical_bytes > minimum[i] && lane.canonical_bytes <= lane.byte_limit && lane.owners === 1 && lane.max_owner_batches === lane.batches)
  }
  const ingress = r.access_ingress
  fields(ingress, ['before', 'after', 'allowed_expected', 'allowed_received', 'allowed_not_received', 'denied_expected', 'denied_received', 'denied_not_received'])
  const keys = ['private_lines', 'usage_events', 'denied_events', 'unmatched_events', 'dropped_events', 'stale_events']
  for (const a of [ingress.before, ingress.after]) {
    fields(a, ['available', ...keys, 'parse_queued', 'parse_limit', 'pending_dropped', 'pending_unmatched'])
    need(Object.values(a).every(count) && a.available === 1 && a.parse_limit === 4096 && a.parse_queued === 0 && a.pending_dropped === 0 && a.pending_unmatched === 0)
    need(a.private_lines === keys.slice(1).reduce((sum, k) => sum + a[k], 0))
  }
  const delta = k => ingress.after[k] - ingress.before[k]
  need(keys.every(k => delta(k) >= 0) && delta('unmatched_events') === 0 && delta('dropped_events') === 0 && delta('stale_events') === 0)
  need(ingress.allowed_expected === 6147 && ingress.allowed_received === 6147 && ingress.allowed_not_received === 0 && delta('usage_events') === 6147)
  need(ingress.denied_expected === 2049 && ingress.denied_received === 2049 && ingress.denied_not_received === 0 && delta('denied_events') === 2049 && delta('private_lines') === 8196)
  return { formation_complete: true, native_owners: 1, trial_users: 200, byte_saturation_complete: false, pressure_complete: false }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    need(process.argv.length === 4)
    console.log(JSON.stringify(checkByteFormationEvidence(readFileSync(process.argv[2], 'utf8'), readFileSync(process.argv[3], 'utf8'))))
  } catch {
    console.error('Real batch formation evidence rejected; private content withheld')
    process.exitCode = 1
  }
}
