import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'

test('complete matrix gate rejects missing reports, mixed commits and unsafe event fields', async () => {
  const { destinationMatrixReports, checkDestinationMatrix } = await import('./destination-matrix.mjs')
  const source = await import('./destination-acceptance.mjs')
  const reports = destinationMatrixReports()
  assert.equal(reports.length, 25)
  assert.equal(new Set(reports.map(report => report.name)).size, 25)
  assert.equal(reports.reduce((total, report) => total + report.cases.length, 0), 611)
  const root = mkdtempSync(join(tmpdir(), 'psp-matrix-gate-'))
  const merge = 'a'.repeat(40)
  const packageName = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
  try {
    for (const report of reports) {
      const folder = join(root, `${report.name}-${merge}`)
      mkdirSync(folder)
      const names = [report.parent, ...report.cases.map(name => `${report.parent}/${name}`)]
      const events = names.flatMap(Test => [{ Action: 'run', Package: packageName, Test }, { Action: 'pass', Package: packageName, Test }])
      events.push({ Action: 'pass', Package: packageName })
      writeFileSync(join(folder, report.file), events.map(event => JSON.stringify(event)).join('\n') + '\n')
    }
    assert.deepEqual(checkDestinationMatrix(root, merge), { reports: 25, cases: 611 })
    const first = reports[0]
    const path = join(root, `${first.name}-${merge}`, first.file)
    const original = readFileSync(path, 'utf8')
    for (const extra of [
      { Action: 'output', Package: packageName, Output: 'private credential' },
      { Action: 'fail', Package: packageName },
      { Action: 'skip', Package: packageName, Test: first.parent },
      { Action: 'run', Package: packageName, Test: `${first.parent}/${first.cases[0]}` },
      { Action: 'run', Package: 'foreign/package', Test: first.parent },
      { Action: 'output', Package: packageName, Diagnostic: 'unknown-private-data', Observed: 'credential' },
      { Action: 'output', Package: packageName, Diagnostic: 'status_state', Observed: 'credential' },
      { Action: 'output', Package: packageName, Diagnostic: 'receipt_wait', Observed: 100 },
      { Action: 'output', Package: packageName, Diagnostic: 'panel_downgrade_application_ms', Observed: 150001 },
      { Action: 'output', Package: packageName, Diagnostic: 'status_state', Observed: true, Elapsed: -1 },
      { Action: 'output', Package: packageName, Diagnostic: 'status_state', Observed: true, Test: 'private/test' },
    ]) {
      writeFileSync(path, original + JSON.stringify(extra) + '\n')
      assert.throws(() => checkDestinationMatrix(root, merge))
    }
    const diagnostics = [
      { Diagnostic: 'status_state', Observed: false },
      { Diagnostic: 'receipt_wait', Observed: 1 },
      { Diagnostic: 'panel_downgrade_application_ms', Observed: 150000 },
    ].map(event => JSON.stringify({ Action: 'output', Package: packageName, ...event })).join('\n') + '\n'
    writeFileSync(path, original + diagnostics)
    assert.deepEqual(checkDestinationMatrix(root, merge), { reports: 25, cases: 611 })
    writeFileSync(path, original)
    assert.throws(() => checkDestinationMatrix(root, 'b'.repeat(40)))
    assert.throws(() => checkDestinationMatrix(root, '../foreign'))
    const missing = reports.find(report => report.name === 'native-destination-sql-mysql-xray-26.6.27')
    rmSync(join(root, `${missing.name}-${merge}`), { recursive: true })
    assert.throws(() => checkDestinationMatrix(root, merge))
    assert.deepEqual(reports.filter(report => report.parent === 'TestNativeDestinationTraffic').map(report => report.cases.length), Array(15).fill(source.destinationCases.length))
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})

test('workflow aggregate gate requires every producer and exact merge identity', () => {
  const workflow = readFileSync(new URL('../.github/workflows/destination-acceptance.yml', import.meta.url), 'utf8')
  const start = workflow.indexOf('  complete-matrix:')
  assert(start >= 0)
  const job = workflow.slice(start)
  for (const required of [
    'needs: [traffic, traffic-sql, published-remote, published-docker, legacy-upgrade]',
    'if: always()', "needs.traffic.result", 'needs.traffic-sql.result', 'needs.published-remote.result',
    'needs.published-docker.result', 'needs.legacy-upgrade.result', 'actions/download-artifact@v8',
    'pattern: native-*', 'destination-matrix.mjs', 'node --test deploy/destination_matrix_test.mjs', '${{ github.sha }}',
  ]) assert(job.includes(required), required)
  assert(!job.includes('continue-on-error'))
})
