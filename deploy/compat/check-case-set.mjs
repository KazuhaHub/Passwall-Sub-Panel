// Judges whether the COMPLETE set of compatibility cases reported, rather than
// whether each report that happened to arrive is green.
//
// WHY THIS IS SEPARATE FROM check-go-results.mjs. That one judges a single case
// and answers "was this measured". This one answers "was everything measured",
// and the difference matters because the two failures look nothing alike: a
// cancelled matrix leg or a failed artifact upload leaves every report that DID
// arrive perfectly passing. A gate that only inspected the reports it found
// would call that complete.
//
// The expected set is DERIVED from the manifest and the profiles, never read
// back out of the reports directory. "Every report I found is fine" is a
// statement that is trivially true of an empty directory.
//
// Exit codes: 0 every expected case reported a pass; 1 not every case did (the
// result says which); 2 the checker could not evaluate.
import { readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { basename, dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const DEFAULT_MANIFEST = fileURLToPath(new URL('../../docs/compat/passwall-node-v4.json', import.meta.url))
const DEFAULT_PROFILES = fileURLToPath(new URL('profiles.json', import.meta.url))

function parseArgs(argv) {
  const args = { manifest: DEFAULT_MANIFEST, profiles: DEFAULT_PROFILES }
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i]
    if (!flag.startsWith('--')) throw new Error(`unexpected argument: ${flag}`)
    const value = argv[++i]
    if (value === undefined) throw new Error(`missing value for ${flag}`)
    args[flag.slice(2)] = value
  }
  for (const required of ['reports', 'output']) {
    if (args[required] === undefined) throw new Error(`--${required} is required`)
  }
  return args
}

// everyResult finds each result.json under the reports tree and names the case
// after its directory. Scanning rather than assuming a layout keeps the gate
// independent of how the artifacts were nested on the way here.
function everyResult(root) {
  const found = new Map()
  const walk = (dir) => {
    let entries
    try {
      entries = readdirSync(dir, { withFileTypes: true })
    } catch {
      return
    }
    for (const entry of entries) {
      const path = join(dir, entry.name)
      if (entry.isDirectory()) walk(path)
      else if (entry.name === 'result.json') found.set(basename(dirname(path)), path)
    }
  }
  walk(root)
  return found
}

// expectedCases is profile x supported version. The version list comes from the
// manifest's min_supported floor, matched by POSITION — never by comparing
// version strings, because v0.0.1-beta11 sorts below v0.0.1-beta9.
function expectedCases(manifest, profiles) {
  const names = manifest.released_nodes.map((row) => row.version)
  const floor = names.indexOf(manifest.min_supported)
  if (floor < 0) {
    throw new Error(`min_supported ${JSON.stringify(manifest.min_supported)} names no row in released_nodes`)
  }
  const supported = names.slice(floor)
  if (supported.length === 0) throw new Error('the supported Node set is empty')
  const cases = []
  for (const profile of Object.keys(profiles)) {
    for (const version of supported) cases.push(`${profile}@${version}`)
  }
  return cases
}

function main() {
  let args
  try {
    args = parseArgs(process.argv.slice(2))
  } catch (err) {
    process.stderr.write(`check-case-set: ${err.message}\n`)
    process.exit(2)
  }

  let expected
  try {
    expected = expectedCases(
      JSON.parse(readFileSync(args.manifest, 'utf8')),
      JSON.parse(readFileSync(args.profiles, 'utf8'))
    )
  } catch (err) {
    process.stderr.write(`check-case-set: cannot derive the expected case set: ${err.message}\n`)
    process.exit(2)
  }

  try {
    statSync(args.reports)
  } catch (err) {
    // A reports directory that does not exist is indistinguishable from a run
    // where nothing was uploaded. Both are "not one case reported".
    process.stderr.write(`check-case-set: cannot read ${args.reports}: ${err.message}\n`)
  }

  const found = everyResult(args.reports)
  const missing = []
  const notPassing = []
  for (const id of expected) {
    const path = found.get(id)
    if (path === undefined) {
      missing.push(id)
      continue
    }
    let report
    try {
      report = JSON.parse(readFileSync(path, 'utf8'))
    } catch {
      // An unreadable report is not evidence of anything, so it counts as not
      // having reported at all rather than as a case that ran.
      missing.push(id)
      continue
    }
    const verdict = report?.verdict
    if (verdict !== 'pass') notPassing.push({ case: id, verdict: verdict ?? 'no verdict' })
  }

  // Evidence for cases nobody asked about is recorded rather than rejected: a
  // name that drifted between the matrix and the profiles shows up here first,
  // and that is worth seeing even though it is not itself a failure.
  const wanted = new Set(expected)
  const unexpected = [...found.keys()].filter((id) => !wanted.has(id)).sort()

  const verdict = missing.length === 0 && notPassing.length === 0 ? 'pass' : 'incomplete'
  const result = {
    verdict,
    expected,
    reported: expected.filter((id) => !missing.includes(id)),
    missing,
    notPassing,
    unexpected
  }

  // Written even on failure: the missing list is the whole useful output, and a
  // gate that says only "failed" leaves the reader with nowhere to go.
  try {
    writeFileSync(args.output, JSON.stringify(result, null, 2) + '\n')
  } catch (err) {
    process.stderr.write(`check-case-set: cannot write ${args.output}: ${err.message}\n`)
    process.exit(2)
  }

  if (verdict === 'pass') return
  process.stderr.write(
    `check-case-set: ${missing.length} case(s) did not report and ${notPassing.length} did not pass\n`)
  for (const id of missing) process.stderr.write(`  missing:     ${id}\n`)
  for (const entry of notPassing) process.stderr.write(`  not passing: ${entry.case} (${entry.verdict})\n`)
  process.exit(1)
}

main()
