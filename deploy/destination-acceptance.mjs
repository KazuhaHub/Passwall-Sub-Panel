import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'

export const nodeCandidate = Object.freeze({ repository: 'KazuhaHub/Passwall-Node', commit: '63c60f6d5e8cd093bbaa8385f5b514dbe2f40390' })
export const destinationCases = Object.freeze(['empty', 'watch', 'block', 'paused', 'trial', 'enforce', 'extra', 'collection_off', 'usage'])
const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
const names = ['TestNativeDestinationTraffic', ...destinationCases.map(name => `TestNativeDestinationTraffic/${name}`)]
function parseEvent(line) {
  try { return JSON.parse(line) } catch { throw new Error('Malformed test execution stream; content withheld') }
}
export function sanitizeDestinationResults(input) {
  return input.split(/\r?\n/).filter(line => line.trim()).map(line => {
    const event = parseEvent(line)
    if (event.Action === 'output') return ''
    if (!['start', 'run', 'pause', 'cont', 'pass', 'bench', 'fail', 'skip'].includes(event.Action)) throw new Error('Unrecognized test event; content withheld')
    const result = { Action: event.Action, Package: event.Package === pkg ? pkg : '[other package]' }
    if (names.includes(event.Test)) result.Test = event.Test
    if (typeof event.Elapsed === 'number' && Number.isFinite(event.Elapsed)) result.Elapsed = event.Elapsed
    return JSON.stringify(result)
  }).filter(Boolean).join('\n') + '\n'
}
export function checkDestinationResults(input) {
  const runs = new Map(), passes = new Map()
  let packagePasses = 0
  for (const line of input.split(/\r?\n/).filter(line => line.trim())) {
    const event = parseEvent(line)
    if (event.Action === 'fail' || event.Action === 'skip') throw new Error('Acceptance failed or skipped; private output withheld')
    if (event.Package !== pkg) continue
    if (!event.Test && event.Action === 'pass') packagePasses++
    if (!names.includes(event.Test)) continue
    if (event.Action === 'run') runs.set(event.Test, (runs.get(event.Test) ?? 0) + 1)
    if (event.Action === 'pass') {
      if (runs.get(event.Test) !== 1) throw new Error('Acceptance pass has no unique execution')
      passes.set(event.Test, (passes.get(event.Test) ?? 0) + 1)
    }
  }
  if (packagePasses !== 1 || names.some(name => runs.get(name) !== 1 || passes.get(name) !== 1)) throw new Error('Acceptance did not execute every required case exactly once')
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  if (process.argv[2] === 'sanitize' && process.argv.length === 3) {
    process.stdout.write(sanitizeDestinationResults(readFileSync(0, 'utf8')))
  } else {
    if (process.argv[2] !== 'check' || process.argv.length !== 4) throw new Error('Usage: destination-acceptance.mjs check RESULTS')
    checkDestinationResults(readFileSync(process.argv[3], 'utf8'))
    console.log('Every native destination acceptance case ran and passed')
  }
}
