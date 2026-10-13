import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'
import { checkIdleCostEvidence } from './destination-idle-cost.mjs'
import { moduleNodeCandidate, sanitizeDestinationResults } from './destination-acceptance.mjs'

// Synthetic closed verifier fixtures only; not native CPU or disk evidence.
const root = 'TestNativeDestinationIdleCost'
const evidence = () => [{ Action: 'start' }, { Action: 'run', Test: root }, { Action: 'pass', Test: root, Elapsed: 45 }, { Action: 'pass', Elapsed: 45 }].map(e => JSON.stringify({ Package: 'github.com/KazuhaHub/passwall-sub-panel/internal/app', ...e })).join('\n')
const report = () => ({ schema_version: 2, candidate: moduleNodeCandidate.commit, core_version: '26.6.27', published_domains: 50000, applied_domains: 50000, core_domains: 50000, cpu_ticks_per_second: 100, control_successes: 2, tracer_bound: 1, receipt_checks: 8191, trace_stage: 6, trace_error: 0, cycles: Array.from({ length: 10 }, (_, i) => ({ phase: i < 5 ? 'plain' : 'traced', index: i % 5 + 1, elapsed_ms: 2200, agent_cpu_ticks: i < 5 ? 2 : 3, core_cpu_ticks: 0, accepted_syncs: 1, state_writes: i < 5 ? null : { db_bytes: 0, wal_bytes: 4096, journal_bytes: 0, write_calls: 1, sync_calls: 1 } })) })

test('idle gate binds actual fifty-thousand policy and original processes to separate plain/traced accepted cycles', () => {
  const value = checkIdleCostEvidence(sanitizeDestinationResults(evidence()), JSON.stringify(report()))
  assert.equal(value.plain_agent_cpu_ms_mean, 20)
  assert.equal(value.traced_state_bytes_mean, 4096)
  for (const mutate of [
    r => { r.schema_version = 1 }, r => { r.trace_stage = 5 }, r => { r.trace_error = 8 },
    r => { r.published_domains-- }, r => { r.applied_domains-- }, r => { r.core_domains = 0 },
    r => { r.candidate = 'foreign' }, r => { r.tracer_bound = 0 }, r => { r.receipt_checks = 8190 }, r => { r.control_successes = 1 },
    r => { r.cycles.pop() }, r => { r.cycles[0].state_writes = { db_bytes: 0 } },
    r => { r.cycles[5].state_writes = null }, r => { r.cycles[5].state_writes.path = '/private/state.db' },
    r => { r.cycles[6].state_writes.wal_bytes = -1 }, r => { r.cycles[0].phase = 'traced' },
    r => { r.cycles[0].accepted_syncs = 2 }, r => { r.cycles[7].index = 1 },
    r => { r.cycles[0].elapsed_ms = 0 }, r => { r.cycles[0].agent_cpu_ticks = null },
  ]) { const value = report(); mutate(value); assert.throws(() => checkIdleCostEvidence(evidence(), JSON.stringify(value)), /private content withheld/) }
  for (const input of [evidence().replace('"run"', '"skip"'), evidence().replace('"run"', '"pass"'), evidence().replaceAll(root, 'TestNativeDestinationMixedPressure'), evidence() + '\n' + JSON.stringify({ Action: 'output', Output: '(cached)' })]) assert.throws(() => checkIdleCostEvidence(input, JSON.stringify(report())))
})

test('idle runtime uses actual published receipts and tracee identity without changing state or timers', () => {
  const source = readFileSync(new URL('../internal/app/destination_native_idle_cost_test.go', import.meta.url), 'utf8')
  for (const required of ['nativeMutationReceiptMatches', 'current.AppliedBody', 'nativeBindLiveCore', 'nativeAssertIdleSyncCycles', 'os.SameFile', 'nativeLoadProcCounters', 'TracerPid:', 'trace=write,pwrite64,writev,pwritev,pwritev2,fsync,fdatasync,mmap,msync', 'measure("plain", 0)', 'measure("traced", tracer.Process.Pid)']) assert(source.includes(required))
  assert.doesNotMatch(source, /\.Process\.(?:Kill|Signal)\(.*parent|runtime\.GC\(|sysctl|SetSnapshot\(|\.collector\.Ack\(/)
  // The UI setting is "off"; the wire CollectLevel is empty. Reuse the
  // exact revision/digest receipt check instead of waiting for that UI literal.
  assert.match(source, /nativeCollectionReceiptMatches\(\*current, "", 0\)/)
  assert.doesNotMatch(source, /CollectEffective\s*!=\s*"off"/)
  const baseline = source.indexOf('prior, err := a.repos.DestAgentPolicy.Get')
  const edit = source.indexOf('request("POST", "dest/policies"')
  assert(baseline >= 0 && edit > baseline)
  const startup = source.indexOf('"actual initial idle candidate and complete streams"')
  const firstEdit = source.indexOf('request("POST", "dest/lists"')
  assert(startup >= 0 && firstEdit > startup && baseline > firstEdit)
  assert.match(source, /nativeReceiptMatches\(\*current, state\.PublishedGeneration, agent\.LastSeen, \*current\.MintedAt\)/)
  assert.match(source, /after := time\.UnixMilli\(time\.Now\(\)\.UnixMilli\(\)\)/)
  assert(source.includes('nativeIdleObserverStopError(signalErr, err)'))
  assert.match(source, /signalErr := tracer\.Process\.Signal\(os\.Interrupt\)/)
  assert.match(source, /panel\.AuditCollect != domain\.AuditCollectOff/)
  const workflow = readFileSync(new URL('../.github/workflows/destination-load.yml', import.meta.url), 'utf8')
  assert.match(workflow, /idle-cost:/)
  assert.match(workflow, /\^TestNativeDestinationIdleCost\$/)
  assert.match(workflow, /\^TestNativeIdleObserverStopRequiresRequestedInterrupt\$/)
  assert.match(workflow, /native-destination-idle-cost-\$\{\{ github\.sha \}\}/)
  assert.doesNotMatch(workflow, /path:.*(?:private\.trace|state-writes\.windows)/)
})
