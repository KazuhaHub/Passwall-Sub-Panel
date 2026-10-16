import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

test('global fixture preflight executes lifecycle, cache and SQL boundaries without claiming a native cohort', () => {
  const workflow = readFileSync(new URL('../.github/workflows/test.yml', import.meta.url), 'utf8')
  const body = workflow.slice(workflow.indexOf('  global-fixture:')).split(/\r?\n(?=  sqlite:)/)[0]
  assert(body.startsWith('  global-fixture:'))
  assert.match(body, /runs-on: ubuntu-24\.04/)
  assert.match(body, /-count=1 -timeout=4m -tags destination_acceptance/)
  for (const root of ['AuditStartPreservesRealQueueAndPermissionChanges', 'PublicCoreCopyExcludesStateAndRejectsUntrustedFiles', 'StoragePauseKeepsControlAndOriginalSQLTransactions', 'TransportKeepsProductionAuthenticationAndExactBodies']) assert(body.includes(root))
  assert(body.includes('MixedProbePreservesRecoveryFailures'))
  assert(body.includes('RequestFailureFactsPreserveErrorsAndBlockedReplies'))
  assert(body.includes('WorkerProbeKeepsFreshConnectionsAndResponseSemantics'))
  const gate = workflow.slice(workflow.indexOf('  sqlite:')).split(/\r?\n(?=  postgres:)/)[0]
  assert.match(gate, /needs: \[race-shard, app-race-shard, global-fixture\]/)
  assert(gate.includes('needs.global-fixture.result') && gate.includes('if: always()'))
  const source = readFileSync(new URL('../internal/app/destination_native_global_fixture_test.go', import.meta.url), 'utf8')
  assert(source.includes('destinationAuditRunner.Run(ctx)') && source.includes('observer.Pressure()'))
  assert(source.includes('gate.Pressure()[0].Batches != 0') && source.includes('serverAuditRequest'))
  assert(source.includes('gate.StopOffers()') && source.includes('SELECT count FROM dest_hits'))
  assert(source.includes('nativeGlobalOwners(gate.Pressure())') && source.includes('owners != 65'))
  assert(source.includes('entries[0].Name() != "install.json"') && source.includes('entries[1].Name() != "xray"'))
  assert(source.includes('decoder.DisallowUnknownFields()') && source.includes('os.O_EXCL'))
  assert.doesNotMatch(source, /SetSnapshot\(|\.collector\.Ack\(|runtime\.GC\(|sysctl/)
})

test('global worker scratch reuse keeps fresh requests and the original pressure contract', () => {
  const probe = readFileSync(new URL('../internal/app/destination_native_worker_probe_test.go', import.meta.url), 'utf8')
  const driver = readFileSync(new URL('../internal/app/destination_native_global_batch_test.go', import.meta.url), 'utf8')
  const workflow = readFileSync(new URL('../.github/workflows/destination-load.yml', import.meta.url), 'utf8')
  for (const required of ['Timeout: 3 * time.Second', 'defer c.Close()', 'Add(5 * time.Second)', 'nativeVLESSRequest(uuid, address, port, host)', 'p.reader.Reset(c)', 'defer p.reader.Reset(nil)', 'io.ReadFull(p.reader, response[:])', 'http.ReadResponse(p.reader, nil)', 'body   [1024]byte', 'responseHTTP.Body.Close()', 'nativeVLESSHost(ctx, port, currentAddress', 'accepted.Load() != 2', 'nativeRequestFailureIndex(before) != nativeRequestFailureIndex(after)']) assert(probe.includes(required))
  assert(driver.includes('var probe nativeVLESSWorkerProbe') && driver.includes('probe.request(ctx, peer.node.DesiredPort, host, port, uuid, want, host)'))
  for (const required of ['const seconds, rate = 120, 10', 'make(chan job, 4096)', 'for range 512', 'm.TrafficMS > (seconds+10)*1000', 'nativePressureBlockRequest(f.user.UUID, name(0, j.seq), blockPort)', 'users[j.seq%200].UUID']) assert(driver.includes(required))
  assert(workflow.includes('TestNativeGlobalWorkerProbeKeepsFreshConnectionsAndResponseSemantics'))
  const blocked = readFileSync(new URL('../internal/app/destination_native_load_test.go', import.meta.url), 'utf8').split('func nativeLoadBlockedRequestFailure(')[1].split('func nativeLoadSampler(')[0]
  for (const source of [probe, blocked]) {
    assert(source.includes('Timeout: 3 * time.Second, KeepAlive: -1'))
    assert(source.includes('Add(5 * time.Second)'))
  }
  assert(blocked.includes('defer connection.Close()') && blocked.includes('io.LimitReader(connection, 4)'))
  assert.doesNotMatch(probe, /sync\.Pool|http\.Transport|\.Ack\(|SetSnapshot\(/)
})

test('global transport preserves raw authenticated bodies and delegates unknown input', () => {
  const source = readFileSync(new URL('../internal/app/destination_native_global_transport_test.go', import.meta.url), 'utf8')
  for (const required of ['peer.probe.agent != peer.agent.AgentID', 'handlers[peer.agent.AgentID] != nil', 'peer.probe.handler(handler)', 'protocol.MaxSyncBodyBytes+1', 'io.NopCloser(bytes.NewReader(body))', 'nativeGlobalTransportReadError{err}', 'original.ServeHTTP(w, r)', 'f.a.server.Handler.ServeHTTP(w, r)', 'wrong-owned-fixture-credential', 'untimed transport preflight fabricated a canonical retry']) assert(source.includes(required))
  const implementation=source.slice(0,source.indexOf('func TestNativeGlobalByteTransport'))
  assert.doesNotMatch(implementation, /\.start\s*=|\.Ack\(|SetSnapshot\(|http\.StatusOK\)/)
})

test('global storage hold decorates admitted real SQL without replacing production collection', () => {
  const app = readFileSync(new URL('../internal/app/app.go', import.meta.url), 'utf8')
  assert(app.includes('return buildWithDestinationAuditStore(ctx, cfg, nil)'))
  assert(app.includes('var auditStore ports.DestAuditStore = admittedDestinationAuditStore{store: repos.DestAudit, gate: operationGate}'))
  assert(app.indexOf('auditStore = decorate(auditStore)') > app.indexOf('var auditStore ports.DestAuditStore'))
  assert(app.includes('destaudit.New(ctx, auditStore') && app.includes('Audit: destAudit,'))
  const source = readFileSync(new URL('../internal/app/destination_native_global_storage_test.go', import.meta.url), 'utf8')
  for (const required of ['g.DestAuditStore.BeginDestinationAudit(ctx, batch)', 'case <-ctx.Done():', 'return domain.DestAuditBegin{}, ctx.Err()', 'storage.DestAuditStore.(admittedDestinationAuditStore)', 'lane.Active != 1', 'storage.begins.Load() != before+1', 'serverAuditRequest', 'errors.Is(err, context.Canceled)']) assert(source.includes(required))
  assert.doesNotMatch(source, /SetSnapshot\(|\.collector\.Ack\(|unsafe\.|reflect\.|runtime\.GC\(|sysctl/)
  const database = readFileSync(new URL('../internal/app/destination_native_database_test.go', import.meta.url), 'utf8')
  assert(database.includes('return buildNativeDestinationPolicyFixtureWithAuditStore(t, nil)'))
  assert(database.includes('buildWithDestinationAuditStore(t.Context(), &cfg, decorate)'))
  assert(database.includes('nativeVerifyDestinationDatabase(t, a)') && database.includes('PSP_NATIVE_DB_DSN'))
})

test('authenticated admission evidence keeps original App assembly and executes under race detection', () => {
  const app = readFileSync(new URL('../internal/app/app.go', import.meta.url), 'utf8')
  assert(app.includes('return buildWithDestinationAuditStore(ctx, cfg, nil)'))
  assert(app.includes('return buildWithDestinationAuditObserver(ctx, cfg, decorate, nil)'))
  assert(app.includes('emitAudit := destinationAuditObserver()'))
  assert.match(app, /original\(e\)\s+observe\(e\)/)
  const fixture = readFileSync(new URL('../internal/app/destination_native_admission_observer_test.go', import.meta.url), 'utf8')
  for (const required of ['a.server.Handler.ServeHTTP(response, request)', 'wrong-owned-fixture-credential', 'gate.Pressure()[0].Batches != 0', 'CandidateBytes: len(wire)', 'Bytes: canonicalBytes', 'BatchLimit: 128', 'ByteLimit: 32 << 20', 'OwnerLimit: 4', 'next("queue_full")', 'next("duplicate_batch")', 'next("collect_off")', "WHERE reason = 'queue_full'", 'loss != 2']) assert(fixture.includes(required))
  assert.doesNotMatch(fixture, /SetSnapshot\(|\.collector\.Ack\(|unsafe\.|reflect\.|limits\s*=/)
  const workflow = readFileSync(new URL('../.github/workflows/test.yml', import.meta.url), 'utf8')
  const body = workflow.slice(workflow.indexOf('  global-fixture:')).split(/\r?\n(?=  sqlite:)/)[0]
  assert(body.includes('go test -race -v -count=1 -timeout=4m -tags destination_acceptance ./internal/app'))
  assert(body.includes('AdmissionObserverKeepsAuthenticationRetryAndSQL'))
})

test('real cohort canonical attribution rejects overlap and retains original routing and pressure gates', () => {
  const binding = readFileSync(new URL('../internal/app/destination_native_admission_binding_test.go', import.meta.url), 'utf8')
  for (const required of ['protocol.ValidateAuditObservation(*report.Audit)', 'sha256.Sum256(canonical)', 'owner.inflight != 1', 'c.events != 1', 'p.CandidateBytes != c.size', 'owner.panel != e.PanelID', 'p.OwnerBatches == 0 && p.Batches < p.BatchLimit', 'p.CandidateBytes > p.ByteLimit-p.Bytes', 'owner.digests[i] == c.digest', 'original.ServeHTTP(response, r)', 'nativeGlobalTransportReadError{err}', 'overlap_preserves_both_requests']) assert(binding.includes(required))
  assert.doesNotMatch(binding, /SetSnapshot\(|\.collector\.Ack\(|\.Offer\(|time\.Sleep\(/)
  const cohort = readFileSync(new URL('../internal/app/destination_native_global_cohort_test.go', import.meta.url), 'utf8')
  assert(cohort.includes('options.admission.observe') && cohort.includes('options.admission.handler(handler, peers)'))
  assert(cohort.indexOf('options.admission.handler(handler, peers)') < cohort.indexOf('options.transport(handler, peers)'))
  const driver = readFileSync(new URL('../internal/app/destination_native_global_batch_test.go', import.meta.url), 'utf8')
  assert(driver.includes('admission: admission') && driver.includes('admission.snapshot(peer.agent.AgentID)'))
  const workflow = readFileSync(new URL('../.github/workflows/test.yml', import.meta.url), 'utf8')
  assert(workflow.includes('AdmissionBindingRejectsAmbiguousEvidence'))
})
