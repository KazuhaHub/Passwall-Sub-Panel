import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

import { contractSource, repositoryURL } from './contract-source.mjs'

const verification = fileURLToPath(new URL('../../docs/compat/verification-v1.json', import.meta.url))
const read = () => JSON.parse(readFileSync(verification, 'utf8'))

test('the shipped manifest names a full commit for the contract job', () => {
  const source = contractSource(read())
  assert.match(source.commit, /^[0-9a-f]{40}$/)
  assert.equal(source.module_path, 'github.com/KazuhaHub/passwall-node')
})

test('a missing contract_source is refused rather than defaulted', () => {
  // Defaulting would put the job back on whatever go.mod says, which is the
  // coupling this file exists to remove.
  const doc = read()
  delete doc.contract_source
  assert.throws(() => contractSource(doc), /no contract_source/)
})

test('a tag instead of a commit is refused', () => {
  // A tag moves; the job checks out a commit and asserts HEAD equals it.
  const doc = read()
  doc.contract_source.commit = 'v0.0.1-beta11'
  assert.throws(() => contractSource(doc), /not a full commit SHA/)
})

test('a short commit is refused', () => {
  const doc = read()
  doc.contract_source.commit = '60d9649'
  assert.throws(() => contractSource(doc), /not a full commit SHA/)
})

test('a missing field is refused', () => {
  const doc = read()
  delete doc.contract_source.module_path
  assert.throws(() => contractSource(doc), /module_path is missing/)
})

test('two places naming different revisions for one tag are refused', () => {
  // The manifest already records what each tag resolved to. If contract_source
  // disagreed, one of the two would be wrong and nothing would say which.
  const doc = read()
  doc.contract_source.commit = '0'.repeat(40)
  assert.throws(() => contractSource(doc), /pinned_sources says/)
})

// THE JOB FETCHES FROM THIS URL, and it used to compose it as
// `https://github.com/${module_path}.git` while module_path is a GO MODULE PATH
// that already begins with a host. The result was
// `https://github.com/github.com/KazuhaHub/passwall-node.git`, which GitHub
// answers with Not Found — so the pinned-source contract job failed at its first
// fetch, for a reason two layers away from the diff that was being blamed.
//
// The composition lives here now, where it is one line with a test, rather than
// in the workflow where the mistake was invisible.
test('the repository URL is the module path, not a host plus the module path', () => {
  const url = repositoryURL(contractSource(read()))
  assert.equal(url, 'https://github.com/KazuhaHub/passwall-node.git')
  // The doubled form is spelled out because that is the specific wrong answer.
  assert.ok(!url.includes('github.com/github.com'), url)
  assert.equal(url.match(/^https:\/\//g).length, 1)
})

test('a module path that already carries a scheme is refused', () => {
  // Whoever wrote this meant a URL, and composing one from it would produce a
  // second scheme rather than an error.
  const doc = read()
  doc.contract_source.module_path = 'https://github.com/KazuhaHub/passwall-node'
  assert.throws(() => contractSource(doc), /module_path must be a module path/)
})

test('a module path that is not host and two segments is refused', () => {
  for (const bad of ['passwall-node', 'github.com/passwall-node', 'github.com/a/b/c', '']) {
    const doc = read()
    doc.contract_source.module_path = bad
    assert.throws(() => contractSource(doc), /module_path/, bad)
  }
})

// A MAJOR-VERSION SUFFIX IS PART OF THE MODULE PATH AND NOT PART OF THE
// REPOSITORY.
//
// Go requires the path of a module published at major N to end in `/vN`, so
// Passwall Node's module path becomes `github.com/KazuhaHub/passwall-node/v4` —
// and the REPOSITORY it lives in is still `github.com/KazuhaHub/passwall-node`.
// Appending the whole path produced `.../passwall-node/v4.git`, which GitHub
// answers with Not Found: the same failure the doubled host used to produce, one
// layer further from the diff that would be blamed for it.
test('the repository URL drops the major-version suffix from the module path', () => {
  assert.equal(
    repositoryURL({ module_path: 'github.com/KazuhaHub/passwall-node/v4' }),
    'https://github.com/KazuhaHub/passwall-node.git',
  )
  // AND A PATH WITHOUT ONE IS UNCHANGED. Both shapes are in the wild: the
  // protocol module is published at v0, where the suffix is not allowed at all.
  assert.equal(
    repositoryURL({ module_path: 'github.com/KazuhaHub/passwall-node' }),
    'https://github.com/KazuhaHub/passwall-node.git',
  )
})

test('a module path may end in a major-version suffix', () => {
  const doc = read()
  doc.contract_source.module_path = 'github.com/KazuhaHub/passwall-node/v4'
  assert.equal(contractSource(doc).module_path, 'github.com/KazuhaHub/passwall-node/v4')
})

test('a module path is a host, a repository and an optional major suffix and no more', () => {
  // `v4` is the only extra segment allowed, and only in that spelling: a Go major
  // suffix is `v` followed by digits. Anything else is somebody's subdirectory or a
  // typo, and composing a repository URL out of either fetches the wrong place.
  for (const bad of ['github.com/a/b/c', 'github.com/a/b/v', 'github.com/a/b/v4/x', 'github.com/a/b/V4']) {
    const doc = read()
    doc.contract_source.module_path = bad
    assert.throws(() => contractSource(doc), /module_path/, bad)
  }
  for (const good of ['github.com/a/b', 'github.com/a/b/v4', 'github.com/a/b/v12']) {
    const doc = read()
    doc.contract_source.module_path = good
    assert.equal(contractSource(doc).module_path, good)
  }
})
