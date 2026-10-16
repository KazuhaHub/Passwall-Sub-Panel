import assert from 'node:assert/strict'
import {readFileSync,writeFileSync} from 'node:fs'
import {fileURLToPath} from 'node:url'

const candidate='61f30239e09d62b05bd59df7d9d28a5071790556'
const hour=3600000, root='TestNativeDestinationRetentionReal72Hours'
const pkg='github.com/KazuhaHub/passwall-sub-panel/internal/app'
const phases=['captured','future_boundaries','before_cleanup26','after_cleanup26','after_restart26','after_node_replay26','after_node_expiry48','after_stale_rejection48','after_hit_cleanup48','before_marker_expiry72','after_marker_expiry72','after_budget_hour_expiry']
const exact=(a,b)=>assert.deepEqual(a,b)
const integer=(n,min=0,max=Number.MAX_SAFE_INTEGER)=>assert(Number.isSafeInteger(n)&&n>=min&&n<=max)
const shape=(o,keys)=>{assert(o&&typeof o==='object'&&!Array.isArray(o));exact(Object.keys(o).sort(),keys.split(' ').sort())}
const pair=(a)=>{assert(Array.isArray(a));exact(a.length,2)}
function sql(s){shape(s,'hour_ms rows events markers received_unix_ms budget_rows budget_keys total_budget_rows');for(const n of Object.values(s))integer(n);exact(s.hour_ms%hour,0);integer(s.markers,0,1);integer(s.budget_keys,0,1);assert(s.budget_rows<=s.total_budget_rows);if(s.markers)integer(s.received_unix_ms,1);else exact(s.received_unix_ms,0)}

export function checkRetentionMetrics(m,b){
  shape(m,'schema_version candidate core_version database native_owners started_unix_ms ended_unix_ms window_ms control_successes fresh_node_samples original_hour_ms original_events observe_pending_before_expiry observe_pending_after_expiry authenticated_boundary_fixtures out_of_range_rows phases transport complete')
  shape(b,'schema_version candidate core_version database requested_owners bootstrap_stage started_native_processes unique_live_parents unique_live_cores independent_fresh_states public_cache_copies complete_stream_pairs nonce_controls shared_users pressure_complete')
  for(const value of [m,b]){exact(value.candidate,candidate);exact(value.core_version,'26.6.27');exact(value.database,'sqlite');exact(value.schema_version,1)}
  exact(m.complete,1);exact(m.native_owners,1)
  for(const [key,value]of Object.entries({requested_owners:1,bootstrap_stage:9,started_native_processes:1,unique_live_parents:1,unique_live_cores:1,independent_fresh_states:1,public_cache_copies:0,complete_stream_pairs:1,nonce_controls:1,shared_users:1,pressure_complete:0}))exact(b[key],value)
  integer(m.started_unix_ms,1);integer(m.ended_unix_ms,m.started_unix_ms+72*hour,m.started_unix_ms+76*hour)
  integer(m.window_ms,72*hour,76*hour);assert(Math.abs(m.ended_unix_ms-m.started_unix_ms-m.window_ms)<=30000)
  integer(m.control_successes,4320,5000);exact(m.fresh_node_samples,m.control_successes)
  pair(m.original_hour_ms);pair(m.original_events);exact(m.original_events,[1,1])
  for(const h of m.original_hour_ms){integer(h,Math.floor(m.started_unix_ms/hour)*hour,m.started_unix_ms+hour);exact(h%hour,0)}
  exact(m.observe_pending_before_expiry,1);exact(m.observe_pending_after_expiry,0);exact(m.out_of_range_rows,2)
  assert(Array.isArray(m.phases));exact(m.phases.map(p=>p.name),phases)
  const initial=m.phases[0].original_sql
  pair(initial)
  let last=m.started_unix_ms
  for(let index=0;index<phases.length;index++){
    const p=m.phases[index];shape(p,'name at_unix_ms original_sql');integer(p.at_unix_ms,last,m.ended_unix_ms);last=p.at_unix_ms
    pair(p.original_sql)
    for(let lane=0;lane<2;lane++){
      const s=p.original_sql[lane];sql(s);exact(s.hour_ms,m.original_hour_ms[lane])
      exact(s.rows,index<8?1:0);exact(s.events,index<8?1:0);exact(s.markers,index<10?1:0)
      if(index<10)exact(s.received_unix_ms,initial[lane].received_unix_ms)
      exact(s.budget_keys,index<11?1:0)
      const budget=index===0?1:index===11?0:lane===0?2:1
      exact(s.budget_rows,budget);exact(s.total_budget_rows,budget)
    }
  }
  for(const s of initial){integer(s.received_unix_ms,m.started_unix_ms,m.phases[0].at_unix_ms);integer(s.hour_ms,s.received_unix_ms-48*hour,s.received_unix_ms+hour)}
  const received=initial.map(s=>s.received_unix_ms), first=Math.min(...received), final=Math.max(...received)
  const byName=Object.fromEntries(m.phases.map(p=>[p.name,p]))
  integer(byName.after_node_expiry48.at_unix_ms,m.original_hour_ms[1]+48*hour)
  integer(byName.before_marker_expiry72.at_unix_ms,first+72*hour-120000,first+72*hour-1)
  integer(byName.after_marker_expiry72.at_unix_ms,final+72*hour)
  integer(byName.after_budget_hour_expiry.at_unix_ms,Math.floor(final/hour)*hour+73*hour)
  assert(Array.isArray(m.authenticated_boundary_fixtures));exact(m.authenticated_boundary_fixtures.length,3)
  for(let i=0;i<3;i++){
    const s=m.authenticated_boundary_fixtures[i];sql(s);exact(s.rows,i===0?1:0);exact(s.events,i===0?1:0);exact(s.markers,1);exact(s.total_budget_rows,2)
    integer(s.received_unix_ms,m.started_unix_ms,m.ended_unix_ms)
    if(i===0){integer(s.hour_ms,s.received_unix_ms+1,s.received_unix_ms+hour);assert(s.received_unix_ms<=byName.future_boundaries.at_unix_ms)}
    if(i===1){integer(s.hour_ms,s.received_unix_ms+hour+1);assert(s.received_unix_ms<=byName.future_boundaries.at_unix_ms)}
    if(i===2){exact(s.hour_ms,m.original_hour_ms[0]);integer(s.received_unix_ms,s.hour_ms+48*hour+1);assert(s.received_unix_ms<=byName.after_stale_rejection48.at_unix_ms)}
  }
  const f=m.transport
  shape(f,'phase captured_rows canonical_bytes suppressed_success_responses canonical_retries offline_requests original_block_success_responses original_http_errors handler_replacements isolated_unix_ms replaced_unix_ms resumed_unix_ms')
  exact(f.phase,3);exact(f.captured_rows,[1,1]);exact(f.original_http_errors,0);exact(f.handler_replacements,1)
  for(const a of [f.canonical_bytes,f.suppressed_success_responses,f.canonical_retries]){pair(a);for(const n of a)integer(n,1)}
  for(const n of f.canonical_bytes)integer(n,1,8*1024*1024)
  integer(f.offline_requests,2);integer(f.original_block_success_responses,1)
  integer(f.isolated_unix_ms,byName.future_boundaries.at_unix_ms,byName.before_cleanup26.at_unix_ms-26*hour)
  integer(f.replaced_unix_ms,f.isolated_unix_ms+26*hour,byName.after_node_replay26.at_unix_ms)
  integer(f.resumed_unix_ms,f.replaced_unix_ms,byName.after_node_replay26.at_unix_ms)
  integer(byName.after_restart26.at_unix_ms,byName.after_cleanup26.at_unix_ms,f.replaced_unix_ms)
  return {owners:1,minimum_real_hours:72,actual_window_ms:m.window_ms,original_replay_after_hours:26,original_lanes:2,markers_expired:true,original_budget_hours_pruned:true,boundary_fixtures:3,control_successes:m.control_successes}
}

export function checkRetentionEvidence(raw,metrics,bootstrap){
  let starts=0,runs=0,passes=0,packages=0,ended=false
  const sanitized=[]
  for(const line of raw.trim().split(/\r?\n/)){
    const e=JSON.parse(line);assert(!ended&&e.Package===pkg&&(e.Test===undefined||e.Test===root))
    assert(!['fail','skip','bench'].includes(e.Action))
    if(e.Action==='output'){assert(!/\(cached\)|WARNING: DATA RACE|panic: test timed out/.test(e.Output??''));continue}
    assert(['start','run','pass'].includes(e.Action))
    if(e.Action==='start'){exact(e.Test,undefined);exact(runs,0);starts++}
    if(e.Action==='run'){exact(e.Test,root);exact(starts,1);runs++}
    if(e.Action==='pass'){assert(Number.isFinite(e.Elapsed)&&e.Elapsed>=72*3600);exact(runs,1);if(e.Test){exact(packages,0);passes++}else{exact(passes,1);packages++;ended=true}}
    const value={Action:e.Action,Package:pkg};if(e.Test)value.Test=e.Test;if(e.Elapsed!==undefined)value.Elapsed=e.Elapsed;sanitized.push(value)
  }
  exact(starts,1);exact(runs,1);exact(passes,1);exact(packages,1)
  return {summary:checkRetentionMetrics(metrics,bootstrap),sanitized}
}

if(process.argv[1]===fileURLToPath(import.meta.url)){
  try{
    assert(process.argv.length===6)
    const [raw,metrics,bootstrap,out]=process.argv.slice(2)
    const result=checkRetentionEvidence(readFileSync(raw,'utf8'),JSON.parse(readFileSync(metrics,'utf8')),JSON.parse(readFileSync(bootstrap,'utf8')))
    writeFileSync(out,result.sanitized.map(x=>JSON.stringify(x)).join('\n')+'\n',{mode:0o600})
    process.stdout.write(JSON.stringify(result.summary)+'\n')
  }catch{process.stderr.write('Real retention original evidence rejected; private inputs preserved.\n');process.exitCode=1}
}
