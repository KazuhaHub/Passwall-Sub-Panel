// Checks docs/compat/releases-v1.json against the other documents in this
// repository. Run by CI; also runnable by hand.
//
// WHY THIS IS NOT THE GO VALIDATOR AGAIN. The Go validator answers "is this
// document internally consistent". It cannot answer "does it agree with the
// manifest next to it", because that would mean reading two more files — and
// that is exactly where the drift happens. A policy is authored by hand while
// the manifest is edited on a different occasion for a different reason, so the
// two can disagree for a long time without either being malformed.
//
// The checks below are the disagreements that matter:
//
//   - the policy offers a release the Node manifest calls remote_upgrade
//     "unsupported". Two documents in one repository then say opposite things
//     about the same version, and the panel would offer an upgrade its own
//     manifest says cannot be done remotely.
//   - the policy offers a release that verification-v1.json does not pin. An
//     offer is a claim about a specific artefact; a version with no pinned
//     commit is a claim about whatever the tag points at today.
//   - the policy names a version the manifest has never heard of, which is a
//     typo far more often than it is a new release.
//
// Exit codes: 0 consistent; 1 inconsistent; 2 could not evaluate.
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const NODE_MANIFEST = fileURLToPath(new URL('../../docs/compat/node-v4.json', import.meta.url))
const VERIFICATION = fileURLToPath(new URL('../../docs/compat/verification-v1.json', import.meta.url))
const POLICY = fileURLToPath(new URL('../../docs/compat/releases-v1.json', import.meta.url))

export function checkReleasesPolicy({ policy, nodeManifest, verification }) {
  const problems = []

  const known = new Map()
  for (const node of nodeManifest.released_nodes ?? []) {
    known.set(node.version, node)
  }
  const pinned = new Set(Object.keys(verification.pinned_sources ?? {}))

  const refused = new Set((policy.refusals ?? []).map(r => r.version))
  const offered = new Set()

  for (const release of policy.releases ?? []) {
    offered.add(release.version)

    const node = known.get(release.version)
    if (!node) {
      problems.push(
        `${release.version} is offered but docs/compat/node-v4.json does not list it — the panel cannot identify a target it does not know`,
      )
      continue
    }
    if (node.remote_upgrade === 'unsupported') {
      problems.push(
        `${release.version} is offered as an upgrade target but node-v4.json records remote_upgrade=unsupported — two documents in this repository contradict each other`,
      )
    }
    if (!pinned.has(release.version)) {
      problems.push(
        `${release.version} is offered but verification-v1.json pins no commit for it — an offer must name an artefact, not a tag that moves`,
      )
    }
    if (refused.has(release.version)) {
      problems.push(`${release.version} is both offered and refused`)
    }
  }

  for (const version of refused) {
    if (!known.has(version)) {
      problems.push(`refusal ${version} names a version node-v4.json does not list — check for a typo`)
    }
  }

  return problems
}

function main() {
  let policy
  let nodeManifest
  let verification
  try {
    policy = JSON.parse(readFileSync(POLICY, 'utf8'))
    nodeManifest = JSON.parse(readFileSync(NODE_MANIFEST, 'utf8'))
    verification = JSON.parse(readFileSync(VERIFICATION, 'utf8'))
  } catch (error) {
    console.error(`check-releases-policy: cannot read the documents: ${error.message}`)
    process.exit(2)
  }

  const problems = checkReleasesPolicy({ policy, nodeManifest, verification })
  if (problems.length > 0) {
    console.error('check-releases-policy: the release policy disagrees with the rest of the repository:')
    for (const problem of problems) console.error(`  - ${problem}`)
    process.exit(1)
  }
  console.log('check-releases-policy: ok')
}

if (process.argv[1] === fileURLToPath(import.meta.url)) main()
