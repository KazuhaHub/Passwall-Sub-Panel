import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { checkGlobalBootstrapEvidence } from './destination-global-bootstrap.mjs'
import { moduleNodeCandidate, sanitizeDestinationResults } from './destination-acceptance.mjs'

const root = 'TestNativeDestinationGlobalCohortBootstrap'
const events = () => [{ Action: 'start' }, { Action: 'run', Test: root }, { Action: 'pass', Test: root }, { Action: 'pass' }].map(value => JSON.stringify({ Package: 'github.com/KazuhaHub/passwall-sub-panel/internal/app', ...value })).join('\n')
const report = () => ({ schema_version: 1, candidate: moduleNodeCandidate.commit, core_version: '26.6.27', database: 'sqlite', requested_owners: 65, bootstrap_stage: 9, started_native_processes: 65, unique_live_parents: 65, unique_live_cores: 65, independent_fresh_states: 65, public_cache_copies: 64, complete_stream_pairs: 65, nonce_controls: 65, shared_users: 1, pressure_complete: 0 })

test('global bootstrap requires all real cohort prerequisites and remains distinct from pressure', () => {
  const result = checkGlobalBootstrapEvidence(sanitizeDestinationResults(events()), JSON.stringify(report()))
  assert.equal(result.actual_cores, 65)
  assert.equal(result.pressure_complete, false)
  for (const mutate of [r => { r.bootstrap_stage-- }, r => { r.started_native_processes-- }, r => { r.unique_live_parents-- }, r => { r.unique_live_cores-- }, r => { r.independent_fresh_states-- }, r => { r.public_cache_copies-- }, r => { r.complete_stream_pairs-- }, r => { r.nonce_controls-- }, r => { r.shared_users = 200 }, r => { r.pressure_complete = 1 }, r => { r.private_path = '/private/state.db' }, r => { r.candidate = 'foreign' }, r => { r.database = 'mysql' }]) {
    const value = report(); mutate(value)
    assert.throws(() => checkGlobalBootstrapEvidence(events(), JSON.stringify(value)), /private content withheld/)
  }
  for (const input of [events().replace('"run"', '"skip"'), events().replaceAll(root, 'TestNativeDestinationIdleCost'), events() + '\n' + JSON.stringify({ Action: 'output', Output: '(cached)' })]) assert.throws(() => checkGlobalBootstrapEvidence(input, JSON.stringify(report())))
})

test('global cohort bootstrap uses independent actual processes and binds after membership deployment', () => {
  const source = readFileSync(new URL('../internal/app/destination_native_global_cohort_test.go', import.meta.url), 'utf8')
  for (const value of ['a.Run()', 'NativeAgentProvisioning.Create', 'ResyncMembershipOrEnqueue', 'nativeCopyPublicCoreCache', 'nativeBindLiveCore', 'os.SameFile', 'nativeVLESSHost', 'cfg.LastSeen.After(memberAfter)', 'roster.LastSeen.After(memberAfter)']) assert(source.includes(value))
  const traffic = source.indexOf('"actual global member traffic and fresh paired receipts"')
  const finalBinding = source.indexOf('peer.live = nativeBindLiveCore', traffic)
  assert(traffic > 0 && finalBinding > traffic)
  assert.doesNotMatch(source, /SetSnapshot\(|\.collector\.Ack\(|runtime\.GC\(|sysctl/)
  const workflow = readFileSync(new URL('../.github/workflows/destination-load.yml', import.meta.url), 'utf8')
  const job = workflow.slice(workflow.indexOf('  global-bootstrap:'))
  assert(job.startsWith('  global-bootstrap:'))
  for (const db of ['sqlite', 'mysql', 'postgres']) assert(job.includes(`database: ${db}`))
  assert(job.includes(`-test.run '^${root}$'`) && job.includes('destination-global-bootstrap.mjs'))
  assert.match(job, /ref: 184f4e23f0f3d72f8492432184685fea4ec4967c/)
  assert.match(job, /PSP_NATIVE_CORE_VERSION=26\.6\.27/)
  assert.match(job, /name: \$\{\{ matrix\.evidence_prefix \}\}-\$\{\{ github\.sha \}\}/)
})

test('global pressure reuses the checked real cohort and opts into actual Node metrics', () => {
  const source = readFileSync(new URL('../internal/app/destination_native_global_cohort_test.go', import.meta.url), 'utf8')
  assert(source.includes('nativeBootstrapGlobalCohort(t, nativeGlobalCohortOptions{})'))
  assert(source.includes('buildNativeDestinationPolicyFixtureWithAuditStore(t, options.decorate)'))
  assert(source.includes('if options.auditMetrics {') && source.includes('newNativeMixedProbe(peer.agent.AgentID)'))
  assert(source.includes('peer.process.Args = append(peer.process.Args, "--audit-metrics")'))
  assert(source.includes('nativeMixedOutput{private: peer.output, probe: peer.probe}'))
  assert(source.indexOf('handler = options.transport(handler, peers)') < source.indexOf('httptest.NewTLSServer(handler)'))
  assert(source.indexOf('return &nativeGlobalCohort{fixture: f, peers: peers, ready: ready, server: server}') > source.indexOf('m.Stage = 9'))
  assert.doesNotMatch(source, /\.server\.Config\.Handler\s*=|SetSnapshot\(|\.collector\.Ack\(/)
})

test('global cache donor proves the current real executable without imposing idle PID stability during provisioning', () => {
  const source = readFileSync(new URL('../internal/app/destination_native_global_cohort_test.go', import.meta.url), 'utf8')
  const start = source.indexOf('dataDir := filepath.Join(peer.dir')
  const copyStart = source.indexOf('if i != 0 {', start)
  const copyEnd = source.indexOf('if _, err := os.Lstat(filepath.Join(dataDir, "state.db"))', copyStart)
  const copy = source.slice(copyStart, copyEnd)
  assert(copy.includes('ready(peers[0]) && nativeGlobalCurrentPublicCore'))
  assert(copy.includes('nativeCopyPublicCoreCache'))
  assert.doesNotMatch(copy, /peers\[0\]\.live\(\)/)
  const donor = source.slice(source.indexOf('func nativeGlobalCurrentPublicCore'))
  for (const required of ['"versions", "26.6.27", "xray"', 'args[0] != expected', 'nativeCoreHasParent(pid, parent)', 'nativeOwnedLiveCoreCommand', 'os.SameFile(file, live)', 'return count == 1']) assert(donor.includes(required))
  const final = source.indexOf('peer.live = nativeBindLiveCore', source.indexOf('"actual global member traffic and fresh paired receipts"'))
  assert(final > 0 && source.slice(final).includes('peer.live()'))
})
