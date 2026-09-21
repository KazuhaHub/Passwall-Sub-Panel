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
  // A GO MAJOR-VERSION SUFFIX IS ALLOWED AND NOTHING ELSE IS. Go requires the path
  // of a module published at major N to end in `/vN`, so `.../passwall-node/v4` is a
  // module path; a fourth segment that is not that spelling is somebody's
  // subdirectory or a typo, and a repository URL composed from either fetches the
  // wrong place. The suffix is refused on its own too — `/v4/x` is not a shape this
  // project publishes.
  if (!/^[a-z0-9.-]+\.[a-z]{2,}\/[^/\s]+\/[^/\s]+(\/v[0-9]+)?$/.test(source.module_path)) {
    throw new Error(`contract_source.module_path is not a host, a repository and an optional major-version suffix: ${source.module_path}`)
  }
  // A tag pinning a commit the manifest also claims must agree with the reviewed
  // pin for that release; two places naming different revisions is worse than one.
  //
  // THE PIN IS LOOKED UP BY VERSION, NOT BY THE TAG IT ARRIVED AS. They were the
  // same string while the legacy scheme was the only one, so this read
  // `pinned_sources[tag]` and never had to say which it meant. A product tag is an
  // ADDRESS — `release/4.0.0` — and the pin is keyed by the VERSION it names, so
  // the lookup found nothing the moment the two diverged. That was worse than a
  // missing check: `if (reviewed && …)` treats an ABSENT pin as agreement, and
  // agreement is exactly what this is here to refuse. It now requires the pin to
  // exist and to match.
  const reviewed = (verification.pinned_sources ?? {})[versionOfTag(source.tag)]
  if (reviewed !== source.commit) {
    throw new Error(
      `contract_source says ${source.tag} is ${source.commit} but pinned_sources says ${reviewed ?? 'nothing'}`,
    )
  }
  return source
}

// versionOfTag is the release a tag names. The namespace is part of the ADDRESS,
// so it comes off — the same rule the Go side applies, written here because the
// two are in different languages and the tag is what this file is given.
//
// A TAG OUTSIDE THE NAMESPACE IS REFUSED rather than passed through: a pin is a
// claim about a release this project publishes, and a tag that is not in the
// namespace names no such release.
const TAG_NAMESPACE = 'release/'
function versionOfTag(tag) {
  if (!tag.startsWith(TAG_NAMESPACE)) {
    throw new Error(`contract_source.tag is not in the ${TAG_NAMESPACE} namespace: ${tag}`)
  }
  return tag.slice(TAG_NAMESPACE.length)
}

// A MAJOR-VERSION SUFFIX BELONGS TO THE MODULE, NOT TO THE REPOSITORY. Go appends
// `/vN` to the path of a module published at major N, and the repository it lives
// in is the path WITHOUT that suffix — so `.../passwall-node/v4` is cloned from
// `.../passwall-node`. The suffix is only ever the last segment and only ever `v`
// followed by digits.
const MAJOR_SUFFIX = /\/v[0-9]+$/

// repositoryURL is the git remote the pinned-source job fetches from.
//
// IT IS THE MODULE PATH WITH A SCHEME AND WITHOUT THE MAJOR SUFFIX. A Go module
// path on GitHub already begins with the host, so a URL is `https://<path>.git`;
// adding the host again — which the workflow did — produces a URL GitHub answers
// with Not Found. Composing it out of the FULL module path fails the same way for
// a v2+ module, and that failure is one more layer from the diff being blamed.
export function repositoryURL(source) {
  return `https://${source.module_path.replace(MAJOR_SUFFIX, '')}.git`
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
