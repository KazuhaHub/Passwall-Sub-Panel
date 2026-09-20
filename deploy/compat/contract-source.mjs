// Prints the Passwall Node revision the pinned-source contract job must test.
//
// WHY THIS IS A FILE AND NOT A LINE IN THE WORKFLOW. The job used to resolve the
// source from PSP's go.mod (`go list -m -f '{{.Dir}}'`), which made "PSP speaks
// to a real released Node" a claim about whatever dependency PSP happened to
// pin that week. Two things followed from that: the evidence moved whenever a
// dependency bump happened, and removing the PN root module — which the plan
// wants — would have removed the test entry point along with it.
//
// The revision is a fact about what to test, so it lives with the other pinned
// revisions in docs/compat/verification-v1.json and is read from there.
//
// A COMMIT, NOT A TAG. A tag can be moved; a commit cannot. The job checks out
// the commit and asserts HEAD equals it, so a moved tag surfaces as a mismatch
// rather than as a silently different revision under test.
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const VERIFICATION = fileURLToPath(new URL('../../docs/compat/verification-v1.json', import.meta.url))

export function contractSource(verification) {
  const source = verification.contract_source
  if (!source || typeof source !== 'object') {
    throw new Error('verification-v1.json carries no contract_source; the pinned-source job has nothing to check out')
  }
  for (const field of ['tag', 'commit', 'module_path']) {
    if (typeof source[field] !== 'string' || source[field] === '') {
      throw new Error(`contract_source.${field} is missing`)
    }
  }
  if (!/^[0-9a-f]{40}$/.test(source.commit)) {
    throw new Error(`contract_source.commit is not a full commit SHA: ${source.commit}`)
  }
  // A MODULE PATH, NOT A URL. The job used to compose a fetch URL as
  // `https://github.com/${module_path}.git`, which is the doubled host
  // `github.com/github.com/...` for this value and a Not Found at the first
  // fetch — a failure two layers away from any diff. The composition moved into
  // repositoryURL below, and this refuses the input that caused it.
  if (source.module_path.includes('://')) {
    throw new Error(`contract_source.module_path must be a module path, not a URL: ${source.module_path}`)
  }
  if (!/^[a-z0-9.-]+\.[a-z]{2,}\/[^/\s]+\/[^/\s]+$/.test(source.module_path)) {
    throw new Error(`contract_source.module_path is not host and two path segments: ${source.module_path}`)
  }
  // A tag pinning a commit the manifest also claims must agree with the reviewed
  // pin for that tag; two places naming different revisions is worse than one.
  const reviewed = (verification.pinned_sources ?? {})[source.tag]
  if (reviewed && reviewed !== source.commit) {
    throw new Error(
      `contract_source says ${source.tag} is ${source.commit} but pinned_sources says ${reviewed}`,
    )
  }
  return source
}

// repositoryURL is the git remote the pinned-source job fetches from.
//
// IT IS THE MODULE PATH WITH A SCHEME, and nothing else: a Go module path on
// GitHub already begins with the host, so a URL is `https://<module_path>.git`.
// Adding the host again — which the workflow did — produces a URL GitHub answers
// with Not Found, and the job fails before it can test anything.
export function repositoryURL(source) {
  return `https://${source.module_path}.git`
}

function main() {
  const [command] = process.argv.slice(2)
  let source
  try {
    source = contractSource(JSON.parse(readFileSync(VERIFICATION, 'utf8')))
  } catch (error) {
    console.error(`contract-source: ${error.message}`)
    process.exit(2)
  }
  switch (command) {
    case '--commit':
      console.log(source.commit)
      break
    case '--tag':
      console.log(source.tag)
      break
    case '--module':
      console.log(source.module_path)
      break
    case '--repo-url':
      console.log(repositoryURL(source))
      break
    default:
      console.error('contract-source: expected one of --commit, --tag, --module, --repo-url')
      process.exit(2)
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) main()
