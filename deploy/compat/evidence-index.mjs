// Assembles the evidence index for one release candidate: what was claimed, by
// which source, against which exact artifacts, with which results.
//
// WHY THIS OUTLIVES THE RUN. Every other check here answers a question about one
// execution. A compatibility claim is read months later, by someone deciding
// whether a combination is covered — and by then the run's logs have expired and
// the artifacts have been replaced. The index is the record that survives, and it
// is what makes "the evidence does not support that claim" checkable rather than
// a matter of opinion.
//
// IT RECORDS, IT DOES NOT JUDGE. A case that failed is carried in the index with
// its verdict; whether a run may publish is the gate's question, answered
// elsewhere. Filtering failures out here would make every index look clean and
// leave the real outcome to be inferred from something that expires.
//
// Exit codes: 0 every planned case reported; 1 at least one did not (the index
// says which); 2 the index could not be assembled.
import { readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { basename, dirname, join } from 'node:path'

function parseArgs(argv) {
  const args = {}
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i]
    if (!flag.startsWith('--')) throw new Error(`unexpected argument: ${flag}`)
    const value = argv[++i]
    if (value === undefined) throw new Error(`missing value for ${flag}`)
    args[flag.slice(2)] = value
  }
  for (const required of ['evidence', 'plan', 'digests', 'source-sha', 'output']) {
    if (args[required] === undefined) throw new Error(`--${required} is required`)
  }
  return args
}

// everyResult finds each result.json under the reports tree and names the case
// after its directory, so the layout download-artifact produces works here
// without this script knowing about it.
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

function main() {
  let args
  try {
    args = parseArgs(process.argv.slice(2))
  } catch (err) {
    process.stderr.write(`evidence-index: ${err.message}\n`)
    process.exit(2)
  }

  let plan, digests
  try {
    plan = JSON.parse(readFileSync(args.plan, 'utf8'))
    digests = JSON.parse(readFileSync(args.digests, 'utf8'))
  } catch (err) {
    // An unreadable digest source must not become an empty artifact map: the
    // digests are what tie the claim to the exact bytes that were tested, and an
    // empty map reads as "no artifacts" rather than as "could not tell".
    process.stderr.write(`evidence-index: cannot read plan or digests: ${err.message}\n`)
    process.exit(2)
  }

  const files = everyResult(args.evidence)
  const cases = []
  const missing = []
  for (const entry of plan.cases ?? []) {
    const id = entry.id
    const path = files.get(id)
    if (path === undefined) {
      missing.push(id)
      cases.push({ id, verdict: null, reason: 'no result artifact was uploaded for this case' })
      continue
    }
    let report
    try {
      report = JSON.parse(readFileSync(path, 'utf8'))
    } catch {
      missing.push(id)
      cases.push({ id, verdict: null, reason: 'result artifact could not be read' })
      continue
    }
    cases.push({ id, verdict: report.verdict ?? null, reason: report.reason ?? null })
  }

  const index = {
    source_sha: args['source-sha'],
    revision: plan.revision ?? '',
    plan_verdict: plan.verdict ?? '',
    case_count: cases.length,
    cases,
    artifacts: digests,
    missing
  }

  try {
    writeFileSync(args.output, JSON.stringify(index, null, 2) + '\n')
  } catch (err) {
    process.stderr.write(`evidence-index: cannot write ${args.output}: ${err.message}\n`)
    process.exit(2)
  }

  if (missing.length > 0) {
    process.stderr.write(`evidence-index: ${missing.length} planned case(s) reported nothing\n`)
    for (const id of missing) process.stderr.write(`  missing: ${id}\n`)
    process.exit(1)
  }
}

main()
