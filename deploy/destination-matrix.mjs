import { lstatSync, readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import * as acceptance from './destination-acceptance.mjs'

const packageName = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
const require = condition => {
  if (!condition) throw new Error('Native matrix evidence is incomplete or unsafe; private content withheld')
}

export function destinationMatrixReports() {
  const reports = []
  const add = (name, parent, cases, check, file = 'destination-acceptance-tests.json') => reports.push({ name, parent, cases, check, file })
  for (const candidate of ['baseline', 'integrated', 'module']) for (const version of ['26.6.27', '26.7.28', '26.9.9']) {
    add(`native-destination-xray-${version}-${candidate}`, 'TestNativeDestinationTraffic', acceptance.destinationCases, acceptance.checkDestinationResults)
  }
  for (const candidate of ['baseline', 'guarded', 'integrated', 'module']) {
    const guarded = candidate !== 'baseline'
    add(`native-destination-sing-box-1.14.0-${candidate}`, guarded ? 'TestNativeGuardedSingBoxDestinationTraffic' : 'TestNativeSingBoxDestinationTraffic', guarded ? acceptance.guardedSingBoxCases : acceptance.singBoxCases, guarded ? acceptance.checkGuardedSingBoxResults : acceptance.checkSingBoxResults)
  }
  for (const database of ['mysql', 'postgres']) {
    for (const version of ['26.6.27', '26.7.28', '26.9.9']) add(`native-destination-sql-${database}-xray-${version}`, 'TestNativeDestinationTraffic', acceptance.destinationCases, acceptance.checkDestinationResults)
    add(`native-destination-sql-${database}-sing-box-1.14.0`, 'TestNativeGuardedSingBoxDestinationTraffic', acceptance.guardedSingBoxCases, acceptance.checkGuardedSingBoxResults)
  }
  add('native-legacy-upgrade', 'TestNativeLegacyPSPUpgrade', acceptance.legacyUpgradeCases, acceptance.checkLegacyUpgradeResults, 'destination-legacy-upgrade-tests.json')
  for (const database of ['mysql', 'postgres']) add(`native-legacy-upgrade-sql-${database}`, 'TestNativeLegacyPSPUpgrade', acceptance.legacyUpgradeCases, acceptance.checkLegacyUpgradeResults, 'destination-legacy-upgrade-tests.json')
  for (const database of ['sqlite', 'mysql', 'postgres']) add(`native-legacy-upgrade-module-${database}`, 'TestNativeLegacyPSPUpgrade', acceptance.legacyUpgradeCases, acceptance.checkLegacyUpgradeResults, 'destination-legacy-upgrade-tests.json')
  add('native-published-remote', 'TestNativePublishedRemoteRollback', acceptance.remoteUpgradeCases, acceptance.checkRemoteUpgradeResults, 'destination-remote-upgrade-tests.json')
  for (const architecture of ['amd64', 'arm64']) add(`native-published-docker-${architecture}`, 'TestNativePublishedDockerRollback', acceptance.remoteUpgradeCases, acceptance.checkDockerUpgradeResults, 'destination-docker-upgrade-tests.json')
  for (const database of ['sqlite', 'mysql', 'postgres']) for (const cohort of ['old', 'new', 'mixed']) add(`native-fleet-${database}-${cohort}`, 'TestNativeDestinationFleet', acceptance.fleetCases, acceptance.checkFleetResults, 'destination-fleet-tests.json')
  for (const database of ['sqlite', 'mysql', 'postgres']) for (const cohort of ['old', 'new', 'mixed']) add(`native-engine-fleet-${database}-${cohort}`, 'TestNativeDestinationFleet', acceptance.fleetCases, acceptance.checkFleetResults, 'destination-fleet-tests.json')
  return reports
}

export function checkDestinationMatrix(root, mergeSHA) {
  require(typeof mergeSHA === 'string' && /^[a-f0-9]{40}$/.test(mergeSHA))
  require(lstatSync(root).isDirectory() && !lstatSync(root).isSymbolicLink())
  const reports = destinationMatrixReports()
  const names = reports.map(report => `${report.name}-${mergeSHA}`).sort()
  require(JSON.stringify(readdirSync(root).sort()) === JSON.stringify(names))
  let cases = 0
  for (const report of reports) {
    const folder = join(root, `${report.name}-${mergeSHA}`)
    require(lstatSync(folder).isDirectory() && !lstatSync(folder).isSymbolicLink())
    require(JSON.stringify(readdirSync(folder)) === JSON.stringify([report.file]))
    const file = join(folder, report.file)
    require(lstatSync(file).isFile() && !lstatSync(file).isSymbolicLink())
    const input = readFileSync(file, 'utf8')
    report.check(input)
    const tests = new Set([report.parent, ...report.cases.map(name => `${report.parent}/${name}`)])
    for (const line of input.split(/\r?\n/).filter(line => line.trim())) {
      let event
      try { event = JSON.parse(line) } catch { require(false) }
      require(event && typeof event === 'object' && !Array.isArray(event))
      require(Object.keys(event).every(key => ['Action', 'Package', 'Test', 'Elapsed', 'Diagnostic', 'Observed'].includes(key)))
      require(event.Package === packageName && ['start', 'run', 'pass', 'output'].includes(event.Action))
      require(event.Test === undefined || tests.has(event.Test))
      require(event.Elapsed === undefined || typeof event.Elapsed === 'number' && Number.isFinite(event.Elapsed) && event.Elapsed >= 0)
      if (event.Action === 'output') {
        require(acceptance.isSafeDestinationDiagnostic(event))
      } else {
        require(event.Diagnostic === undefined && event.Observed === undefined)
      }
    }
    cases += report.cases.length
  }
  require(reports.length === 48 && cases === 861)
  return { reports: reports.length, cases }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const result = checkDestinationMatrix(process.argv[2], process.argv[3])
    console.log(`${result.reports} independent reports / ${result.cases} required cases passed`)
  } catch {
    console.error('Native matrix evidence failed; private content withheld')
    process.exitCode = 1
  }
}
