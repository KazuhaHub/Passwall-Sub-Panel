import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

test('global fixture preflight executes both integration boundaries without claiming a native cohort', () => {
  const workflow = readFileSync(new URL('../.github/workflows/test.yml', import.meta.url), 'utf8')
  const body = workflow.slice(workflow.indexOf('  global-fixture:')).split(/\r?\n(?=  sqlite:)/)[0]
  assert(body.startsWith('  global-fixture:'))
  assert.match(body, /runs-on: ubuntu-24\.04/)
  assert.match(body, /-count=1 -timeout=4m -tags destination_acceptance/)
  for (const root of ['AuditStartPreservesRealQueueAndPermissionChanges', 'PublicCoreCopyExcludesStateAndRejectsUntrustedFiles']) assert(body.includes(root))
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
