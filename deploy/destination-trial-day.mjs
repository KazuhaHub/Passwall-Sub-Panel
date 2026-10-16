import assert from 'node:assert/strict'
import {createHash} from 'node:crypto'
import {readFileSync, writeFileSync} from 'node:fs'
import {fileURLToPath} from 'node:url'

const root = 'TestNativeDestinationTrialRealDay'
const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
const candidate = '61f30239e09d62b05bd59df7d9d28a5071790556'
const day = 86400000
const integer = (x, lo = 0, hi = Number.MAX_SAFE_INTEGER) => assert(Number.isSafeInteger(x) && x >= lo && x <= hi)
const exact = (x, n) => assert.equal(x, n)
const matrix = (x, rows, columns, expected) => {
  exact(x.length, rows)
  for (const row of x) { exact(row.length, columns); for (const n of row) exact(n, expected) }
}
const latencyNames = ['fixture_destination_sync_handler_ms','psp_node_control_sync_ms','psp_poll_ms','psp_poll_stage_ms{stage=sink_flush}','psp_dest_audit_begin_ms{kind=block}','psp_dest_audit_begin_ms{kind=trial}','psp_dest_audit_chunk_ms{kind=block}','psp_dest_audit_chunk_ms{kind=trial}']
const latencyBounds=[.05,.1,.25,.5,1,2,5,10,20,50,100,200,500,1000,2000,5000,10000]
const closedKeys=new Set('schema_version candidate core_version database native_owners users sites domain_set_sha256 started_unix_ms ended_unix_ms window_ms trial_attempted trial_delivered trial_target_requests block_attempted block_delivered block_denied transport_failures late_or_wrong_hour control_successes user_hour_deliveries user_hour_unique_sites owner_hour_deliveries node_hours pages latency_histograms utc_sql_hours sql_nodes trial_stored_rows trial_stored_events block_stored_events trial_identified_rows loss_rows loss_events loss_unmatched sqlite_allocated_bytes dest_hits_allocated_bytes complete sqlite_file_bytes node_access_baseline loss_before loss_after latency_window_ms sql_drain_ms available private_lines usage_events denied_events unmatched_events dropped_events stale_events parse_queued parse_limit pending_dropped pending_unmatched owner hour samples peak_keys peak_retained_dropped peak_retained_unmatched peak_access_dropped peak_access_unmatched peak_access_stale peak_pending_dropped peak_pending_unmatched peak_heap_bytes trial_rows trial_events block_events distinct_sites queries name elapsed_ms duration_us items total count buckets upper_ms inf hour_ms'.split(' '))
function closedNumeric(value, strings, key=''){
  if(Array.isArray(value)){assert(value.length<=4800);for(const v of value)closedNumeric(v,strings,key)}
  else if(value&&typeof value==='object'){for(const[k,v]of Object.entries(value)){assert(closedKeys.has(k));closedNumeric(v,strings,k)}}
  else if(typeof value==='string')assert(strings.has(value))
  else if(typeof value==='boolean')assert(true)
  else if(key==='upper_ms')assert(Number.isFinite(value)&&value>=0)
  else integer(value)
}
export function checkTrialDayMetrics(m, bootstrap) {
  exact(m.schema_version,1); exact(m.complete,1); exact(m.candidate,candidate);exact(m.core_version,'26.6.27');exact(m.database,'sqlite')
  exact(m.native_owners,10);exact(m.users,200);exact(m.sites,300)
  const domains = Array.from({length:300},(_,i)=>`trial-day-${String(i).padStart(3,'0')}.test`).join('\n')
  exact(m.domain_set_sha256,createHash('sha256').update(domains).digest('hex'))
  closedNumeric(m,new Set([candidate,'26.6.27','sqlite',m.domain_set_sha256,'hits_first_page','policies_overview',...latencyNames]))
  integer(m.started_unix_ms,1);integer(m.ended_unix_ms,m.started_unix_ms+day,m.started_unix_ms+day+5000);integer(m.window_ms,day,day+1000)
  for(const key of ['trial_attempted','trial_delivered','trial_target_requests','trial_stored_events'])exact(m[key],1440000)
  for(const key of ['block_attempted','block_delivered','block_denied','block_stored_events'])exact(m[key],864000)
  for(const key of ['transport_failures','late_or_wrong_hour','trial_identified_rows','loss_rows','loss_events','loss_unmatched'])exact(m[key],0)
  integer(m.latency_window_ms,m.window_ms,m.window_ms+10000);integer(m.sql_drain_ms,0,181000)
  exact(m.loss_before.length,3);exact(m.loss_after.length,3)
  for(let i=0;i<3;i++){integer(m.loss_before[i]);integer(m.loss_after[i],m.loss_before[i]);exact(m.loss_after[i]-m.loss_before[i],[m.loss_rows,m.loss_events,m.loss_unmatched][i])}
  exact(m.node_access_baseline.length,10)
  for(const b of m.node_access_baseline){exact(b.available,1);exact(b.parse_limit,4096);exact(b.parse_queued,0);exact(b.pending_dropped,0);exact(b.pending_unmatched,0);exact(b.private_lines,b.usage_events+b.denied_events+b.unmatched_events+b.dropped_events+b.stale_events)}
  exact(m.control_successes,14400);integer(m.trial_stored_rows,1,75000);integer(m.dest_hits_allocated_bytes,1);integer(m.sqlite_allocated_bytes,m.dest_hits_allocated_bytes)
  exact(m.sqlite_file_bytes.length,3);integer(m.sqlite_file_bytes[0],1);integer(m.sqlite_file_bytes[1]);integer(m.sqlite_file_bytes[2])
  matrix(m.user_hour_deliveries,24,200,300);matrix(m.user_hour_unique_sites,24,200,300);matrix(m.owner_hour_deliveries,24,10,6000)
  exact(m.node_hours.length,240)
  const nodeKeys=['hour','owner','samples','peak_keys','peak_retained_dropped','peak_retained_unmatched','peak_access_dropped','peak_access_unmatched','peak_access_stale','peak_pending_dropped','peak_pending_unmatched','peak_heap_bytes'].sort()
  for(let i=0;i<240;i++){
    const h=m.node_hours[i];assert.deepEqual(Object.keys(h).sort(),nodeKeys);exact(h.hour,Math.floor(i/10));exact(h.owner,i%10);integer(h.samples,1,100000);integer(h.peak_heap_bytes,1)
    exact(h.peak_keys.length,4);for(const n of h.peak_keys)integer(n,0,40000)
    for(const key of ['peak_retained_dropped','peak_retained_unmatched']){exact(h[key].length,4);for(const n of h[key])exact(n,0)}
    for(const key of ['peak_access_dropped','peak_access_unmatched','peak_access_stale','peak_pending_dropped','peak_pending_unmatched'])exact(h[key],0)
  }
  exact(m.sql_nodes.length,10)
  let rows=0
  for(let i=0;i<10;i++){const n=m.sql_nodes[i];exact(n.owner,i);integer(n.trial_rows,1,7500);exact(n.trial_events,144000);exact(n.block_events,86400);exact(n.distinct_sites,300);rows+=n.trial_rows}
  exact(rows,m.trial_stored_rows)
  integer(m.utc_sql_hours.length,24,25)
  let trial=0,block=0,utcRows=0,prior=0
  const first=Math.floor(m.started_unix_ms/3600000)*3600000,last=Math.floor(m.ended_unix_ms/3600000)*3600000
  for(const h of m.utc_sql_hours){integer(h.hour_ms,first,last);exact(h.hour_ms%3600000,0);if(prior)exact(h.hour_ms-prior,3600000);prior=h.hour_ms;integer(h.trial_rows,1,3000);integer(h.trial_events,1);integer(h.block_events,1);trial+=h.trial_events;block+=h.block_events;utcRows+=h.trial_rows}
  exact(trial,1440000);exact(block,864000);exact(utcRows,m.trial_stored_rows)
  exact(m.pages.schema_version,1);exact(m.pages.window_ms,m.window_ms);exact(m.pages.queries.length,2)
  const pages={}
  for(let i=0;i<2;i++){
    const q=m.pages.queries[i];exact(q.name,['hits_first_page','policies_overview'][i]);exact(q.samples.length,1440)
    const values=[]
    for(let tick=0;tick<1440;tick++) {const s=q.samples[tick];integer(s.elapsed_ms,tick*60000,(tick+1)*60000-1);integer(s.duration_us,1,15000000);integer(s.items,0,i?2:50);integer(s.total,s.items);if(i){exact(s.items,2);exact(s.total,2)}values.push(s.duration_us)}
    values.sort((a,b)=>a-b);pages[q.name]=values[Math.ceil(values.length*.95)-1]/1000
  }
  assert.deepEqual(m.latency_histograms.map(h=>h.name),latencyNames)
  for(const h of m.latency_histograms){integer(h.count,1);exact(h.buckets.length,latencyBounds.length+1);let previous=0;for(let i=0;i<h.buckets.length;i++){const b=h.buckets[i];integer(b.count,previous,h.count);previous=b.count;if(i===latencyBounds.length){exact(b.inf,true);exact(b.upper_ms,0);exact(b.count,h.count)}else{exact(b.inf,false);exact(b.upper_ms,latencyBounds[i])}}}
  for(const [key,n]of Object.entries({schema_version:1,requested_owners:10,bootstrap_stage:9,started_native_processes:10,unique_live_parents:10,unique_live_cores:10,independent_fresh_states:10,public_cache_copies:9,complete_stream_pairs:10,nonce_controls:10,shared_users:1,pressure_complete:0}))exact(bootstrap[key],n)
  exact(bootstrap.candidate,candidate);exact(bootstrap.core_version,'26.6.27');exact(bootstrap.database,'sqlite')
  return {hours:24,owners:10,users:200,sites:300,trial_requests:1440000,block_requests:864000,trial_rows:m.trial_stored_rows,sqlite_allocated_bytes:m.sqlite_allocated_bytes,sqlite_file_bytes:m.sqlite_file_bytes,dest_hits_allocated_bytes:m.dest_hits_allocated_bytes,page_p95_ms:pages}
}

export function checkTrialDayEvidence(raw, metrics, bootstrap) {
  let starts=0,runs=0,passes=0,packages=0,ended=false
  const sanitized=[]
  for(const line of raw.trim().split(/\r?\n/)){
    const e=JSON.parse(line);assert(!ended&&e.Package===pkg)
    assert(e.Test===undefined||e.Test===root)
    assert(!['fail','skip','bench'].includes(e.Action))
    if(e.Action==='output'){assert(!/\(cached\)|WARNING: DATA RACE|panic: test timed out/.test(e.Output??''));continue}
    assert(['start','run','pass'].includes(e.Action))
    if(e.Action==='start'){assert(e.Test===undefined);starts++}
    if(e.Action==='run'){exact(e.Test,root);runs++}
    if(e.Action==='pass'){assert(Number.isFinite(e.Elapsed)&&e.Elapsed>=86400);if(e.Test)passes++;else{packages++;ended=true}}
    const value={Action:e.Action,Package:pkg};if(e.Test)value.Test=e.Test;if(e.Elapsed!==undefined)value.Elapsed=e.Elapsed;sanitized.push(value)
  }
  exact(starts,1);exact(runs,1);exact(passes,1);exact(packages,1)
  return {summary:checkTrialDayMetrics(metrics,bootstrap),sanitized}
}

if(process.argv[1]===fileURLToPath(import.meta.url)){
  try{
    assert(process.argv.length===6)
    const [raw,metrics,bootstrap,out]=process.argv.slice(2)
    const result=checkTrialDayEvidence(readFileSync(raw,'utf8'),JSON.parse(readFileSync(metrics,'utf8')),JSON.parse(readFileSync(bootstrap,'utf8')))
    writeFileSync(out,result.sanitized.map(x=>JSON.stringify(x)).join('\n')+'\n',{mode:0o600})
    process.stdout.write(JSON.stringify(result.summary)+'\n')
  }catch{process.stderr.write('Real-day original evidence rejected; private inputs preserved.\n');process.exitCode=1}
}
