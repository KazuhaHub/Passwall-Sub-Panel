import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import {spawnSync} from 'node:child_process'
import {test} from 'node:test'
import {checkRetentionMetrics,checkRetentionEvidence} from './destination-retention.mjs'

// Synthetic rejection vectors only, never real elapsed-time or Node evidence.
const candidate='61f30239e09d62b05bd59df7d9d28a5071790556',hour=3600000,start=Date.UTC(2026,9,16)
const names=['captured','future_boundaries','before_cleanup26','after_cleanup26','after_restart26','after_node_replay26','after_node_expiry48','after_stale_rejection48','after_hit_cleanup48','before_marker_expiry72','after_marker_expiry72','after_budget_hour_expiry']
function good(){
  const received=start+60000,isolated=start+120001,after26=isolated+26*hour
  const times=[start+61000,start+120000,after26,after26+1000,after26+2000,after26+3000,start+48*hour+15000,start+48*hour+16000,start+48*hour+17000,received+72*hour-2000,received+72*hour+2000,start+73*hour+2000]
  const sql=(lane,i)=>({hour_ms:start,rows:i<8?1:0,events:i<8?1:0,markers:i<10?1:0,received_unix_ms:i<10?received:0,budget_rows:i===0?1:i===11?0:lane===0?2:1,budget_keys:i<11?1:0,total_budget_rows:i===0?1:i===11?0:lane===0?2:1})
  return {schema_version:1,candidate,core_version:'26.6.27',database:'sqlite',native_owners:1,started_unix_ms:start,ended_unix_ms:times[11]+1000,window_ms:times[11]+1000-start,control_successes:4380,fresh_node_samples:4380,original_hour_ms:[start,start],original_events:[1,1],observe_pending_before_expiry:1,observe_pending_after_expiry:0,out_of_range_rows:2,
    phases:names.map((name,i)=>({name,at_unix_ms:times[i],original_sql:[sql(0,i),sql(1,i)]})),
    authenticated_boundary_fixtures:[{hour_ms:start+hour,rows:1,events:1,markers:1,received_unix_ms:start+90000,budget_rows:2,budget_keys:1,total_budget_rows:2},{hour_ms:start+3*hour,rows:0,events:0,markers:1,received_unix_ms:start+100000,budget_rows:2,budget_keys:1,total_budget_rows:2},{hour_ms:start,rows:0,events:0,markers:1,received_unix_ms:start+48*hour+15500,budget_rows:0,budget_keys:1,total_budget_rows:2}],
    transport:{phase:3,captured_rows:[1,1],canonical_bytes:[400,410],suppressed_success_responses:[2,2],canonical_retries:[3,3],offline_requests:1000,original_block_success_responses:1,original_http_errors:0,handler_replacements:1,isolated_unix_ms:isolated,replaced_unix_ms:after26+2200,resumed_unix_ms:after26+2300},complete:1}
}
const bootstrap=()=>({schema_version:1,candidate,core_version:'26.6.27',database:'sqlite',requested_owners:1,bootstrap_stage:9,started_native_processes:1,unique_live_parents:1,unique_live_cores:1,independent_fresh_states:1,public_cache_copies:0,complete_stream_pairs:1,nonce_controls:1,shared_users:1,pressure_complete:0})
test('retention checker separates real26h replay48h expiry72h markers and whole-hour budgets',()=>{
  const result=checkRetentionMetrics(good(),bootstrap());assert.equal(result.minimum_real_hours,72);assert.equal(result.original_lanes,2)
  for(const mutate of [m=>m.window_ms=3600000,m=>m.phases.splice(4,1),m=>m.phases[5].original_sql[0].events++,m=>m.phases[5].original_sql[0].total_budget_rows++,m=>m.phases[4].original_sql[0].received_unix_ms++,m=>m.phases[9].original_sql[0].markers=0,m=>m.phases[10].original_sql[0].markers=1,m=>m.phases[11].original_sql[0].budget_keys=1,m=>m.authenticated_boundary_fixtures[0].hour_ms=start,m=>m.authenticated_boundary_fixtures[2].rows=1,m=>m.observe_pending_after_expiry=1,m=>m.transport.resumed_unix_ms=m.started_unix_ms,m=>m.private_path='/private',m=>m.control_successes=0]){const m=good();mutate(m);assert.throws(()=>checkRetentionMetrics(m,bootstrap()))}
  const b=bootstrap();b.requested_owners=65;assert.throws(()=>checkRetentionMetrics(good(),b))
})
test('retention execution requires actual72h root and package PASS and strips private output',()=>{
  const pkg='github.com/KazuhaHub/passwall-sub-panel/internal/app',root='TestNativeDestinationRetentionReal72Hours'
  const events=[{Action:'start',Package:pkg},{Action:'run',Package:pkg,Test:root},{Action:'output',Package:pkg,Test:root,Output:'private credential and database path'},{Action:'pass',Package:pkg,Test:root,Elapsed:262803},{Action:'pass',Package:pkg,Elapsed:262804}]
  const encode=x=>x.map(e=>JSON.stringify(e)).join('\n')
  const result=checkRetentionEvidence(encode(events),good(),bootstrap());assert.equal(result.sanitized.length,4);assert(!JSON.stringify(result).includes('credential'))
  for(const mutate of [e=>e[3].Elapsed=3600,e=>e[4].Elapsed=3600,e=>e[3].Action='skip',e=>e[2].Output='WARNING: DATA RACE',e=>e.push(e[4]),e=>e[1].Test='ShortBoundaryTest']){const e=structuredClone(events);mutate(e);assert.throws(()=>checkRetentionEvidence(encode(e),good(),bootstrap()))}
})
test('retention runner preserves fixed source isolation and76h lifetime without long execution',()=>{
  const script=readFileSync(new URL('./run-destination-retention.sh',import.meta.url),'utf8')
  for(const text of ['PSP_NATIVE_RETENTION=72h','-test.timeout=76h',"^TestNativeDestinationRetentionReal72Hours$",'v0.2.1-0.20261009195400-11a90e9bc95a','-buildvcs=true','test ! -e "$run_dir"','destination-retention.mjs'])assert(script.includes(text))
  assert(!/export GITHUB_ACTIONS|rm -rf|PSP_NATIVE_TRIAL_DAY/.test(script))
  const result=spawnSync('bash',['-n'],{input:script,encoding:'utf8'});assert.equal(result.status,0,result.stderr)
})
