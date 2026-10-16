import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'
import { checkDestinationGlobalByteResults, moduleNodeCandidate, isSafeDestinationDiagnostic } from './destination-acceptance.mjs'
import { checkGlobalBootstrapMetrics } from './destination-global-bootstrap.mjs'

const need = x => { if (!x) throw new Error('Global byte evidence rejected; private content withheld') }
const count = n => Number.isSafeInteger(n) && n >= 0
const fields = (v, keys) => need(v && !Array.isArray(v) && Object.keys(v).sort().join() === [...keys].sort().join())
const vector = (v, check = count) => need(Array.isArray(v) && v.length === 4 && v.every(check))
const kinds = ['block', 'observe', 'trial', 'usage'], batches = [128,32,32,64], bytes = [32,8,8,16].map(x => x*1024*1024), owners = [4,1,1,1]
const minimum = bytes.map((n,i) => Math.floor(n/Math.min(64,batches[i]-1)))
const numeric = v => need(Object.values(v).every(count))
function witness(p, i) {
  fields(p, ['CandidateBytes','Batches','Bytes','OwnerBatches','BatchLimit','ByteLimit','OwnerLimit'])
  numeric(p)
  need(p.BatchLimit === batches[i] && p.ByteLimit === bytes[i] && p.OwnerLimit === owners[i])
  need(p.OwnerBatches === 0 && p.Batches > 0 && p.Batches < batches[i] && p.Bytes <= bytes[i])
  need(p.CandidateBytes > bytes[i]-p.Bytes && p.CandidateBytes <= 1024*1024)
}
function queue(q, i) {
  fields(q, ['kind','waiting','active','batches','canonical_bytes','batch_limit','byte_limit','owner_limit','owners','max_owner_batches'])
  need(q.kind === kinds[i]); numeric(Object.fromEntries(Object.entries(q).filter(([k]) => k !== 'kind')))
  need(q.batch_limit === batches[i] && q.byte_limit === bytes[i] && q.owner_limit === owners[i])
  need(q.batches === q.active+q.waiting && q.active <= 1 && q.batches <= batches[i] && q.canonical_bytes <= bytes[i])
  need(q.owners <= 65 && q.owners <= q.batches && q.max_owner_batches <= owners[i] && q.max_owner_batches <= q.batches)
  need((q.batches === 0) === (q.canonical_bytes === 0) && (q.batches === 0) === (q.owners === 0))
}
function access(a) {
  fields(a,['available','private_lines','usage_events','denied_events','unmatched_events','dropped_events','stale_events','parse_queued','parse_limit','pending_dropped','pending_unmatched']); numeric(a)
  need(a.available === 1 && a.parse_limit === 4096 && a.parse_queued === 0 && a.pending_dropped === 0 && a.pending_unmatched === 0)
  need(a.private_lines === a.usage_events+a.denied_events+a.unmatched_events+a.dropped_events+a.stale_events)
}
function node(n) {
  fields(n,['schema_version','go_heap_alloc_bytes','go_heap_inuse_bytes','go_heap_objects','go_goroutines','lanes','access_log'])
  need(n.schema_version === 1 && [n.go_heap_alloc_bytes,n.go_heap_inuse_bytes,n.go_heap_objects,n.go_goroutines].every(x => count(x) && x > 0))
  vector(n.lanes, () => true)
  n.lanes.forEach((lane,i) => {
    fields(lane,['kind','keys','key_limit','hours','pending','pending_rows','pending_bytes','retained_dropped_events','retained_unmatched_events'])
    need(lane.kind === kinds[i]); numeric(Object.fromEntries(Object.entries(lane).filter(([k]) => k !== 'kind')))
    need(lane.key_limit === [20000,10000,10000,50000][i])
    need(['keys','pending','pending_rows','pending_bytes','retained_dropped_events','retained_unmatched_events'].every(k => lane[k] === 0))
  })
  access(n.access_log)
}

export function checkGlobalByteEvidence(results, metrics, bootstrap, database = 'sqlite') {
  checkGlobalBootstrapMetrics(bootstrap, database)
  need(typeof results === 'string' && Buffer.byteLength(results) <= 128*1024)
  for (const line of results.trim().split(/\r?\n/)) {
    const e = JSON.parse(line)
    if (e.Action === 'output') need(isSafeDestinationDiagnostic(e) && e.Diagnostic.startsWith('global_setup_stage_') && e.Observed === undefined)
  }
  checkDestinationGlobalByteResults(results)
  need(typeof metrics === 'string' && Buffer.byteLength(metrics) <= 4*1024*1024)
  const r = JSON.parse(metrics)
  fields(r,['schema_version','candidate','core_version','database','stage','native_owners','trial_users','fresh_owner','new_connections_per_owner','required_canonical_bytes_exclusive','carrier_response_fault_ms','fresh_owner_extra_hold_ms','transport_failures','held_sql_entries','delegated_begins_while_held','carrier_byte_witness_owner','fresh_owner_byte_witness','held_queue','owners','psp_samples','latency_histograms','byte_saturation_complete','batch_saturation_complete','key_saturation_complete','pressure_complete'])
  need(r.schema_version === 1 && r.candidate === moduleNodeCandidate.commit && r.core_version === '26.6.27' && ['sqlite','mysql','postgres'].includes(database) && r.database === database)
  need(r.stage === 6 && r.native_owners === 65 && r.trial_users === 200 && r.fresh_owner === 64)
  vector(r.new_connections_per_owner); need(r.new_connections_per_owner.join() === '1400,700,1300,200')
  vector(r.required_canonical_bytes_exclusive); need(r.required_canonical_bytes_exclusive.join() === minimum.join())
  need(r.byte_saturation_complete === 1 && r.batch_saturation_complete === 0 && r.key_saturation_complete === 0 && r.pressure_complete === 0)
  need(r.transport_failures === 0 && r.held_sql_entries === 1 && r.delegated_begins_while_held === 0)
  need(count(r.carrier_response_fault_ms) && r.carrier_response_fault_ms >= 75000 && r.carrier_response_fault_ms <= 76000)
  need(count(r.fresh_owner_extra_hold_ms) && r.fresh_owner_extra_hold_ms <= 120000)
  vector(r.carrier_byte_witness_owner, n => count(n) && n < 64)
  vector(r.fresh_owner_byte_witness, () => true); vector(r.held_queue, () => true)
  r.fresh_owner_byte_witness.forEach((p,i) => { witness(p,i); need(p.CandidateBytes > minimum[i]); need(r.held_queue[i].canonical_bytes >= p.Bytes && r.held_queue[i].batches >= p.Batches) })
  r.held_queue.forEach(queue)
  need(Array.isArray(r.owners) && r.owners.length === 65)
  r.owners.forEach((row, ordinal) => {
    fields(row,['owner','attempted','delivered','control_successes','durable_first_packets','packets','admission','access_ingress','stored_events','loss_rows','node'])
    need(row.owner === ordinal && row.control_successes === 1)
    vector(row.attempted); vector(row.delivered); vector(row.durable_first_packets, n => n === 1)
    need(row.delivered.every((n,i) => n === r.new_connections_per_owner[i]+1 && n === row.attempted[i]))
    vector(row.stored_events); vector(row.loss_rows)
    const allowed = row.delivered[1]+row.delivered[2]+row.delivered[3]+row.control_successes
    row.stored_events.forEach((n,i) => need(n+row.loss_rows[i] === (i === 3 ? allowed : row.delivered[i])))
    const p = row.packets
    fields(p,['suppressed_success_responses','verified_frozen_retries','recovered_first_packets','first_canonical_bytes','first_rows','new_packets','max_new_canonical_bytes','rows_in_largest_new_packet','original_http_errors'])
    need(p.original_http_errors === 0)
    vector(p.suppressed_success_responses, n => count(n) && n > 0); vector(p.verified_frozen_retries)
    need(p.verified_frozen_retries.every((n,i) => n+1 === p.suppressed_success_responses[i]))
    vector(p.recovered_first_packets, n => n === 1); vector(p.first_canonical_bytes, n => count(n) && n > 0 && n < 4096); vector(p.first_rows, n => count(n) && n > 0 && n <= 8)
    vector(p.new_packets, n => count(n) && n > 0); vector(p.max_new_canonical_bytes)
    need(p.max_new_canonical_bytes.every((n,i) => n > minimum[i] && n <= 1024*1024))
    vector(p.rows_in_largest_new_packet, n => count(n) && n > 8 && n <= 8192)
    const a = row.admission
    fields(a,['accepted','rejected','same_rejected_candidate_retries','byte_exclusive_rejections','last_rejection'])
    for (const k of ['accepted','rejected','same_rejected_candidate_retries','byte_exclusive_rejections']) vector(a[k])
    vector(a.last_rejection, () => true)
    a.last_rejection.forEach((p,i) => { fields(p,['CandidateBytes','Batches','Bytes','OwnerBatches','BatchLimit','ByteLimit','OwnerLimit']); numeric(p); need(a.byte_exclusive_rejections[i] <= a.rejected[i] && a.same_rejected_candidate_retries[i] <= a.rejected[i] && a.accepted[i] > 0) })
    if (ordinal === r.fresh_owner) a.byte_exclusive_rejections.forEach((n,i) => need(n > 0 && row.loss_rows[i] > 0))
    const ingress = row.access_ingress
    fields(ingress,['before','after','allowed_expected','allowed_received','allowed_not_received','denied_expected','denied_received','denied_not_received'])
    access(ingress.before); access(ingress.after)
    need(ingress.allowed_expected === allowed && ingress.allowed_received === allowed && ingress.allowed_not_received === 0)
    need(ingress.denied_expected === row.delivered[0] && ingress.denied_received === row.delivered[0] && ingress.denied_not_received === 0)
    need(ingress.after.usage_events-ingress.before.usage_events === allowed && ingress.after.denied_events-ingress.before.denied_events === row.delivered[0])
    for (const k of ['unmatched_events','dropped_events','stale_events']) need(ingress.after[k] === ingress.before[k])
    node(row.node); need(Object.keys(ingress.after).every(k => row.node.access_log[k] === ingress.after[k]))
  })
  r.carrier_byte_witness_owner.forEach((owner,i) => need(r.owners[owner].admission.byte_exclusive_rejections[i] > 0))
  need(Array.isArray(r.psp_samples) && r.psp_samples.length >= 75 && r.psp_samples.length <= 1201)
  let previous = -1
  for (const s of r.psp_samples) {
    fields(s,['elapsed_ms','go_heap_alloc_bytes','go_heap_inuse_bytes','queue'])
    need(count(s.elapsed_ms) && s.elapsed_ms > previous && s.elapsed_ms <= 1200000); previous = s.elapsed_ms
    need(count(s.go_heap_alloc_bytes) && s.go_heap_alloc_bytes > 0 && count(s.go_heap_inuse_bytes) && s.go_heap_inuse_bytes > 0)
    vector(s.queue, () => true); s.queue.forEach(queue)
  }
  need(Array.isArray(r.latency_histograms) && r.latency_histograms.length === 12)
  const names = ['fixture_destination_sync_handler_ms','psp_node_control_sync_ms','psp_poll_ms','psp_poll_stage_ms{stage=sink_flush}',...kinds.flatMap(k => [`psp_dest_audit_begin_ms{kind=${k}}`,`psp_dest_audit_chunk_ms{kind=${k}}`])]
  const bounds = [0.05,0.1,0.25,0.5,1,2,5,10,20,50,100,200,500,1000,2000,5000,10000,0]
  r.latency_histograms.forEach((h,i) => {
    fields(h,['name','count','buckets']); need(h.name === names[i] && count(h.count) && Array.isArray(h.buckets) && h.buckets.length === bounds.length)
    let last = 0
    h.buckets.forEach((b,j) => { fields(b,['upper_ms','inf','count']); need(b.upper_ms === bounds[j] && b.inf === (j === bounds.length-1) && count(b.count) && b.count >= last && b.count <= h.count); last = b.count })
    need(last === h.count)
  })
  return { byte_saturation_complete:true, native_owners:65, trial_users:200, batch_saturation_complete:false, key_saturation_complete:false, pressure_complete:false }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { need(process.argv.length === 6); console.log(JSON.stringify(checkGlobalByteEvidence(readFileSync(process.argv[2],'utf8'),readFileSync(process.argv[3],'utf8'),readFileSync(process.argv[4],'utf8'),process.argv[5]))) }
  catch { console.error('Global byte evidence rejected; private content withheld'); process.exitCode = 1 }
}
