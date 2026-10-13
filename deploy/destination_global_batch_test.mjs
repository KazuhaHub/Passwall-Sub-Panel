import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { checkGlobalBatchEvidence } from './destination-global-batch.mjs'
import { moduleNodeCandidate } from './destination-acceptance.mjs'

const kinds = ['block', 'observe', 'trial', 'usage']
const limits = [128,32,32,64], bytes = [32,8,8,16], owners = [4,1,1,1]
const root = 'TestNativeDestinationGlobalBatchPressure'
const events = () => [{Action:'start'},{Action:'run',Test:root},{Action:'pass',Test:root},{Action:'pass'}].map(e=>JSON.stringify({Package:'github.com/KazuhaHub/passwall-sub-panel/internal/app',...e})).join('\n')
const queue = (full = true) => kinds.map((kind,i)=>({kind,waiting:full?limits[i]-(i===0?1:0):0,active:full&&i===0?1:0,batches:full?limits[i]:0,canonical_bytes:full?limits[i]*1000:0,batch_limit:limits[i],byte_limit:bytes[i]*1024*1024,owner_limit:owners[i],owners:full?(i===0?32:limits[i]):0,max_owner_batches:full?owners[i]:0}))
const access = (usage = 1, denied = 0) => ({available:1,private_lines:usage+denied,usage_events:usage,denied_events:denied,unmatched_events:0,dropped_events:0,stale_events:0,parse_queued:0,parse_limit:4096,pending_dropped:0,pending_unmatched:0})
const node = () => ({schema_version:2,go_heap_alloc_bytes:1000,go_heap_inuse_bytes:2000,go_heap_objects:10,go_goroutines:10,lanes:kinds.map((kind,i)=>({kind,keys:0,key_limit:[20000,10000,10000,50000][i],hours:0,pending:0,pending_rows:0,pending_bytes:0,retained_dropped_events:0,retained_unmatched_events:0})),access_log:access(3721,1200)})
const boot = () => ({schema_version:1,candidate:moduleNodeCandidate.commit,core_version:'26.6.27',database:'sqlite',requested_owners:65,bootstrap_stage:9,started_native_processes:65,unique_live_parents:65,unique_live_cores:65,independent_fresh_states:65,public_cache_copies:64,complete_stream_pairs:65,nonce_controls:65,shared_users:1,pressure_complete:0})
const report = () => {
  const names=['fixture_destination_sync_handler_ms','psp_node_control_sync_ms','psp_poll_ms','psp_poll_stage_ms{stage=sink_flush}',...kinds.flatMap(k=>[`psp_dest_audit_begin_ms{kind=${k}}`,`psp_dest_audit_chunk_ms{kind=${k}}`])]
  const bounds=[0.05,0.1,0.25,0.5,1,2,5,10,20,50,100,200,500,1000,2000,5000,10000,0]
  return {schema_version:1,candidate:moduleNodeCandidate.commit,core_version:'26.6.27',database:'sqlite',stage:5,native_owners:65,shared_trial_users:200,trial_user_deliveries:Array(200).fill(390),traffic_ms:120000,observation_ms:125000,transport_failures:0,queue_reads:12500,queue_bound_violations:0,simultaneous_batch_saturation_samples:3000,max_total_batches:256,max_total_canonical_bytes:256000,queue_peaks:queue(),held_sql_entries:1,delegated_begins_while_held:0,owners:Array.from({length:65},(_,owner)=>({owner,delivered:[1200,1200,1200,1200],control_successes:120,frozen_transmissions:[2,2,2,2],verified_canonical_retries:[1,1,1,1],access_ingress:{before:access(),after:access(3721,1200),allowed_expected:3720,allowed_received:3720,allowed_not_received:0,denied_expected:1200,denied_received:1200,denied_not_received:0},final_node:node(),ledgers:kinds.map((kind,i)=>({kind,expected_events:i===3?3720:1200,stored_rows:1000,stored_events:1000,trial_identified_rows:0,budget_max_rows:1000,losses:[{reason:'queue_full',rows:i===3?2720:200,events:0,unmatched:0}]}))})),psp_samples:[0,100000,125000].map((elapsed_ms,i)=>({elapsed_ms,go_heap_alloc_bytes:1000,go_heap_inuse_bytes:2000,queue:queue(i===1)})),histograms:names.map(name=>({name,count:1,buckets:bounds.map((upper_ms,i)=>({upper_ms,inf:i===bounds.length-1,count:1}))})),global_batch_complete:1,byte_saturation_complete:0,key_saturation_complete:0,pressure_complete:0}
}

test('global batch proof requires all actual owners, capacity and ledger facts and excludes full pressure',()=>{
  assert.equal(checkGlobalBatchEvidence(events(),JSON.stringify(report()),JSON.stringify(boot())).pressure_complete,false)
  for(const mutate of [r=>{r.native_owners=1},r=>{r.stage=4},r=>{r.simultaneous_batch_saturation_samples=0},r=>{r.max_total_batches=255},r=>{r.delegated_begins_while_held=1},r=>{r.pressure_complete=1},r=>{r.key_saturation_complete=1},r=>{r.owners[1].owner=0},r=>{r.owners[2].ledgers[0].losses[0].rows--},r=>{r.trial_user_deliveries[3]=0},r=>{r.owners[4].final_node.private_path='/private'},r=>{r.psp_samples[1].queue[3].batches=63},r=>{r.histograms[0].count=0}]) {
    const r=report();mutate(r);assert.throws(()=>checkGlobalBatchEvidence(events(),JSON.stringify(r),JSON.stringify(boot())))
  }
  for(const mutate of [b=>{b.unique_live_cores=64},b=>{b.pressure_complete=1},b=>{b.shared_users=200}]) {const b=boot();mutate(b);assert.throws(()=>checkGlobalBatchEvidence(events(),JSON.stringify(report()),JSON.stringify(b)))}
  assert.throws(()=>checkGlobalBatchEvidence(events()+'\n'+JSON.stringify({Action:'output',Output:'(cached)'}),JSON.stringify(report()),JSON.stringify(boot())))
  assert.throws(()=>checkGlobalBatchEvidence(events().replace('"run"','"skip"'),JSON.stringify(report()),JSON.stringify(boot())))
})

test('global batch driver uses real 65-owner traffic and original storage, with three owned databases',()=>{
  const source=readFileSync(new URL('../internal/app/destination_native_global_batch_test.go',import.meta.url),'utf8')
  for(const required of ['nativeBootstrapGlobalCohort','auditMetrics: true','nativeGlobalMixedTransport(timed, peers)','nativePrepareTrialLoad','nativeMutationReceiptMatches','nativeCollectionReceiptMatches','storage.pause()','storage.resume(pause)','nativeVLESSHost','nativeLoadBlockedRequest','lane.BatchLimit','lane.ByteLimit','m.Saturation++','nativeMixedIngressBetween','sql(nativeMixedLossSQL)','queueFull[i] += loss.Rows']) assert(source.includes(required))
  assert(source.indexOf('peer.live = nativeBindLiveCore')>source.indexOf('all real global trial policies and 200-user paired rosters'))
  assert.doesNotMatch(source,/SetSnapshot\(|\.collector\.Ack\(|runtime\.GC\(|time\.Now\(\)\.Add\(-/)
  assert(source.includes('t.Cleanup(writeMetrics)') && source.includes('m.Stage = 2\n\twriteMetrics()') && source.includes('m.Stage = 3\n\twriteMetrics()'))
  assert(source.includes('memberConcurrency: 8'))
  const setup=readFileSync(new URL('../internal/app/destination_native_trial_load_test.go',import.meta.url),'utf8')
  assert(setup.includes('memberConcurrency := 1') && setup.includes('memberConcurrency < 1 || memberConcurrency > 8'))
  assert(setup.includes('for range memberConcurrency') && setup.includes('members.Wait(); close(results)'))
  assert(setup.indexOf('for err := range results') < setup.indexOf('mixed.memberReady(completed)'))
  assert(setup.includes('completed != len(users)'))
  const workflow=readFileSync(new URL('../.github/workflows/destination-load.yml',import.meta.url),'utf8')
  const job=workflow.slice(workflow.indexOf('\n  global-batch:'))
  assert(job.startsWith('\n  global-batch:'))
  for(const db of ['sqlite','mysql','postgres']) assert(job.includes(`database: ${db}`))
  assert(job.includes(`-test.run '^${root}$'`)&&job.includes('PSP_NATIVE_GLOBAL_BATCH_METRICS=')&&job.includes('destination-global-batch.mjs'))
  assert(job.includes(`ref: ${moduleNodeCandidate.commit}`)&&job.includes('PSP_NATIVE_CORE_VERSION=26.6.27'))
  assert(job.includes('destination-global-bootstrap-metrics.json')&&job.includes('destination-global-batch-metrics.json'))
})
