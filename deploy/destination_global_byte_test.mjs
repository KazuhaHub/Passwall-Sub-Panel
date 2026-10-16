import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'
import { moduleNodeCandidate, sanitizeDestinationResults } from './destination-acceptance.mjs'
import { checkGlobalByteEvidence } from './destination-global-byte.mjs'

const kinds = ['block','observe','trial','usage'], limits = [32,8,8,16].map(n => n*1024*1024), caps = [128,32,32,64]
const sizes = [570000,286000,282000,287000], required = [524288,270600,270600,266305]
const rejectedRows = [1400,700,1300,2202]
const counts = [1400,700,1300,200], root = 'TestNativeDestinationGlobalBytePressure', Package = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
const events = () => [{Action:'start',Package},{Action:'run',Package,Test:root},{Action:'pass',Package,Test:root},{Action:'pass',Package}].map(e => JSON.stringify(e)).join('\n')
const access = (allowed, denied) => ({available:1,private_lines:allowed+denied,usage_events:allowed,denied_events:denied,unmatched_events:0,dropped_events:0,stale_events:0,parse_queued:0,parse_limit:4096,pending_dropped:0,pending_unmatched:0})
const witness = i => ({CandidateBytes:sizes[i],Batches:Math.ceil(limits[i]/sizes[i]),Bytes:limits[i]-10000,OwnerBatches:0,BatchLimit:caps[i],ByteLimit:limits[i],OwnerLimit:[4,1,1,1][i]})
const queue = (i, empty = false) => ({kind:kinds[i],waiting:empty?0:witness(i).Batches,active:0,batches:empty?0:witness(i).Batches,canonical_bytes:empty?0:limits[i]-10000,batch_limit:caps[i],byte_limit:limits[i],owner_limit:[4,1,1,1][i],owners:empty?0:witness(i).Batches,max_owner_batches:empty?0:1})
const bootstrap = () => JSON.stringify({schema_version:1,candidate:moduleNodeCandidate.commit,core_version:'26.6.27',database:'sqlite',requested_owners:65,bootstrap_stage:9,started_native_processes:65,unique_live_parents:65,unique_live_cores:65,independent_fresh_states:65,public_cache_copies:64,complete_stream_pairs:65,nonce_controls:65,shared_users:1,pressure_complete:0})
const check = (events, metrics) => checkGlobalByteEvidence(events,metrics,bootstrap())
function report() {
  const allowed = counts[1]+counts[2]+counts[3]+4, denied = counts[0]+1
  const before = access(2,0), after = access(allowed+2,denied)
  const histNames = ['fixture_destination_sync_handler_ms','psp_node_control_sync_ms','psp_poll_ms','psp_poll_stage_ms{stage=sink_flush}',...kinds.flatMap(k => [`psp_dest_audit_begin_ms{kind=${k}}`,`psp_dest_audit_chunk_ms{kind=${k}}`])]
  const bounds = [0.05,0.1,0.25,0.5,1,2,5,10,20,50,100,200,500,1000,2000,5000,10000,0]
  return {schema_version:2,candidate:moduleNodeCandidate.commit,core_version:'26.6.27',database:'sqlite',stage:6,native_owners:65,trial_users:200,fresh_owner:64,new_connections_per_owner:[...counts],required_canonical_bytes_exclusive:[...required],carrier_response_fault_ms:75001,fresh_owner_extra_hold_ms:12000,transport_failures:0,held_sql_entries:1,delegated_begins_while_held:0,carrier_byte_witness_owner:[0,0,0,0],fresh_owner_byte_witness:kinds.map((_,i) => witness(i)),fresh_owner_candidate_rows:[...rejectedRows],held_queue:kinds.map((_,i) => queue(i)),
    owners:Array.from({length:65},(_,owner) => ({owner,attempted:counts.map(n => n+1),delivered:counts.map(n => n+1),control_successes:1,durable_first_packets:[1,1,1,1],
      packets:{suppressed_success_responses:[2,2,2,2],verified_frozen_retries:[1,1,1,1],recovered_first_packets:[1,1,1,1],first_canonical_bytes:[546,550,355,269],first_rows:[1,1,1,1],new_packets:[1,1,1,1],max_new_canonical_bytes:[...sizes],rows_in_largest_new_packet:[1400,700,1300,2202],original_http_errors:0},
      admission:{accepted:[1,1,1,1],rejected:[1,1,1,1],rejected_rows:[...rejectedRows],last_rejected_rows:[...rejectedRows],same_rejected_candidate_retries:[0,0,0,0],byte_exclusive_rejections:[1,1,1,1],last_rejection:kinds.map((_,i) => witness(i))},
      access_ingress:{before:{...before},after:{...after},allowed_expected:allowed,allowed_received:allowed,allowed_not_received:0,denied_expected:denied,denied_received:denied,denied_not_received:0},stored_events:[denied-rejectedRows[0],counts[1]+1-rejectedRows[1],counts[2]+1-rejectedRows[2],allowed-rejectedRows[3]],loss_rows:[...rejectedRows],receiver_losses:rejectedRows.map(rows => [{reason:'queue_full',rows,events:0,unmatched:0}]),
      node:{schema_version:1,go_heap_alloc_bytes:10000000,go_heap_inuse_bytes:12000000,go_heap_objects:50000,go_goroutines:20,access_log:{...after},lanes:kinds.map((kind,i) => ({kind,keys:0,key_limit:[20000,10000,10000,50000][i],hours:0,pending:0,pending_rows:0,pending_bytes:0,retained_dropped_events:0,retained_unmatched_events:0}))}})),
    psp_samples:Array.from({length:76},(_,i) => ({elapsed_ms:i*1000,go_heap_alloc_bytes:10000000,go_heap_inuse_bytes:12000000,queue:kinds.map((_,kind) => queue(kind,true))})),
    latency_histograms:histNames.map(name => ({name,count:1,buckets:bounds.map((upper_ms,i) => ({upper_ms,inf:i === bounds.length-1,count:1}))})),byte_saturation_complete:1,batch_saturation_complete:0,key_saturation_complete:0,pressure_complete:0}
}

test('global byte checker keeps the actual 65-owner component separate from full pressure', () => {
  const result = check(events(),JSON.stringify(report()))
  assert.equal(result.byte_saturation_complete,true)
  assert.equal(result.pressure_complete,false)
  assert.equal(result.native_owners,65)
  assert.equal(check(sanitizeDestinationResults(events()),JSON.stringify(report())).byte_saturation_complete,true)
})

const mutations = {
  target_instead_of_actual:r => {r.native_owners=1},
  no_fresh_owner:r => {r.fresh_owner=0},
  carrier_is_reserved:r => {r.carrier_byte_witness_owner[0]=64},
  missing_carrier_rejection:r => {r.owners[0].admission.byte_exclusive_rejections[2]=0},
  owner_cap_confounded:r => {r.fresh_owner_byte_witness[0].OwnerBatches=1},
  batch_cap_confounded:r => {r.fresh_owner_byte_witness[1].Batches=32},
  equality_is_not_refusal:r => {r.fresh_owner_byte_witness[2].CandidateBytes=10000},
  changed_original_byte_cap:r => {r.fresh_owner_byte_witness[3].ByteLimit++},
  oversized_packet:r => {r.fresh_owner_byte_witness[0].CandidateBytes=1048577},
  inadequate_real_packet:r => {r.owners[4].packets.max_new_canonical_bytes[0]=required[0]},
  fabricated_received_events:r => {r.owners[5].access_ingress.allowed_not_received=1},
  unreconciled_loss:r => {r.owners[64].loss_rows[2]++},
  missing_new_candidate_loss:r => {r.owners[64].loss_rows[0]=0;r.owners[64].stored_events[0]++},
  transport_error:r => {r.transport_failures=1},
  no_actual_sql_wait:r => {r.held_sql_entries=0},
  early_sql_delegation:r => {r.delegated_begins_while_held=1},
  no_first_durable_proof:r => {r.owners[12].durable_first_packets[0]=0},
  canonical_not_recovered:r => {r.owners[12].packets.recovered_first_packets[0]=0},
  original_authentication_error:r => {r.owners[12].packets.original_http_errors=1},
  native_pending:r => {r.owners[12].node.lanes[1].pending=1},
  quiet_parser_omission:r => {r.owners[12].access_ingress.after.dropped_events++},
  missing_process:r => {r.owners.pop()},
  duplicate_process:r => {r.owners[12].owner=11},
  secret_field:r => {r.owners[12].credential='private'},
  disguised_extra_fault:r => {r.carrier_response_fault_ms=195000},
  unbounded_fresh_hold:r => {r.fresh_owner_extra_hold_ms=120001},
  relabelled_complete:r => {r.pressure_complete=1},
  relabelled_keys:r => {r.key_saturation_complete=1},
  missing_latency:r => {r.latency_histograms.pop()},
  broken_histogram:r => {r.latency_histograms[0].buckets.at(-1).count=0},
  changed_sample_caps:r => {r.psp_samples[1].queue[0].byte_limit++},
  missing_heap_window:r => {r.psp_samples=[]},
  reason_substitution_with_equal_sum:r => {r.owners[64].receiver_losses[0][0].reason='collect_off'},
  bound_request_rows_disagree:r => {r.owners[64].admission.rejected_rows[0]++},
  unbound_database_rows:r => {r.owners[64].receiver_losses[0][0].rows++},
  partial_candidate_loss:r => {r.owners[64].receiver_losses[0][0].rows=1;r.owners[64].admission.rejected_rows[0]=1;r.owners[64].admission.last_rejected_rows[0]=1;r.owners[64].loss_rows[0]=1;r.owners[64].stored_events[0]=1400},
  unexpected_node_loss:r => {r.owners[64].receiver_losses[0].push({reason:'node_dropped',rows:0,events:1,unmatched:0})},
  duplicated_loss_reason:r => {r.owners[64].receiver_losses[0]=[{reason:'queue_full',rows:1,events:0,unmatched:0},{reason:'queue_full',rows:1399,events:0,unmatched:0}]},
  unbound_small_first_packet:r => {r.fresh_owner_candidate_rows[0]=1},
  impossible_single_retained_batch:r => {r.fresh_owner_byte_witness[0].Batches=1},
  impossible_sample_reservations:r => {const q=queue(0);q.waiting=q.batches=q.owners=1;r.psp_samples[0].queue[0]=q},
  contradictory_owner_cardinality:r => {r.held_queue[0].owners=1},
  old_row_sum_schema:r => {r.schema_version=1}
}
for (const [name,mutate] of Object.entries(mutations)) test('global byte rejects '+name, () => {const r=structuredClone(report());mutate(r);assert.throws(() => check(events(),JSON.stringify(r)))})
test('global byte rejects missing actual test pass', () => {assert.throws(() => check(events().replaceAll('"pass"','"skip"'),JSON.stringify(report())))})
test('global byte requires original bootstrap process and state evidence', () => {
  assert.throws(() => checkGlobalByteEvidence(events(),JSON.stringify(report())))
  for (const field of ['started_native_processes','unique_live_cores','independent_fresh_states','complete_stream_pairs']) {
    const b=JSON.parse(bootstrap());b[field]--
    assert.throws(() => checkGlobalByteEvidence(events(),JSON.stringify(report()),JSON.stringify(b)))
  }
  assert.throws(() => checkGlobalByteEvidence(events(),JSON.stringify(report()),bootstrap(),'mysql'))
})

test('global byte phase diagnostics cannot replace successful pressure evidence', () => {
  for (const name of ['priming_ms', 'first_sql_ms', 'formation_ms', 'live_keys_ms']) {
    const diagnostic = sanitizeDestinationResults(JSON.stringify({Action:'output',Package,Test:root,Output:`native global byte phase: ${name}=0\n`}))
    assert.throws(() => checkGlobalByteEvidence(events()+'\n'+diagnostic,JSON.stringify(report()),bootstrap()))
  }
})
test('global byte keeps the original 65-owner bootstrap and same publication gate', () => {
  const source=readFileSync(new URL('../internal/app/destination_native_global_byte_test.go',import.meta.url),'utf8')
  assert(source.includes('nativeBootstrapGlobalCohort(t,') && source.includes('nativeGlobalCollectionPublication(t, cohort, token)'))
  assert(source.includes('nativeGlobalByteTransport(timed, peers, faults)') && source.includes('nativeGlobalByteOnly(facts.Last[kind], kind)'))
  assert(!source.includes('.Offer(') && !source.includes('SetSnapshot('))
  const ci=readFileSync(new URL('../.github/workflows/test.yml',import.meta.url),'utf8')
  const load=readFileSync(new URL('../.github/workflows/destination-load.yml',import.meta.url),'utf8')
  assert(ci.includes('GlobalBytePrimingKeepsIndependentOwnerOrderAndDeadline'))
  assert(load.includes('TestNativeGlobalBytePrimingKeepsIndependentOwnerOrderAndDeadline'))
  assert(load.slice(load.indexOf('\n  global-byte:')).includes('TestNativeGlobalWorkerProbeKeepsFreshConnectionsAndResponseSemantics'))
  assert(source.includes('faultEnd := start.Add(75 * time.Second)') && source.includes('context.WithDeadline(t.Context(), faultEnd)'))
  assert(source.indexOf('nativePrimeGlobalByteOwners(ctx, 65,') < source.indexOf('global byte first packets durable in original SQL'))
  assert(source.indexOf('global byte first packets durable in original SQL') < source.indexOf('storage.pause()'))
})
