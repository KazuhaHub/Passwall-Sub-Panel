import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'
import { checkDestinationTrialLoadEvidence } from './destination-trial-load.mjs'

const require = value => { if (!value) throw new Error('First-screen evidence invalid; private content withheld') }
const fields = (value, keys) => require(value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).sort().join() === [...keys].sort().join())
const count = value => Number.isSafeInteger(value) && value >= 0

export function checkDestinationTrialPages(input, duration) {
  require(typeof input === 'string' && Buffer.byteLength(input) <= 64 * 1024)
  const report = JSON.parse(input)
  fields(report, ['schema_version', 'window_ms', 'queries'])
  require(report.schema_version === 1 && count(duration) && duration >= 300000 && duration <= 305000 && report.window_ms === duration)
  require(Array.isArray(report.queries) && report.queries.length === 2)
  return report.queries.map((query, i) => {
    fields(query, ['name', 'samples'])
    require(query.name === ['hits_first_page', 'policies_overview'][i])
    require(Array.isArray(query.samples) && query.samples.length >= 100 && query.samples.length <= 149)
    let previous = -1
    for (const sample of query.samples) {
      fields(sample, ['elapsed_ms', 'duration_us', 'items', 'total'])
      require(Object.values(sample).every(count) && sample.elapsed_ms > previous)
      require(previous < 0 ? sample.elapsed_ms <= 2000 : sample.elapsed_ms - previous <= 5000)
      require(sample.elapsed_ms + sample.duration_us / 1000 <= duration)
      require(sample.total >= sample.items && (i === 0 ? sample.items <= 50 : sample.items === 2 && sample.total === 2))
      previous = sample.elapsed_ms
    }
    require(previous >= 280000 && (i !== 0 || query.samples.at(-1).total >= 20000))
    const durations = query.samples.map(s => s.duration_us).sort((a, b) => a - b)
    return { name: query.name, samples: durations.length, p95_ms: durations[Math.ceil(durations.length * 0.95) - 1] / 1000, last_total: query.samples.at(-1).total }
  })
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    require(process.argv.length === 5)
    const results = readFileSync(process.argv[2], 'utf8'), metrics = readFileSync(process.argv[3], 'utf8')
    checkDestinationTrialLoadEvidence(results, metrics)
    console.log(JSON.stringify(checkDestinationTrialPages(readFileSync(process.argv[4], 'utf8'), JSON.parse(metrics).duration_ms)))
  } catch {
    console.error('First-screen evidence failed; private content withheld')
    process.exitCode = 1
  }
}
