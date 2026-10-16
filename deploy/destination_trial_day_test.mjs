import assert from 'node:assert/strict'
import {createHash} from 'node:crypto'
import {readFileSync} from 'node:fs'
import {spawnSync} from 'node:child_process'
import {test} from 'node:test'
import {checkTrialDayEvidence,checkTrialDayMetrics} from './destination-trial-day.mjs'

// Synthetic validator vectors only; these never constitute a real-day run.
const candidate='61f30239e09d62b05bd59df7d9d28a5071790556'
const names=['fixture_destination_sync_handler_ms','psp_node_control_sync_ms','psp_poll_ms','psp_poll_stage_ms{stage=sink_flush}','psp_dest_audit_begin_ms{kind=block}','psp_dest_audit_begin_ms{kind=trial}','psp_dest_audit_chunk_ms{kind=block}','psp_dest_audit_chunk_ms{kind=trial}']
function good(){
  const start=Date.UTC(2026,9,16)
  return {schema_version:1,complete:1,candidate,core_version:'26.6.27',database:'sqlite',native_owners:10,users:200,sites:300,domain_set_sha256:createHash('sha256').update(Array.from({length:300},(_,i)=>`trial-day-${String(i).padStart(3,'0')}.test`).join('\n')).digest('hex'),started_unix_ms:start,ended_unix_ms:start+86400000,window_ms:86400000,
    sqlite_file_bytes:[21000000,0,0],loss_before:[0,0,0],loss_after:[0,0,0],latency_window_ms:86400000,sql_drain_ms:0,node_access_baseline:Array.from({length:10},()=>({available:1,private_lines:0,usage_events:0,denied_events:0,unmatched_events:0,dropped_events:0,stale_events:0,parse_queued:0,parse_limit:4096,pending_dropped:0,pending_unmatched:0})),
    trial_attempted:1440000,trial_delivered:1440000,trial_target_requests:1440000,trial_stored_events:1440000,block_attempted:864000,block_delivered:864000,block_denied:864000,block_stored_events:864000,transport_failures:0,late_or_wrong_hour:0,trial_identified_rows:0,loss_rows:0,loss_events:0,loss_unmatched:0,control_successes:14400,trial_stored_rows:72000,dest_hits_allocated_bytes:20000000,sqlite_allocated_bytes:21000000,
    user_hour_deliveries:Array.from({length:24},()=>Array(200).fill(300)),user_hour_unique_sites:Array.from({length:24},()=>Array(200).fill(300)),owner_hour_deliveries:Array.from({length:24},()=>Array(10).fill(6000)),
    node_hours:Array.from({length:240},(_,i)=>({hour:Math.floor(i/10),owner:i%10,samples:1800,peak_keys:[1,0,300,0],peak_retained_dropped:[0,0,0,0],peak_retained_unmatched:[0,0,0,0],peak_access_dropped:0,peak_access_unmatched:0,peak_access_stale:0,peak_pending_dropped:0,peak_pending_unmatched:0,peak_heap_bytes:1048576})),
    sql_nodes:Array.from({length:10},(_,i)=>({owner:i,trial_rows:7200,trial_events:144000,block_events:86400,distinct_sites:300})),utc_sql_hours:Array.from({length:24},(_,i)=>({hour_ms:start+i*3600000,trial_rows:3000,trial_events:60000,block_events:36000})),
    pages:{schema_version:1,window_ms:86400000,queries:['hits_first_page','policies_overview'].map((name,i)=>({name,samples:Array.from({length:1440},(_,tick)=>({elapsed_ms:tick*60000+10,duration_us:1000,items:i?2:50,total:i?2:72000}))}))},
    latency_histograms:names.map(name=>({name,count:100,buckets:[...[.05,.1,.25,.5,1,2,5,10,20,50,100,200,500,1000,2000,5000,10000].map(upper_ms=>({upper_ms,inf:false,count:95})),{upper_ms:0,inf:true,count:100}]}))}
}
const bootstrap=()=>({schema_version:1,candidate,core_version:'26.6.27',database:'sqlite',requested_owners:10,bootstrap_stage:9,started_native_processes:10,unique_live_parents:10,unique_live_cores:10,independent_fresh_states:10,public_cache_copies:9,complete_stream_pairs:10,nonce_controls:10,shared_users:1,pressure_complete:0})
test('real-day checker keeps exact24h ten-node200-user300-site scope and actual SQL units',()=>{
  const result=checkTrialDayMetrics(good(),bootstrap());assert.equal(result.trial_requests,1440000);assert.equal(result.block_requests,864000);assert.deepEqual(result.page_p95_ms,{hits_first_page:1,policies_overview:1})
  const baseline=good();baseline.loss_before[0]=7;baseline.loss_after[0]=7;baseline.node_access_baseline[0].stale_events=3;baseline.node_access_baseline[0].private_lines=3
  assert.doesNotThrow(()=>checkTrialDayMetrics(baseline,bootstrap()))
  baseline.loss_after[0]++;assert.throws(()=>checkTrialDayMetrics(baseline,bootstrap()))
})
test('real-day checker rejects shortened clocks missing owners lost coverage loss and private metadata',()=>{
  for(const mutate of [m=>m.window_ms=300000,m=>m.complete=0,m=>m.native_owners=65,m=>m.trial_delivered--,m=>m.user_hour_deliveries[23][199]--,m=>m.user_hour_unique_sites[0][0]--,m=>m.owner_hour_deliveries[0][9]--,m=>m.node_hours[1].peak_retained_dropped[2]++,m=>m.node_hours[0].samples=0,m=>m.sql_nodes[9].trial_events--,m=>m.sql_nodes[0].distinct_sites--,m=>m.loss_rows++,m=>m.pages.queries[0].samples[1439].elapsed_ms=86400000,m=>m.utc_sql_hours.pop(),m=>m.latency_histograms[3].count=0,m=>m.credential='private']){const m=good();mutate(m);assert.throws(()=>checkTrialDayMetrics(m,bootstrap()))}
  const b=bootstrap();b.unique_live_cores=9;assert.throws(()=>checkTrialDayMetrics(good(),b))
})
test('real-day execution evidence requires real elapsed root and package PASS while discarding private output',()=>{
  const Package='github.com/KazuhaHub/passwall-sub-panel/internal/app',Test='TestNativeDestinationTrialRealDay'
  const events=[{Action:'start',Package},{Action:'run',Package,Test},{Action:'output',Package,Test,Output:'owned private diagnostic'},{Action:'pass',Package,Test,Elapsed:86401},{Action:'pass',Package,Elapsed:86402}]
  const encode=x=>x.map(e=>JSON.stringify(e)).join('\n')
  const r=checkTrialDayEvidence(encode(events),good(),bootstrap());assert(!JSON.stringify(r.sanitized).includes('private'));assert.equal(r.sanitized.length,4)
  for(const mutate of [e=>e[3].Elapsed=300,e=>e[3].Action='skip',e=>e[3].Action='fail',e=>e[1].Test='TestNativeDestinationTrialLoad',e=>e.splice(3,1),e=>e.push(e[4])]){const e=structuredClone(events);mutate(e);assert.throws(()=>checkTrialDayEvidence(encode(e),good(),bootstrap()))}
})
test('real-day runner keeps clean source pins isolation and26h lifetime without impersonating hosted jobs',()=>{
  const script=readFileSync(new URL('./run-destination-trial-day.sh',import.meta.url),'utf8')
  assert.equal(spawnSync('bash',['-n'],{input:script,encoding:'utf8'}).status,0)
  for(const part of [candidate,'PSP_NATIVE_TRIAL_DAY=24h','test ! -e "$run_dir"','mkdir -m 700','./cmd/node','-test.timeout=26h',"'^TestNativeDestinationTrialRealDay$'",'statuses=("${PIPESTATUS[@]}")','destination-trial-day.mjs'])assert(script.includes(part))
  assert(!script.includes('GITHUB_ACTIONS=')&&!script.includes('RUNNER_ENVIRONMENT=')&&!script.includes('rm -'))
  const cohort=readFileSync(new URL('../internal/app/destination_native_global_cohort_test.go',import.meta.url),'utf8')
  assert(cohort.includes('return nativeBootstrapOwnedCohort(t, options, 65, "destination-global-bootstrap-metrics.json", nil)'))
  const driver=readFileSync(new URL('../internal/app/destination_native_trial_day_test.go',import.meta.url),'utf8')
  for(const part of ['nativeBuildTrialDayFixture','nativePrepareTrialLoad','nativeDayPublish','nativeDayInterval','second < 86400','minute < 1440','nativeTrialLatencySnapshot','FROM dest_hits','FROM dbstat','m.Completed = 1'])assert(driver.includes(part))
})
