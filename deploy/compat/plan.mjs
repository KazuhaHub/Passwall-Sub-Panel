// Builds the compatibility case plan: which cases exist, what identity each one
// runs against, and which are required.
//
// WHY A PLANNER AND NOT ANOTHER LIST. Everything downstream consumes this
// output — the release matrix, the per-case validator, the summary gate — so a
// mistake here is not a bug in one job, it is the same bug reproduced in every
// job that reads the plan.
//
// THE SUPPORTED VERSION LIST IS NOT IN THIS FILE, AND MUST NOT BE. It is sliced
// from docs/compat/node-v4.json by POSITION at min_supported. Restating it here
// would make the plan agree with itself while silently disagreeing with the
// manifest the panel ships — which is how a version stops being tested without
// anybody deciding it should.
//
// What this file adds instead is IDENTITY: which commit each tag pointed at when
// it was first resolved. A manifest version with no pinned identity fails the
// plan, rather than being tested against whatever the tag points at today.
//
// Exit codes: 0 a complete plan; 1 the inputs do not produce one (the output
// says which); 2 the planner could not evaluate its inputs at all.
import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const DEFAULTS = {
  manifest: fileURLToPath(new URL('../../docs/compat/node-v4.json', import.meta.url)),
  verification: fileURLToPath(new URL('../../docs/compat/verification-v1.json', import.meta.url)),
  profiles: fileURLToPath(new URL('profiles.json', import.meta.url))
}

const RELEASE_VERSION = /^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/
const SHA = /^[0-9a-f]{40}$/

function parseArgs(argv) {
  const args = { ...DEFAULTS }
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i]
    if (!flag.startsWith('--')) throw new Error(`unexpected argument: ${flag}`)
    const value = argv[++i]
    if (value === undefined) throw new Error(`missing value for ${flag}`)
    args[flag.slice(2)] = value
  }
  return args
}

function build(manifest, verification, profiles) {
  const problems = []

  const names = manifest.released_nodes.map((row) => row.version)
  const floor = names.indexOf(manifest.min_supported)
  if (floor < 0) problems.push(`min_supported ${JSON.stringify(manifest.min_supported)} names no row in released_nodes`)
  // POSITIONAL, never a version comparison: v0.0.1-beta9 sorts above
  // v0.0.1-beta11, so ordering this by version would invert the floor.
  const supported = floor < 0 ? [] : names.slice(floor)
  if (supported.length === 0) problems.push('the supported Node set is empty')

  const seen = new Set()
  for (const name of names) {
    if (seen.has(name)) problems.push(`released_nodes repeats ${name}`)
    seen.add(name)
  }

  // An exclusion is a decision, and a decision says why. Without the reason it
  // is indistinguishable from a version nobody got round to, and a known-bad
  // release hidden between two good ends of a range is exactly what explicit
  // exclusions exist to prevent.
  const exclusions = new Map()
  for (const entry of verification.excluded ?? []) {
    if (!entry.version) {
      problems.push('an exclusion names no version')
      continue
    }
    if (!entry.reason) {
      problems.push(`excluded ${entry.version} gives no reason`)
      continue
    }
    if (!supported.includes(entry.version)) {
      problems.push(`excluded ${entry.version} is not a supported version`)
      continue
    }
    exclusions.set(entry.version, entry.reason)
  }

  const profileNames = Object.keys(verification.profiles ?? {})
  if (profileNames.length === 0) problems.push('no profile is declared')
  for (const [name, spec] of Object.entries(verification.profiles ?? {})) {
    const ref = spec.expected_tests ?? ''
    const target = ref.slice(ref.indexOf('#') + 1)
    if (ref.indexOf('#') < 0 || profiles[target] === undefined) {
      problems.push(`profile ${name} references ${ref === '' ? 'nothing' : ref}, which the profile file does not define`)
    }
  }

  // The edge model has to exist before it can be populated, and an edge naming
  // only one end is not a checkable claim.
  for (const edge of verification.upgrade_edges ?? []) {
    const id = edge.id ?? '(no id)'
    if (!edge.id) problems.push('an upgrade edge has no id')
    if (!edge.from) problems.push(`upgrade edge ${id} has no source`)
    if (!edge.to) problems.push(`upgrade edge ${id} has no target`)
    if (edge.schema !== undefined && (edge.schema.from === undefined || edge.schema.to === undefined)) {
      problems.push(`upgrade edge ${id} declares a schema without both ends`)
    }
  }

  const pinned = verification.pinned_sources ?? {}
  const cases = []
  const ids = new Set()
  for (const name of [...profileNames].sort()) {
    const spec = verification.profiles[name]
    for (const version of supported) {
      if (exclusions.has(version)) continue
      if (!RELEASE_VERSION.test(version)) {
        problems.push(`${version} is not a release version`)
        continue
      }
      if (!SHA.test(pinned[version] ?? '')) {
        problems.push(`${version} has no pinned source SHA`)
        continue
      }
      const id = `${name}@${version}`
      if (ids.has(id)) {
        problems.push(`duplicate case id ${id}`)
        continue
      }
      ids.add(id)
      cases.push({
        id,
        profile: name,
        class: spec.class ?? 'required',
        direction: spec.direction ?? '',
        version,
        // The tag is a readable label; the SHA is the identity. Carrying only
        // the tag would let a moved tag silently substitute another commit.
        tag: version,
        sha: pinned[version],
        install_methods: spec.install_methods ?? [],
        platforms: spec.platforms ?? [],
        expected_tests: spec.expected_tests ?? ''
      })
    }
  }
  if (problems.length === 0 && !cases.some((entry) => entry.class === 'required')) {
    problems.push('no required case was planned')
  }

  return {
    problems,
    cases,
    excluded: [...exclusions].map(([version, reason]) => ({ version, reason }))
  }
}

function main() {
  let args
  try {
    args = parseArgs(process.argv.slice(2))
  } catch (err) {
    process.stderr.write(`plan: ${err.message}\n`)
    process.exit(2)
  }

  let manifest, verification, profiles
  try {
    manifest = JSON.parse(readFileSync(args.manifest, 'utf8'))
    verification = JSON.parse(readFileSync(args.verification, 'utf8'))
    profiles = JSON.parse(readFileSync(args.profiles, 'utf8'))
  } catch (err) {
    process.stderr.write(`plan: cannot read the inputs: ${err.message}\n`)
    process.exit(2)
  }

  const { problems, cases, excluded } = build(manifest, verification, profiles)
  const plan = {
    verdict: problems.length === 0 ? 'ok' : 'invalid',
    revision: verification.revision ?? '',
    problems,
    excluded,
    cases
  }

  if (args.emit !== undefined) {
    // Machine-readable fragments for the workflows. Nothing here is interpolated
    // into a shell command: the workflow reads a JSON array, never a string of
    // paths that a rename could turn into an argument.
    if (plan.verdict !== 'ok') {
      for (const problem of problems) process.stderr.write(`plan: ${problem}\n`)
      process.exit(1)
    }
    if (args.emit === 'versions') {
      process.stdout.write(JSON.stringify([...new Set(cases.map((entry) => entry.version))]) + '\n')
      return
    }
    if (args.emit === 'cases') {
      process.stdout.write(JSON.stringify(cases) + '\n')
      return
    }
    process.stderr.write(`plan: unknown --emit ${args.emit}\n`)
    process.exit(2)
  }

  try {
    writeFileSync(args.output, JSON.stringify(plan, null, 2) + '\n')
  } catch (err) {
    process.stderr.write(`plan: cannot write ${args.output}: ${err.message}\n`)
    process.exit(2)
  }

  if (plan.verdict === 'ok') return
  for (const problem of problems) process.stderr.write(`plan: ${problem}\n`)
  process.exit(1)
}

main()
