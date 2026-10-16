import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { moduleNodeCandidate, sanitizeDestinationResults } from './destination-acceptance.mjs'
import { checkByteFormationEvidence } from './destination-byte-formation.mjs'

const root = 'TestNativeDestinationByteFormation', Package = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
const events = () => [{Action:'start',Package},{Action:'run',Package,Test:root},{Action:'pass',Package,Test:root},{Action:'pass',Package}].map(e=>JSON.stringify(e)).join('\n')
function report() {
  const access = (usage,denied) => ({available:1,private_lines:usage+denied,usage_events:usage,denied_events:denied,unmatched_events:0,dropped_events:0,stale_events:0,parse_queued:0,parse_limit:4096,pending_dropped:0,pending_unmatched:0})
  return {schema_version:1,candidate:moduleNodeCandidate.commit,core_version:'26.6.27',database:'sqlite',stage:3,native_owners:1,trial_users:200,attempted:[2049,2049,2049,2049],delivered:[2049,2049,2049,2049],transport_failures:0,response_fault_ms:75000,durable_first_packets:[1,1,1,1],packets:{suppressed_success_responses:[2,2,2,2],verified_frozen_retries:[1,1,1,1],recovered_first_packets:[1,1,1,1],first_canonical_bytes:[400,400,400,400],first_rows:[1,1,1,3],new_packets:[1,1,1,1],max_new_canonical_bytes:[400000,400000,400000,900000],rows_in_largest_new_packet:[2048,2048,2048,6144],original_http_errors:0},held_queue:['block','observe','trial','usage'].map((kind,i)=>({kind,waiting:i===0?0:1,active:i===0?1:0,batches:1,canonical_bytes:i===3?900000:400000,batch_limit:[128,32,32,64][i],byte_limit:[32,8,8,16][i]*1024*1024,owner_limit:[4,1,1,1][i],owners:1,max_owner_batches:1})),held_sql_entries:1,delegated_begins_while_held:0,access_ingress:{before:access(2,0),after:access(6149,2049),allowed_expected:6147,allowed_received:6147,allowed_not_received:0,denied_expected:2049,denied_received:2049,denied_not_received:0},stored_events:[2049,2049,2049,6147],loss_rows:[0,0,0,0],formation_complete:1,byte_saturation_complete:0,pressure_complete:0}
}

test('real formation proof cannot be relabelled as byte saturation or accepted from incomplete evidence', () => {
  const result = checkByteFormationEvidence(sanitizeDestinationResults(events()), JSON.stringify(report()))
  assert.deepEqual(result, {formation_complete:true,native_owners:1,trial_users:200,byte_saturation_complete:false,pressure_complete:false})
  for (const mutate of [r=>{r.native_owners=65},r=>{r.trial_users=199},r=>{r.stage=2},r=>{r.response_fault_ms=74000},r=>{r.byte_saturation_complete=1},r=>{r.pressure_complete=1},r=>{r.durable_first_packets[2]=0},r=>{r.packets.max_new_canonical_bytes[2]=262144},r=>{r.packets.recovered_first_packets[0]=0},r=>{r.packets.verified_frozen_retries[1]=0},r=>{r.packets.original_http_errors=1},r=>{r.held_queue[1].owner_limit=2},r=>{r.held_queue[3].canonical_bytes=1000},r=>{r.delegated_begins_while_held=1},r=>{r.stored_events[1]--},r=>{r.access_ingress.allowed_not_received=1},r=>{r.private_batch_id='private'},r=>{r.packets.payload='private'}]) {
    const r=report();mutate(r);assert.throws(()=>checkByteFormationEvidence(events(),JSON.stringify(r)))
  }
  for (const stream of [events().replace('"run"','"skip"'),events().replace('"run"','"pass"'),events()+'\n'+JSON.stringify({Action:'output',Package,Output:'(cached)'}),events()+'\n'+JSON.stringify({Action:'output',Package,Diagnostic:'global_batch_suite_timeout'})]) assert.throws(()=>checkByteFormationEvidence(stream,JSON.stringify(report())))
})

test('formation uses actual original Node reports and records only bounded numeric evidence', () => {
  const fault=readFileSync(new URL('../internal/app/destination_native_byte_fault_test.go',import.meta.url),'utf8')
  const driver=readFileSync(new URL('../internal/app/destination_native_byte_formation_test.go',import.meta.url),'utf8')
  for(const required of ['original.ServeHTTP(response, r)','sha256.Sum256(canonical)','response.Code != http.StatusOK','p.first[index] != packet','w.WriteHeader(http.StatusServiceUnavailable)','w.Write(response.Body.Bytes())']) assert(fault.includes(required))
  for(const required of ['const perLane, faultSeconds = 2048, 75','SELECT COUNT(*) FROM dest_audit_batches WHERE agent_id=? AND batch_id=? AND kind=?','storage.pause()','storage.resume(pause)','lane.Pending != 1 || lane.Keys < want','facts.MaxNewBytes[i] <= (256<<10)','nativeMixedIngressBetween','m.Stored[i]+m.LossRows[i] != expected','publicsuffix.EffectiveTLDPlusOne(host)','root != host']) assert(driver.includes(required))
  assert.doesNotMatch(fault+driver,/\.collector\.Ack\(|SetSnapshot\(|\.Offer\(|runtime\.GC\(/)
  const complete=readFileSync(new URL('../.github/workflows/destination-load.yml',import.meta.url),'utf8')
  const workflow=complete.slice(complete.indexOf('\n  byte-formation:'))
  assert(workflow.startsWith('\n  byte-formation:') && workflow.includes("if: github.event_name == 'workflow_dispatch' && inputs.suite == 'byte-formation'"))
  assert(complete.includes('          - byte-formation'))
  assert(workflow.includes('ref: '+moduleNodeCandidate.commit) && workflow.includes('PSP_NATIVE_CORE_VERSION=26.6.27'))
  assert(workflow.includes(`-test.run '^${root}$' -test.timeout=15m`))
  assert(workflow.includes('destination-byte-formation.mjs') && workflow.includes('destination-byte-formation-metrics.json'))
  assert(!workflow.includes('continue-on-error'))
  const ci=readFileSync(new URL('../.github/workflows/test.yml',import.meta.url),'utf8')
  assert(ci.includes('ByteResponseFaultPreservesOriginalHandlerAndCanonicalRetries'))
})
