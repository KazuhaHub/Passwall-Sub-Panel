import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { checkDestinationResults, sanitizeDestinationResults, nodeCandidate, destinationCases } from './destination-acceptance.mjs'

test('target route diagnostics disclose only exact guard comparisons and never prove acceptance', async () => {
  const { isSafeDestinationDiagnostic } = await import('./destination-acceptance.mjs')
  for (const name of ['one', 'local', 'loopback', 'exact_address', 'exact_host_prefix']) {
    for (const observed of [true, false]) {
      const output = `private-path:4: native diagnostic: fixture_route_${name}=${observed}\n`
      const sanitized = sanitizeDestinationResults(encode([{ Action: 'output', Output: output }]))
      const event = JSON.parse(sanitized)
      assert.deepEqual(event, { Action: 'output', Package: 'github.com/KazuhaHub/passwall-sub-panel/internal/app', Diagnostic: 'fixture_route_' + name, Observed: observed })
      assert(isSafeDestinationDiagnostic(event))
      assert(!sanitized.includes('private-path') && !sanitized.includes('Output'))
      assert.throws(() => checkDestinationResults(sanitized))
    }
  }
  for (const value of ['private_address=true', 'one=1', 'local=true private-secret', 'exact_host_prefix=2001:db8::1']) {
    assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native diagnostic: fixture_route_${value}\n` }])).trim(), '')
  }
  assert(!isSafeDestinationDiagnostic({ Diagnostic: 'fixture_route_private', Observed: true }))
})

test('pre-load fixture failures retain closed categories and the original failed root', async () => {
  const { isSafeDestinationDiagnostic } = await import('./destination-acceptance.mjs')
  const Package = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const Test = 'TestNativeDestinationGlobalBatchPressure'
  const failures = {
    'create destination fixture administrator failed; private diagnostics withheld': 'admin_create_failed',
    'load destination fixture issuer settings failed; private diagnostics withheld': 'admin_settings_failed',
    'issue destination fixture access token failed; private diagnostics withheld': 'admin_token_failed',
    'read native fixture automatic port boundary failed; private diagnostics withheld': 'port_boundary_failed',
    'native fixture has no explicit nonprivileged server range': 'port_range_invalid',
    'native fixture explicit server range is exhausted': 'port_range_exhausted',
    'native fixture explicit server range is occupied': 'port_range_occupied',
    'release owned native fixture port probe failed; private diagnostics withheld': 'port_probe_release_failed',
    'invalid owned target port': 'target_port_invalid',
    'create target nonce failed; private diagnostics withheld': 'target_nonce_failed',
    'read owned target addresses failed; private diagnostics withheld': 'target_addresses_failed',
    'decode owned addresses failed; private diagnostics withheld': 'target_addresses_invalid',
    'target address already exists': 'target_address_conflict',
    'add exact owned target /128 failed; private diagnostics withheld': 'target_address_add_failed',
    'remove exact owned target /128 failed; private diagnostics withheld': 'target_address_remove_failed',
    'verify local target route failed; private diagnostics withheld': 'target_route_read_failed',
    'decode local target route failed; private diagnostics withheld': 'target_route_decode_failed',
    'target does not have its exact local loopback route': 'target_route_invalid',
    'bind exact owned target failed; private diagnostics withheld': 'target_bind_failed',
  }
  for (const [message, category] of Object.entries(failures)) {
    const events = sanitizeDestinationResults(encode([
      { Action: 'output', Package, Test, Output: `private-path:4: ${message}; credential=private-secret host=private-host\n` },
      { Action: 'fail', Package, Test }, { Action: 'fail', Package },
    ])).trim().split('\n').map(line => JSON.parse(line))
    assert.equal(events[0].Diagnostic, 'fixture_' + category)
    assert(isSafeDestinationDiagnostic(events[0]))
    assert.deepEqual(events.slice(1), [{ Action: 'fail', Package, Test }, { Action: 'fail', Package }])
    for (const privateValue of ['private-path', 'private-secret', 'private-host', 'Output']) assert(!JSON.stringify(events).includes(privateValue))
  }
  for (const stage of ['administrator', 'allowed_target', 'observe_target', 'control_target']) {
    const event = JSON.parse(sanitizeDestinationResults(encode([{ Action: 'output', Package, Test, Output: `private-path:4: native setup stage: ${stage}\n` }])))
    assert.equal(event.Diagnostic, 'global_setup_stage_' + stage)
    assert(isSafeDestinationDiagnostic(event))
  }
  assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: 'create destination fixture private-secret failed\n' }])).trim(), '')
  assert(!isSafeDestinationDiagnostic({ Diagnostic: 'fixture_target_private-secret' }))
})

test('published remote rollback requires the real watcher, helper and complete execution evidence', async () => {
  const acceptance = await import('./destination-acceptance.mjs')
  assert.equal(typeof acceptance.checkRemoteUpgradeResults, 'function')
  assert(acceptance.remoteUpgradeCases.includes('health_failure_restore'))
  const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const names = ['TestNativePublishedRemoteRollback', ...['candidate_policy', 'health_failure_restore', 'published_rollback', 'restart_recovery', 'idempotent_replay'].map(name => `TestNativePublishedRemoteRollback/${name}`)]
  const values = [...names.flatMap(Test => [{ Action: 'run', Package: pkg, Test }, { Action: 'pass', Package: pkg, Test }]), { Action: 'pass', Package: pkg }]
  const evidence = sanitizeDestinationResults(encode(values))
  assert.doesNotThrow(() => acceptance.checkRemoteUpgradeResults(evidence))
  assert.throws(() => acceptance.checkLegacyUpgradeResults(evidence))
  for (const name of names) {
    assert.throws(() => acceptance.checkRemoteUpgradeResults(encode(values.filter(event => event.Test !== name))))
    assert.throws(() => acceptance.checkRemoteUpgradeResults(encode(values.map(event => event.Test === name && event.Action === 'pass' ? { ...event, Action: 'skip' } : event))))
  }
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  const job = workflow.slice(workflow.indexOf('  published-remote:'), workflow.indexOf('  legacy-upgrade:'))
  assert.equal(acceptance.remoteNodeCandidate.repository, nodeCandidate.repository)
  assert.match(acceptance.remoteNodeCandidate.commit, /^[a-f0-9]{40}$/)
  for (const value of [acceptance.remoteNodeCandidate.commit, 'PSP_NATIVE_REMOTE_SOURCE', 'PSP_NATIVE_REMOTE_NODE_BIN', 'check-remote-upgrade', 'TestNativePublishedRemoteRollback', 'TestNativeRemoteHealthFaultPreservesOtherRequests']) assert(job.includes(value), value)
})

test('setup profiling retains only bounded numeric observations and cannot prove completion', async () => {
  const { isSafeDestinationDiagnostic, checkDestinationGlobalBatchResults } = await import('./destination-acceptance.mjs')
  const limits = { elapsed_ms: 1200000, allocated_bytes: 1000000000000000, gc_cycles: 1000000, gc_pause_ns: 1200000000000, heap_bytes: 1000000000000000 }
  const facts = []
  for (const [name, limit] of Object.entries(limits)) {
    for (const value of [0, limit]) {
      const result = sanitizeDestinationResults(encode([{ Action: 'output', Output: `private-file: native global setup profile: ${name}=${value}\n` }]))
      const event = JSON.parse(result)
      assert.deepEqual(Object.keys(event).sort(), ['Action', 'Diagnostic', 'Observed', 'Package'])
      assert.equal(event.Diagnostic, `global_setup_profile_${name}`)
      assert.equal(event.Observed, value)
      assert(isSafeDestinationDiagnostic(event))
      assert(!result.includes('private-file'))
      facts.push(event)
    }
    for (const value of [limit + 1, -1, 1.5, 'private', '1; secret']) {
      assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native global setup profile: ${name}=${value}\n` }])).trim(), '')
    }
    for (const Observed of [limit + 1, -1, 1.5, '1', true]) assert(!isSafeDestinationDiagnostic({ Diagnostic: `global_setup_profile_${name}`, Observed }))
  }
  assert(!isSafeDestinationDiagnostic({ Diagnostic: 'global_setup_profile_agent', Observed: 1 }))
  assert.throws(() => checkDestinationGlobalBatchResults(encode(facts)))
})

test('global configuration failure facts have closed phases, causes and numeric bounds', async () => {
  const { sanitizeDestinationResults, isSafeDestinationDiagnostic } = await import('./destination-acceptance.mjs')
  for (const phase of ['catalog', 'build', 'compile', 'repository', 'capability', 'encode', 'mint']) {
    for (const cause of ['canceled', 'deadline', 'conflict', 'validation', 'unavailable', 'other']) {
      const result = sanitizeDestinationResults(encode([{ Action: 'output', Output: `private identity: native global config error: ${phase}_${cause}=17\n` }]))
      const event = JSON.parse(result)
      assert.deepEqual(Object.keys(event).sort(), ['Action', 'Diagnostic', 'Observed', 'Package'])
      assert.equal(event.Diagnostic, `global_config_error_${phase}_${cause}`)
      assert.equal(event.Observed, 17)
      assert(isSafeDestinationDiagnostic(event))
      assert(!result.includes('private identity'))
    }
  }
  for (const fact of ['agent_other=1', 'compile_private=1', 'compile_other=0', 'compile_other=1000001', 'compile_other=-1', 'compile_other=1.5', 'compile_other=1 private']) {
    assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native global config error: ${fact}\n` }])).trim(), '')
  }
  for (const Observed of [0, 1000001, -1, 1.5, '1', true]) assert(!isSafeDestinationDiagnostic({ Diagnostic: 'global_config_error_compile_other', Observed }))
  assert(!isSafeDestinationDiagnostic({ Diagnostic: 'global_config_error_agent_other', Observed: 1 }))
})

test('published Docker rollback requires real policy, automatic recovery and both production architectures', async () => {
  const acceptance = await import('./destination-acceptance.mjs')
  assert.equal(typeof acceptance.checkDockerUpgradeResults, 'function')
  const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const names = ['TestNativePublishedDockerRollback', ...['candidate_policy', 'health_failure_restore', 'published_rollback', 'restart_recovery', 'idempotent_replay'].map(name => `TestNativePublishedDockerRollback/${name}`)]
  const events = [...names.flatMap(Test => [{ Action: 'run', Package: pkg, Test }, { Action: 'pass', Package: pkg, Test }]), { Action: 'pass', Package: pkg }]
  assert.doesNotThrow(() => acceptance.checkDockerUpgradeResults(sanitizeDestinationResults(encode(events))))
  assert.throws(() => acceptance.checkRemoteUpgradeResults(encode(events)))
  for (const name of names) {
    assert.throws(() => acceptance.checkDockerUpgradeResults(encode(events.filter(event => event.Test !== name))))
    assert.throws(() => acceptance.checkDockerUpgradeResults(encode(events.map(event => event.Test === name && event.Action === 'pass' ? { ...event, Action: 'skip' } : event))))
  }
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  const job = workflow.slice(workflow.indexOf('  published-docker:'), workflow.indexOf('  legacy-upgrade:'))
  for (const required of [acceptance.remoteNodeCandidate.commit, 'ubuntu-24.04', 'ubuntu-24.04-arm', 'Dockerfile.release', 'PSP_NATIVE_DOCKER_IMAGE', 'TestNativePublishedDockerRollback', 'check-docker-upgrade', 'native-published-docker-${{ matrix.arch }}']) assert(job.includes(required), required)
})

test('global readiness failures disclose only closed guard categories', async () => {
  const { isSafeDestinationDiagnostic } = await import('./destination-acceptance.mjs')
  const Package = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const Test = 'TestNativeDestinationGlobalBatchPressure'
  for (const reason of ['state', 'store', 'capability', 'streams', 'streams_store', 'streams_metadata', 'streams_config', 'streams_roster', 'streams_adapter', 'streams_inbound', 'streams_inbound_missing', 'streams_inbound_offline', 'streams_inbound_stale', 'streams_inbound_read', 'streams_inbound_absent', 'streams_inbound_runtime', 'streams_inbound_shape', 'receipt', 'collection', 'shape', 'subjects', 'roster', 'complete']) {
    const result = sanitizeDestinationResults(encode([{ Action: 'output', Package, Test, Output: 'private-path:4: global batch readiness: ' + reason + '; credential=secret host=private-host' }]))
    const event = JSON.parse(result)
    assert.deepEqual(event, { Action: 'output', Package, Test, Diagnostic: 'global_batch_readiness_' + reason })
    assert(isSafeDestinationDiagnostic(event))
    for (const privateValue of ['private-path', 'secret', 'private-host', 'Output']) assert(!result.includes(privateValue))
  }
  assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Package, Test, Output: 'global batch readiness: private-unknown' }])).trim(), '')
})

test('global sync diagnostics retain only bounded actual transport facts', async () => {
  const { isSafeDestinationDiagnostic } = await import('./destination-acceptance.mjs')
  const values = { attempt_age_ms: 1200000, completion_age_ms: 10, last_duration_ms: 12345, last_status: 503, inflight: 1, parent_alive: 0 }
  for (const [name, value] of Object.entries(values)) {
    const result = sanitizeDestinationResults(encode([{ Action: 'output', Output: `private-path:4: native global sync: ${name}=${value}\n` }]))
    const event = JSON.parse(result)
    assert.equal(event.Diagnostic, `global_sync_${name}`)
    assert.equal(event.Observed, value)
    assert(isSafeDestinationDiagnostic(event))
    assert(!result.includes('private-path'))
  }
  for (const line of ['agent_id=42', 'attempt_age_ms=1200001', 'completion_age_ms=-1', 'last_status=0', 'last_status=600', 'inflight=66', 'parent_alive=2', 'last_duration_ms=1; credential=private']) {
    assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native global sync: ${line}\n` }])).trim(), '')
  }
  assert(!isSafeDestinationDiagnostic({ Diagnostic: 'global_sync_agent_id', Observed: 1 }))
})

test('setup process CPU diagnostics retain only bounded aggregate counters and never completion proof', async () => {
  const { isSafeDestinationDiagnostic, checkDestinationGlobalBatchResults } = await import('./destination-acceptance.mjs')
  const values = { sample: 1, elapsed_ms: 1200000, sample_ms: 29, parent_ticks: 30000, waited_child_ticks: 40000, live_core_ticks: 500,
    changed_core_identities: 65, parents: 65, cores: 65, ticks_per_second: 100, logical_cpus: 4, unavailable: 1 }
  const facts = []
  for (const [name, value] of Object.entries(values)) {
    const result = sanitizeDestinationResults(encode([{ Action: 'output', Output: `private PID/path: native global setup cpu: ${name}=${value}\n` }]))
    const event = JSON.parse(result)
    assert.equal(event.Diagnostic, `global_setup_cpu_${name}`)
    assert.equal(event.Observed, value)
    assert(isSafeDestinationDiagnostic(event))
    assert(!result.includes('private PID/path'))
    facts.push(event)
  }
  assert.throws(() => checkDestinationGlobalBatchResults(encode(facts)))
  for (const line of ['agent_id=1', 'pid=42', 'parents=66', 'cores=66', 'changed_core_identities=66', 'logical_cpus=0',
    'ticks_per_second=0', 'sample=0', 'sample=11', 'elapsed_ms=1200001', 'parent_ticks=-1', 'waited_child_ticks=1000000000001', 'unavailable=0', 'sample_ms=2; private']) {
    assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native global setup cpu: ${line}\n` }])).trim(), '')
  }
  assert(!isSafeDestinationDiagnostic({ Diagnostic: 'global_setup_cpu_pid', Observed: 42 }))
})

test('global drain diagnostics preserve bounded failure facts without private output or completion proof', () => {
  const facts = []
  const values = { owner: 65, lane: 4, owners_finished: 0, node_lanes: 4, node_invalid: 1, node_invalid_causes: 127,
    samples: 360, traffic_ms: 130542, sample_first_ms: 140000, sample_last_ms: 142000, frozen: 2, retried: 1,
    delivered_allowed: 5000, delivered_block: 400, expected: 5000, accounted: 4999, accounted_negative: 0,
    node_keys: 0, node_pending: 2, node_dropped: 3, node_unmatched: 4, queue_batches: 1 }
  for (const prefix of ['before', 'after']) for (const name of ['private', 'usage', 'denied', 'unmatched', 'dropped', 'stale', 'pending_dropped', 'pending_unmatched', 'queued']) values[`${prefix}_${name}`] = 0
  for (const [name, value] of Object.entries(values)) {
    const result = sanitizeDestinationResults(encode([{ Action: 'output', Output: `private-path:4: native global drain: ${name}=${value}\n` }]))
    const event = JSON.parse(result)
    assert.equal(event.Diagnostic, `global_drain_${name}`)
    assert.equal(event.Observed, value)
    assert(acceptance.isSafeDestinationDiagnostic(event))
    assert(!result.includes('private-path'))
    facts.push(event)
  }
  for (const reason of ['state', 'node_samples', 'sample_window', 'access_quiescence', 'frozen_retry', 'ingress_accounting', 'usage_read', 'hit_read', 'budget_read', 'loss_query', 'loss_scan', 'loss_cursor', 'ledger_accounting', 'node_pending', 'psp_pending']) {
    const result = sanitizeDestinationResults(encode([{ Action: 'output', Output: `private-path:4: global batch drain: ${reason}\n` }]))
    const event = JSON.parse(result)
    assert.equal(event.Diagnostic, `global_batch_drain_${reason}`)
    assert(acceptance.isSafeDestinationDiagnostic(event))
    assert(!result.includes('private-path'))
    facts.push(event)
  }
  for (const line of ['owner=0', 'owner=66', 'lane=0', 'lane=5', 'owners_finished=66', 'node_lanes=5', 'node_invalid=2', 'node_invalid_causes=128', 'accounted_negative=2', 'expected=-1', 'expected=1000000000001', 'traffic_ms=1200001', 'agent_id=1', 'pid=1', 'before_secret=1', 'accounted=2; private']) {
    assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native global drain: ${line}\n` }])).trim(), '')
  }
  assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: 'global batch drain: private\n' }])).trim(), '')
  assert(!acceptance.isSafeDestinationDiagnostic({ Diagnostic: 'global_drain_pid', Observed: 1 }))
  assert.throws(() => acceptance.checkDestinationGlobalBatchResults(encode(facts)))
  const timeout = JSON.parse(sanitizeDestinationResults(encode([{ Action: 'output', Output: 'timed out waiting for global real canonical retry, SQL drain and all owner loss ledgers\n' }])))
  assert.equal(timeout.Diagnostic, 'global_batch_drain_timeout')
})

test('global control stage diagnostics expose only bounded aggregate timing and never completion proof', async () => {
  const { isSafeDestinationDiagnostic, checkDestinationGlobalBatchResults } = await import('./destination-acceptance.mjs')
  const facts = []
  for (const stage of ['desired', 'ingest', 'policy', 'panel', 'config', 'roster', 'directives', 'tasks', 'finish']) {
    for (const [field, value] of [['count', 1000000], ['mean_ms', 0], ['p95_ms', 12345], ['max_ms', 1200000]]) {
      const result = sanitizeDestinationResults(encode([{ Action: 'output', Output: `private identity: native global stage: ${stage}_${field}=${value}\n` }]))
      const event = JSON.parse(result)
      assert.equal(event.Diagnostic, `global_stage_${stage}_${field}`)
      assert.equal(event.Observed, value)
      assert(isSafeDestinationDiagnostic(event))
      assert(!result.includes('private identity'))
      facts.push(event)
    }
  }
  assert.throws(() => checkDestinationGlobalBatchResults(encode(facts)))
  for (const line of ['agent_count=1', 'directives_count=0', 'directives_count=1000001', 'desired_mean_ms=-1', 'ingest_p95_ms=1200001', 'policy_mean_ms=1.5', 'tasks_max_ms=1; credential=secret']) {
    assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native global stage: ${line}\n` }])).trim(), '')
  }
  assert(!isSafeDestinationDiagnostic({ Diagnostic: 'global_stage_agent_count', Observed: 1 }))
})

test('Docker recovery diagnostics retain fixed categories without task or credential output', () => {
  const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const Test = 'TestNativePublishedDockerRollback/health_failure_restore'
  for (const [message, expected] of [
    ['Docker automatic restoration changed original receipt identity', 'docker_restore_receipt_failed'],
    ['Docker automatic restoration changed original execution identity', 'docker_restore_execution_failed'],
    ['Docker automatic restoration changed original installation identity', 'docker_restore_installation_failed'],
    ['timed out waiting for Docker failed result acknowledgement', 'docker_failed_ack_timeout'],
    ['Docker replacement lost private material, original state inode or advancing counter epoch', 'docker_preservation_failed'],
  ]) {
    const output = sanitizeDestinationResults(encode([{ Action: 'output', Package: pkg, Test, Output: `${message}; task=private-id credential=secret host=private-host` }]))
    assert.deepEqual(JSON.parse(output), { Action: 'output', Package: pkg, Test, Diagnostic: expected })
    for (const text of ['private-id', 'secret', 'private-host', 'Output']) assert(!output.includes(text))
  }
  assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Package: pkg, Test, Output: 'private task receipt credential address' }])), '\n')
})

test('setup stages remain closed and expose no request or account parameters', async () => {
  const { isSafeDestinationDiagnostic } = await import('./destination-acceptance.mjs')
  for (const stage of ['capability', 'group', 'principal', 'membership', 'policy', 'mode', 'collection', 'publication', 'receipt']) {
    const event = JSON.parse(sanitizeDestinationResults(encode([{ Action: 'output', Output: `private-file:1: native setup stage: ${stage}\n` }])))
    assert.equal(event.Diagnostic, `global_setup_stage_${stage}`)
    assert(isSafeDestinationDiagnostic(event))
    assert.deepEqual(Object.keys(event).sort(), ['Action', 'Diagnostic', 'Package'])
  }
  for (const value of ['mode credential=secret', 'private-email', 'membership=123']) {
    assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native setup stage: ${value}\n` }])), '\n')
  }
  assert(!isSafeDestinationDiagnostic({ Diagnostic: 'global_setup_stage_private' }))
})

test('global timeout stack facts retain only closed operation categories', async () => {
  const { isSafeDestinationDiagnostic } = await import('./destination-acceptance.mjs')
  const frames = {
    'internal/service/sharedclient.(*Service).ProvisionClient(': 'provision',
    'internal/service/nodesync.(*Service).NativePanelSnapshot(': 'snapshot',
    'internal/service/nodesync.(*Service).NativePanelClientSnapshot(': 'scoped_snapshot',
    'internal/adapters/sqlstore.(*nativeDesiredRepo).Load(': 'desired_sql',
    'internal/adapters/sqlstore.(*nativeDesiredRepo).LoadClient(': 'scoped_desired_sql',
    'internal/adapters/sqlstore.(*nativeDesiredRepo).load(': 'desired_sql_internal',
    'internal/adapters/sqlstore.(*pspClientRepo).ListInbounds(': 'attachment_read',
    'internal/adapters/sqlstore.(*pspClientRepo).UpdateInboundStates(': 'attachment_write',
    'internal/adapters/sqlstore.(*nodeAgentRepo).RecordApplied(': 'stream_ack',
    'internal/adapters/sqlstore.(*nodeAgentRepo).RecordAppliedBatch(': 'stream_ack_vector',
    'internal/adapters/sqlstore.(*nodeAgentRepo).UpdateProtocolObservation(': 'protocol_write',
    'internal/adapters/sqlstore.(*nodeAgentRepo).MintConfigWithPolicyCandidate(': 'config_mint',
    'internal/adapters/sqlstore.(*nodeAgentIssueRepo).RecordBatch(': 'issues_write',
    'database/sql.(*DB).conn(': 'sql_connection',
  }
  for (const [frame, category] of Object.entries(frames)) {
    const result = sanitizeDestinationResults(encode([{ Action: 'output', Output: `github.com/KazuhaHub/passwall-sub-panel/${frame}0xprivate, credential-secret)\n` }]))
    const event = JSON.parse(result)
    assert.equal(event.Diagnostic, `global_batch_${category}_stack`)
    assert.deepEqual(Object.keys(event).sort(), ['Action', 'Diagnostic', 'Package'])
    assert(isSafeDestinationDiagnostic(event))
    assert(!result.includes('0xprivate') && !result.includes('credential-secret') && !result.includes(frame))
  }
  assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: 'private unknown.(*Repo).Read(credential-secret)\n' }])), '\n')
  assert(!isSafeDestinationDiagnostic({ Diagnostic: 'global_batch_unknown_stack' }))
})

test('Docker preservation keeps each original guard while withholding private installation details', async () => {
  const { isSafeDestinationDiagnostic } = await import('./destination-acceptance.mjs')
  for (const name of ['docker_preserved_material', 'docker_preserved_state', 'docker_advanced_epoch']) {
    for (const value of [true, false]) {
      const result = sanitizeDestinationResults(encode([{ Action: 'output', Output: `private-path:1: native diagnostic: ${name}=${value}\n` }]))
      const event = JSON.parse(result)
      assert.equal(event.Diagnostic, name)
      assert.equal(event.Observed, value)
      assert(isSafeDestinationDiagnostic(event))
      assert(!result.includes('private-path'))
    }
  }
  for (const line of ['docker_state_inode=12345', 'docker_epoch_value=42', 'docker_material_hash=private', 'docker_advanced_epoch=true; credential=secret']) {
    assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native diagnostic: ${line}\n` }])).trim(), '')
  }
  const source = readFileSync(new URL('../internal/app/destination_native_docker_test.go', import.meta.url), 'utf8')
  assert.match(source, /binary\.BigEndian\.Uint64\(current\.epoch\) > binary\.BigEndian\.Uint64\(before\.epoch\)/)
  assert.match(source, /if !material \|\| !state \|\| !epoch/)
  assert.match(source, /nativeWait\(caseTest, 3\*time\.Minute, "Docker restart production convergence and counter epoch", func\(\) bool \{\s*return converged\(nativeRemotePublishedIdentity, after\) && binary\.BigEndian\.Uint64\(m\.preserved\(caseTest\)\.epoch\) > binary\.BigEndian\.Uint64\(before\.epoch\)/)
})

test('status candidate diagnostics retain only fixed comparison facts', () => {
  const facts = ['rows', 'panel', 'kind', 'reason', 'exhausted', 'rules', 'pending', 'applied_at', 'state', 'paused', 'generation',
    'is_applied', 'is_sniffing', 'is_paused', 'is_pending', 'is_offline', 'is_none', 'is_rejected', 'is_over_limit']
  for (const fact of facts) for (const value of ['true', 'false']) {
    const event = JSON.parse(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native diagnostic: status_${fact}=${value}\n` }])))
    assert.deepEqual(event, { Action: 'output', Package: 'github.com/KazuhaHub/passwall-sub-panel/internal/app', Diagnostic: `status_${fact}`, Observed: value === 'true' })
  }
  for (const output of ['native diagnostic: status_secret=true\n', 'native diagnostic: status_state=private-receipt\n', 'native diagnostic: status_rules=123\n']) {
    assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: output }])), '\n')
  }
})

test('load readiness diagnostics retain each exact comparison as a boolean without private output', async () => {
  const { isSafeDestinationDiagnostic } = await import('./destination-acceptance.mjs')
  for (const name of ['state', 'runtime', 'agent', 'panel', 'selection', 'observed_core', 'transition', 'collection', 'rule_count', 'rule_shape', 'config', 'roster']) {
    for (const value of ['true', 'false']) {
      const event = JSON.parse(sanitizeDestinationResults(encode([{ Action: 'output', Package: 'private', Output: `private credential: native diagnostic: load_${name}=${value}\n` }])))
      assert.deepEqual(event, { Action: 'output', Package: 'github.com/KazuhaHub/passwall-sub-panel/internal/app', Diagnostic: `load_${name}`, Observed: value === 'true' })
      assert(isSafeDestinationDiagnostic(event))
    }
  }
  for (const value of ['credential=true', 'state=private', 'selection=123', 'roster=true private']) {
    assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native diagnostic: load_${value}\n` }])), '\n')
  }
  const timeout = JSON.parse(sanitizeDestinationResults(encode([{ Action: 'output', Output: 'timed out waiting for exact load policy and collection receipt; credential withheld' }])))
  assert.equal(timeout.Diagnostic, 'load_receipt_timeout')
  assert(isSafeDestinationDiagnostic(timeout))
})

test('remote restoration diagnostics retain only fixed booleans', () => {
  for (const suffix of ['task_terminal', 'receipt_failed', 'receipt_activated', 'source_installed', 'seen', 'identity', 'capability', 'streams', 'policy', 'report', 'core', 'listener', 'journal_readable', 'journal_failed', 'journal_running', 'journal_delivered', 'quarantined', 'failed_result_posted', 'failed_result_accepted', 'failed_result_rejected', 'outbox_pending', 'outbox_valid', 'fresh_after_restore', 'sync_validation_error', 'sync_outbox_error', 'sync_observer_error', 'sync_duplicate_issue', 'sync_stream_error', 'sync_process_error']) {
    const category = `remote_restore_${suffix}`
    for (const value of ['true', 'false']) {
      const retained = sanitizeDestinationResults(encode([{ Action: 'output', Output: `native diagnostic: ${category}=${value}\n` }]))
      assert.equal(JSON.parse(retained).Diagnostic, category)
      assert.equal(JSON.parse(retained).Observed, value === 'true')
    }
  }
  for (const output of ['native diagnostic: remote_restore_secret=true\n', 'native diagnostic: remote_restore_policy=private\n']) assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: output }])), '\n')
})

test('all real cores require scoped users, exemption expiry and allow priority', async () => {
  const acceptance = await import('./destination-acceptance.mjs')
  const required = ['group_scope', 'exemption', 'exemption_expired', 'allow_priority']
  for (const cases of [destinationCases, acceptance.singBoxCases, acceptance.guardedSingBoxCases]) {
    for (const name of required) assert(cases.includes(name), name)
  }
  for (const source of ['destination_native_acceptance_test.go', 'destination_native_singbox_test.go']) {
    const driver = readFileSync(new URL(`../internal/app/${source}`, import.meta.url), 'utf8')
    for (const name of required) assert(driver.includes(`run("${name}"`), name)
  }
})

test('legacy upgrade has an independent complete real execution gate and immutable released sources', async () => {
  const acceptance = await import('./destination-acceptance.mjs')
  assert.equal(typeof acceptance.checkLegacyUpgradeResults, 'function')
  const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const tests = ['TestNativeLegacyPSPUpgrade', ...['old_panel', 'panel_upgrade', 'legacy_publication', 'mixed_fleet', 'mixed_offline_pause', 'node_upgrade', 'node_downgrade', 'node_reupgrade', 'panel_downgrade_offline', 'panel_downgrade', 'panel_reupgrade', 'allowlist_capability_setup', 'allowlist_capability_removed', 'allowlist_capability_restored'].map(name => `TestNativeLegacyPSPUpgrade/${name}`)]
  const values = [...tests.flatMap(Test => [{ Action: 'run', Package: pkg, Test }, { Action: 'pass', Package: pkg, Test }]), { Action: 'pass', Package: pkg }]
  const evidence = sanitizeDestinationResults(encode(values))
  assert.doesNotThrow(() => acceptance.checkLegacyUpgradeResults(evidence))
  assert.throws(() => checkDestinationResults(evidence))
  for (const name of tests) {
    assert.throws(() => acceptance.checkLegacyUpgradeResults(encode(values.filter(event => event.Test !== name))))
    assert.throws(() => acceptance.checkLegacyUpgradeResults(encode(values.map(event => event.Test === name && event.Action === 'pass' ? { ...event, Action: 'skip' } : event))))
  }
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  const legacyJob = workflow.slice(workflow.indexOf('  legacy-upgrade:'))
  for (const value of ['0ae05e1e40170c030acd23cdf637310b740b50a6', '8e5e3db72219a8db9081a372722abb1529d22ecd', nodeCandidate.commit, 'native-legacy-panel-bridge.go.txt', 'check-legacy-upgrade', 'PSP_NATIVE_LEGACY_PANEL_BIN', 'PSP_NATIVE_LEGACY_NODE_BIN', 'PSP_NATIVE_MIXED_NODE_BIN']) assert(legacyJob.includes(value), value)
})
import * as acceptance from './destination-acceptance.mjs'

test('panel transition evidence retains bounded application duration and fixed facts only', () => {
  for (const phase of ['downgrade', 'reupgrade']) {
    for (const duration of [0, 5720, 120000, 150000]) {
      const event = JSON.parse(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native panel application: ${phase}=${duration}\n` }])))
      assert.equal(event.Diagnostic, `panel_${phase}_application_ms`)
      assert.equal(event.Observed, duration)
    }
  }
  for (const fact of ['streams', 'capability', 'config_known', 'config_policy', 'report_applied', 'report_empty', 'report_current', 'original_core_exited']) {
    for (const value of ['true', 'false']) {
      const event = JSON.parse(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native diagnostic: panel_${fact}=${value}\n` }])))
      assert.equal(event.Diagnostic, `panel_${fact}`)
      assert.equal(event.Observed, value === 'true')
    }
  }
  for (const value of ['downgrade=150001', 'secret=1', 'reupgrade=-1', 'downgrade=credential', 'reupgrade=1.25']) assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native panel application: ${value}\n` }])), '\n')
})

function events() {
  const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const names = ['TestNativeDestinationTraffic', ...destinationCases.map(name => `TestNativeDestinationTraffic/${name}`)]
  return [...names.flatMap(Test => [{ Action: 'run', Package: pkg, Test }, { Action: 'pass', Package: pkg, Test }]), { Action: 'pass', Package: pkg }]
}
const encode = values => values.map(value => JSON.stringify(value)).join('\n')
test('node transition diagnostics retain only fixed booleans without private state', () => {
  for (const fact of ['sequence', 'capability', 'policy', 'core', 'listener', 'config', 'roster', 'version', 'original_etags', 'agent_fresh', 'streams', 'minted_empty']) {
    for (const value of ['true', 'false']) {
      const result = JSON.parse(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native diagnostic: node_transition_${fact}=${value}\n` }])))
      assert.equal(result.Diagnostic, `node_transition_${fact}`)
      assert.equal(result.Observed, value === 'true')
    }
  }
  for (const text of ['credential=secret', 'config=private', 'address=true']) assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native diagnostic: node_transition_${text}\n` }])), '\n')
})

test('allowlist capability diagnostics retain only fixed convergence booleans', () => {
  for (const fact of ['streams', 'capability', 'agent_fresh', 'membership', 'policy_body', 'policy_receipt']) {
    for (const value of ['true', 'false']) {
      const result = JSON.parse(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native diagnostic: capability_${fact}=${value}\n` }])))
      assert.equal(result.Diagnostic, `capability_${fact}`)
      assert.equal(result.Observed, value === 'true')
    }
  }
  for (const text of ['credential=secret', 'address=true', 'membership=private']) assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native diagnostic: capability_${text}\n` }])), '\n')
})
test('Xray separately requires actual LKG and pruned-candidate rejection', () => {
  for (const name of ['lkg_rejected', 'pruned_rejected']) {
    assert(destinationCases.includes(name))
    assert.throws(() => checkDestinationResults(encode(events().filter(event => !event.Test?.endsWith(`/${name}`)))))
  }
})
test('Xray requires actual policy rejection and retry independently of listener failure', () => {
  assert(destinationCases.includes('policy_rejected'))
  assert.throws(() => checkDestinationResults(encode(events().filter(event => !event.Test?.endsWith('/policy_rejected')))))
})
test('each engine requires a real report-only refresh without replacing its core', () => {
  for (const cases of [destinationCases, acceptance.singBoxCases, acceptance.guardedSingBoxCases]) assert(cases.includes('report_refresh'))
  assert.throws(() => checkDestinationResults(encode(events().filter(event => !event.Test?.endsWith('/report_refresh')))))
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  assert(workflow.includes("PSP_NATIVE_SOURCE_COMMIT='${{ github.event.pull_request.head.sha || github.sha }}'"))
})
test('each engine requires the real unchanged-sync process identity case', () => {
  for (const cases of [destinationCases, acceptance.singBoxCases, acceptance.guardedSingBoxCases]) assert(cases.includes('idle_sync'))
  assert.throws(() => checkDestinationResults(encode(events().filter(event => !event.Test?.endsWith('/idle_sync')))))
})
test('native matrix requires the actual core-check timeout case', () => {
  assert(destinationCases.includes('core_check_timeout'))
  assert.throws(() => checkDestinationResults(encode(events().filter(event => !event.Test?.endsWith('/core_check_timeout')))))
})
test('listener diagnostics retain only fixed booleans and withhold arbitrary labels and values', () => {
  for (const category of ['listener_issue', 'listener_core_rejected', 'listener_agent_fresh', 'listener_policy_ready', 'listener_policy_unchanged', 'listener_epoch', 'listener_config_changed', 'listener_config_preserved', 'listener_config_fresh', 'receipt_generation', 'receipt_mint_fresh', 'receipt_sync_fresh', 'receipt_state', 'receipt_sha', 'receipt_lkg', 'receipt_precheck', 'receipt_streams', 'receipt_transition', 'fallback_report', 'fallback_accepted', 'fallback_empty', 'fallback_exhausted', 'fallback_core', 'fallback_streams', 'fallback_issue']) {
    for (const value of ['true', 'false']) {
      const retained = sanitizeDestinationResults(encode([{ Action: 'output', Package: 'private', Output: `native diagnostic: ${category}=${value}\n` }]))
      assert.equal(JSON.parse(retained).Diagnostic, category)
      assert.equal(JSON.parse(retained).Observed, value === 'true')
    }
  }
  for (const output of ['native diagnostic: listener_secret=true\n', 'native diagnostic: listener_issue=credential\n']) assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: output }])), '\n')
})
test('retained execution evidence excludes process output and arbitrary metadata', () => {
  const retained = sanitizeDestinationResults(encode([...events(), { Action: 'output', Package: 'private', Output: 'credential email address' }, { Action: 'pass', Package: 'ignored', Output: 'private', Private: 'secret' }]))
  for (const privateText of ['credential', 'email', 'address', 'private', 'secret', 'Output', 'Private']) assert(!retained.includes(privateText))
  assert.doesNotThrow(() => checkDestinationResults(retained))
  const diagnostic = sanitizeDestinationResults(encode([{ Action: 'output', Package: 'private', Output: 'private address: real VLESS traffic did not reach its nonce target; credential secret' }]))
  assert.equal(JSON.parse(diagnostic).Diagnostic, 'traffic_allow_failed')
  assert(!diagnostic.includes('credential'))
  assert(!diagnostic.includes('address'))
})
test('acceptance requires immutable reviewed Node source', () => {
  assert.equal(nodeCandidate.repository, 'KazuhaHub/Passwall-Node')
  assert.match(nodeCandidate.commit, /^[a-f0-9]{40}$/)
})
test('integrated Node executes the complete core matrix without replacing fixed checkpoints', () => {
  assert.equal(acceptance.integratedNodeCandidate?.repository, nodeCandidate.repository)
  assert.equal(acceptance.integratedNodeCandidate?.commit, 'e2c9dfbfe934d4a61fda84b0a80b4f1cb5dbc719')
  assert.equal(acceptance.moduleNodeCandidate?.repository, nodeCandidate.repository)
  assert.equal(acceptance.moduleNodeCandidate?.commit, '96693d3c128d82f0d154774f593c497a276a1105')
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  const traffic = workflow.slice(workflow.indexOf('  traffic:'), workflow.indexOf('  published-remote:'))
  const rows = [...traffic.matchAll(/\{engine: ([\w-]+), core: '([^']+)', candidate: (\w+), node: '([a-f0-9]{40})'\}/g)]
    .map(([, engine, core, candidate, commit]) => [engine, core, candidate, commit])
  assert.deepEqual(rows, [
    ...['26.6.27', '26.7.28', '26.9.9'].map(core => ['xray', core, 'baseline', nodeCandidate.commit]),
    ['sing-box', '1.14.0', 'baseline', nodeCandidate.commit],
    ['sing-box', '1.14.0', 'guarded', acceptance.guardedNodeCandidate.commit],
    ...['26.6.27', '26.7.28', '26.9.9'].map(core => ['xray', core, 'integrated', acceptance.integratedNodeCandidate.commit]),
    ['sing-box', '1.14.0', 'integrated', acceptance.integratedNodeCandidate.commit],
    ...['26.6.27', '26.7.28', '26.9.9'].map(core => ['xray', core, 'module', acceptance.moduleNodeCandidate.commit]),
    ['sing-box', '1.14.0', 'module', acceptance.moduleNodeCandidate.commit],
  ])
  assert(traffic.includes('xray:baseline|xray:integrated|xray:module) test_name=TestNativeDestinationTraffic; verifier=check ;;'))
  assert(traffic.includes('sing-box:guarded|sing-box:integrated|sing-box:module) test_name=TestNativeGuardedSingBoxDestinationTraffic; verifier=check-guarded-singbox ;;'))
  assert(traffic.includes('sing-box:baseline) test_name=TestNativeSingBoxDestinationTraffic; verifier=check-singbox ;;'))
  assert(traffic.includes('*) exit 1 ;;'))
  assert(traffic.includes('native-destination-${{ matrix.engine }}-${{ matrix.core }}-${{ matrix.candidate }}-${{ github.sha }}'))
})
test('SQL runtime matrix executes every reviewed core using the full verifier and owned database', () => {
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  const start = workflow.indexOf('  traffic-sql:')
  assert(start >= 0, 'real SQL traffic job is missing')
  const job = workflow.slice(start, workflow.indexOf('  published-remote:', start))
  const rows = [...job.matchAll(/\{engine: ([\w-]+), core: '([^']+)', database: (\w+)\}/g)]
    .map(([, engine, core, database]) => [engine, core, database])
  assert.deepEqual(rows, ['mysql', 'postgres'].flatMap(database => [
    ...['26.6.27', '26.7.28', '26.9.9'].map(core => ['xray', core, database]),
    ['sing-box', '1.14.0', database],
  ]))
  for (const required of [acceptance.moduleNodeCandidate.commit, 'mysql:8', 'postgres:16',
    'PSP_NATIVE_DB_KIND', 'psp_native_acceptance', 'TestNativeDestinationTraffic',
    'TestNativeGuardedSingBoxDestinationTraffic', 'verifier=check', 'verifier=check-guarded-singbox',
    'destination-acceptance.mjs "$verifier"', 'native-destination-sql-${{ matrix.database }}-${{ matrix.engine }}-${{ matrix.core }}-${{ github.sha }}',
    'if-no-files-found: error']) assert(job.includes(required), required)
  assert(!job.includes('continue-on-error'))
  assert(!job.includes('PSP_NATIVE_DB_DSN'), 'the fixture must not accept arbitrary database endpoints')
  for (const source of ['destination_native_acceptance_test.go', 'destination_native_singbox_test.go']) {
    const driver = readFileSync(new URL(`../internal/app/${source}`, import.meta.url), 'utf8')
    assert(driver.includes('buildNativeDestinationPolicyFixture(t)'), source)
  }
})

test('real execution gate rejects absent, skipped, failed, duplicate and incomplete cases', () => {
  assert.doesNotThrow(() => checkDestinationResults(encode(events())))
  for (const values of [[], events().slice(0, -1), events().filter(e => !e.Test?.endsWith('/trial')), [...events(), events()[0]], events().map(e => e.Test?.endsWith('/block') && e.Action === 'pass' ? { ...e, Action: 'skip' } : e), [...events(), { Action: 'fail', Package: 'other/package' }]]) {
    assert.throws(() => checkDestinationResults(encode(values)))
  }
  assert.throws(() => checkDestinationResults('not JSON'))
})
test('workflow exercises the actual Node/PSP boundary and uploads only reviewed evidence', () => {
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  for (const required of ['ubuntu-24.04', nodeCandidate.commit, 'destination_acceptance', 'TestNativeDestinationTraffic', 'verifier=check', 'destination-acceptance.mjs "$verifier"', '26.6.27', '26.7.28', '26.9.9', 'PSP_NATIVE_NODE_BIN', 'PSP_NATIVE_CORE_VERSION']) assert(workflow.includes(required), required)
  assert(!workflow.includes('continue-on-error'))
  assert(!workflow.includes('secrets.'))
  assert(!workflow.includes('actions/upload-artifact') || workflow.includes('destination-acceptance-tests.json'))
})
test('the real driver exercises the first report and traffic after a daemon restart', () => {
  const driver = readFileSync(new URL('../internal/app/destination_native_acceptance_test.go', import.meta.url), 'utf8')
  assert(destinationCases.includes('restart'))
  for (const required of ['run("restart"', 'probe.arm()', 'probe.first()', 'stopDaemon(caseTest)', 'startDaemon(caseTest)']) assert(driver.includes(required), required)
})
test('the driver must execute every declared real traffic and fault scenario', () => {
  const driver = readFileSync(new URL('../internal/app/destination_native_acceptance_test.go', import.meta.url), 'utf8')
  for (const name of destinationCases) assert(driver.includes(`run("${name}"`), name)
})

test('sing-box execution has its own complete gate and cannot borrow Xray passes', () => {
  assert.equal(typeof acceptance.checkSingBoxResults, 'function')
  const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const names = ['TestNativeSingBoxDestinationTraffic', ...acceptance.singBoxCases.map(name => `TestNativeSingBoxDestinationTraffic/${name}`)]
  const values = [...names.flatMap(Test => [{ Action: 'run', Package: pkg, Test }, { Action: 'pass', Package: pkg, Test }]), { Action: 'pass', Package: pkg }]
  const evidence = sanitizeDestinationResults(encode(values))
  assert.doesNotThrow(() => acceptance.checkSingBoxResults(evidence))
  assert.throws(() => checkDestinationResults(evidence))
  assert.throws(() => acceptance.checkSingBoxResults(encode(events())))
  for (const name of names) {
    assert.throws(() => acceptance.checkSingBoxResults(encode(values.filter(event => event.Test !== name))))
    assert.throws(() => acceptance.checkSingBoxResults(encode([...values, { Action: 'run', Package: pkg, Test: name }])))
    assert.throws(() => acceptance.checkSingBoxResults(encode(values.map(event => event.Test === name && event.Action === 'pass' ? { ...event, Action: 'skip' } : event))))
  }
})

test('receipt diagnostic sequence is bounded and contains no arbitrary output', () => {
  for (const n of [1, 17, 99]) {
    const retained = sanitizeDestinationResults(encode([{ Action: 'output', Output: `native receipt wait: ${n}\n` }]))
    assert.equal(JSON.parse(retained).Diagnostic, 'receipt_wait')
    assert.equal(JSON.parse(retained).Observed, n)
  }
  for (const value of ['0', '100', 'secret', '-1']) assert.equal(sanitizeDestinationResults(encode([{ Action: 'output', Output: `native receipt wait: ${value}\n` }])), '\n')
})

test('sing-box driver and hosted workflow execute every declared scenario', () => {
  const driver = readFileSync(new URL('../internal/app/destination_native_singbox_test.go', import.meta.url), 'utf8')
  for (const name of acceptance.singBoxCases) assert(driver.includes(`run("${name}"`), name)
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  for (const value of ['sing-box', '1.14.0', 'TestNativeSingBoxDestinationTraffic', 'check-singbox']) assert(workflow.includes(value), value)
  const restart = driver.slice(driver.indexOf('run("restart"'), driver.indexOf('run("collection_off"'))
  for (const required of ['receiptAfter = time.UnixMilli(time.Now().UnixMilli())', 'waitAppliedCandidate(prior)', 'nativeObservedRuntimeReady', 'GetServerStatus', 'GetInbound', 'net.DialTimeout', 'nativeBindLiveCore', 'traffic(literalHost, true)']) assert(restart.includes(required), required)
  assert(workflow.includes('RestartRequiresObservedCoreAndInbound'))
})

test('guarded sing-box keeps an independent immutable candidate and complete execution gate', () => {
  assert.equal(acceptance.guardedNodeCandidate?.repository, nodeCandidate.repository)
  assert.equal(acceptance.guardedNodeCandidate?.commit, 'a6163c8b5c10a9c8129cf660020fe2c8fa2b85db')
  assert.notEqual(acceptance.guardedNodeCandidate.commit, nodeCandidate.commit)
  assert.deepEqual(acceptance.guardedSingBoxCases, [...acceptance.singBoxCases.filter(name => name !== 'private_baseline'), 'private_guard'])
  const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  const names = ['TestNativeGuardedSingBoxDestinationTraffic', ...acceptance.guardedSingBoxCases.map(name => `TestNativeGuardedSingBoxDestinationTraffic/${name}`)]
  const values = [...names.flatMap(Test => [{ Action: 'run', Package: pkg, Test }, { Action: 'pass', Package: pkg, Test }]), { Action: 'pass', Package: pkg }]
  const evidence = sanitizeDestinationResults(encode(values))
  assert.doesNotThrow(() => acceptance.checkGuardedSingBoxResults(evidence))
  assert.throws(() => acceptance.checkSingBoxResults(evidence))
  for (const name of names) {
    assert.throws(() => acceptance.checkGuardedSingBoxResults(encode(values.filter(event => event.Test !== name))))
    assert.throws(() => acceptance.checkGuardedSingBoxResults(encode([...values, { Action: 'run', Package: pkg, Test: name }])))
    assert.throws(() => acceptance.checkGuardedSingBoxResults(encode(values.map(event => event.Test === name && event.Action === 'pass' ? { ...event, Action: 'skip' } : event))))
  }
  assert.throws(() => acceptance.checkGuardedSingBoxResults(encode(events())))
  const baseline = values.map(event => event.Test ? { ...event, Test: event.Test.replace('TestNativeGuardedSingBoxDestinationTraffic', 'TestNativeSingBoxDestinationTraffic').replace('/private_guard', '/private_baseline') } : event)
  assert.throws(() => acceptance.checkGuardedSingBoxResults(encode(baseline)))
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  for (const required of [nodeCandidate.commit, acceptance.guardedNodeCandidate.commit, '${{ matrix.node }}', 'check-guarded-singbox', 'TestNativeGuardedSingBoxDestinationTraffic']) assert(workflow.includes(required), required)
  const driver = readFileSync(new URL('../internal/app/destination_native_singbox_test.go', import.meta.url), 'utf8')
  for (const required of ['func TestNativeGuardedSingBoxDestinationTraffic', 'run("private_guard"', 'privateRequests.Load()', 'privateDestinations(false)', 'strconv.Itoa(privatePort)']) assert(driver.includes(required), required)
})
