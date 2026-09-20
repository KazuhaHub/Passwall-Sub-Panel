import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

import { contractSource } from './contract-source.mjs'

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
