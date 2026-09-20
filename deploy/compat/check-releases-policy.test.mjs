import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

import { checkReleasesPolicy } from './check-releases-policy.mjs'

const policy = fileURLToPath(new URL('../../docs/compat/releases-v1.json', import.meta.url))
const nodeManifest = fileURLToPath(new URL('../../docs/compat/node-v4.json', import.meta.url))
const verification = fileURLToPath(new URL('../../docs/compat/verification-v1.json', import.meta.url))

const read = path => JSON.parse(readFileSync(path, 'utf8'))

// Each case mutates ONE thing, so a failure names the rule it broke rather than
// the first rule that happened to fire.
function documents({ policyEdit, nodeEdit, verificationEdit } = {}) {
  const p = read(policy)
  const n = read(nodeManifest)
  const v = read(verification)
  if (policyEdit) policyEdit(p)
  if (nodeEdit) nodeEdit(n)
  if (verificationEdit) verificationEdit(v)
  return { policy: p, nodeManifest: n, verification: v }
}

test('the shipped documents agree', () => {
  assert.deepEqual(checkReleasesPolicy(documents()), [])
})

test('offering a release the manifest calls remote_upgrade unsupported is refused', () => {
  // Two documents in one repository, saying opposite things about one version.
  const problems = checkReleasesPolicy(
    documents({
      nodeEdit: n => {
        n.released_nodes.find(node => node.version === 'v0.0.1-beta11').remote_upgrade = 'unsupported'
      },
    }),
  )
  assert.equal(problems.length, 1)
  assert.match(problems[0], /remote_upgrade=unsupported/)
})

test('offering a release with no pinned commit is refused', () => {
  // An offer is a claim about an artefact. Without a pin it is a claim about
  // whatever the tag points at today.
  const problems = checkReleasesPolicy(
    documents({
      verificationEdit: v => {
        delete v.pinned_sources['v0.0.1-beta11']
      },
    }),
  )
  assert.equal(problems.length, 1)
  assert.match(problems[0], /pins no commit/)
})

test('offering a version the manifest has never heard of is refused', () => {
  // Almost always a typo, and the panel could not identify the target.
  const problems = checkReleasesPolicy(
    documents({
      policyEdit: p => {
        p.releases[0].version = 'v0.0.1-beta99'
        p.releases[0].release_tag = 'v0.0.1-beta99'
      },
    }),
  )
  assert.ok(problems.some(p => /does not list it/.test(p)), problems.join('; '))
})

test('a refusal naming an unknown version is refused', () => {
  const problems = checkReleasesPolicy(
    documents({
      policyEdit: p => {
        p.refusals.push({ version: 'v0.0.1-beta404', reason: 'typo' })
      },
    }),
  )
  assert.equal(problems.length, 1)
  assert.match(problems[0], /check for a typo/)
})

test('a version that is both offered and refused is refused', () => {
  const problems = checkReleasesPolicy(
    documents({
      policyEdit: p => {
        p.refusals.push({ version: 'v0.0.1-beta10', reason: 'contradiction' })
      },
    }),
  )
  assert.ok(problems.some(p => /both offered and refused/.test(p)), problems.join('; '))
})
