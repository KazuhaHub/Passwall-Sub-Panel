import assert from 'node:assert/strict'
import { test } from 'node:test'
import { verifyImageTagMissing } from './check-image.mjs'

const repository = 'kazuhahub/passwall-sub-panel'
const tag = 'v3.9.2'
const token = 'anonymous-test-token-do-not-log'
const privateBody = 'private-response-body-do-not-log'
const json = (body, status = 200) => new Response(JSON.stringify(body), {
  status, headers: { 'Content-Type': 'application/json; charset=utf-8' },
})

function fixture(manifestResponse, tokenResponse = json({ token })) {
  const calls = []
  const fetchImpl = async (url, options) => {
    calls.push({ url: String(url), options })
    return calls.length === 1 ? tokenResponse : manifestResponse
  }
  return { calls, fetchImpl }
}

async function rejectsRedacted(fetchImpl) {
  await assert.rejects(verifyImageTagMissing(repository, tag, fetchImpl), error => {
    assert(!error.message.includes(token))
    assert(!error.message.includes(privateBody))
    return true
  })
}

test('only a JSON MANIFEST_UNKNOWN allows missing, using anonymous repository pull scope', async () => {
  const { calls, fetchImpl } = fixture(json({ errors: [{ code: 'MANIFEST_UNKNOWN', message: privateBody }] }, 404))
  await verifyImageTagMissing(repository, tag, fetchImpl)
  assert.equal(calls.length, 2)
  const tokenURL = new URL(calls[0].url)
  assert.equal(tokenURL.origin, 'https://ghcr.io')
  assert.equal(tokenURL.pathname, '/token')
  assert.equal(tokenURL.searchParams.get('service'), 'ghcr.io')
  assert.equal(tokenURL.searchParams.get('scope'), `repository:${repository}:pull`)
  assert.equal(calls[0].options.headers.Authorization, undefined)
  assert.equal(calls[1].url, `https://ghcr.io/v2/${repository}/manifests/${tag}`)
  assert.equal(calls[1].options.headers.Authorization, `Bearer ${token}`)
  for (const call of calls) {
    assert.equal(call.options.method, 'GET')
    assert.equal(call.options.redirect, 'error')
    assert(call.options.signal instanceof AbortSignal)
    assert(!call.url.includes(token))
  }
})

test('an existing exact manifest refuses publication without reading its body', async () => {
  const response = json({ private: privateBody })
  const { fetchImpl } = fixture(response)
  await rejectsRedacted(fetchImpl)
  assert.equal(response.bodyUsed, false)
})

for (const status of [401, 403, 429, 500, 503]) {
  test(`manifest ${status} fails closed, including changed package visibility`, async () => {
    const { fetchImpl } = fixture(json({ errors: [{ code: 'DENIED', message: privateBody }] }, status))
    await rejectsRedacted(fetchImpl)
  })
}

for (const status of [401, 403, 404, 429, 500]) {
  test(`anonymous token ${status} fails before manifest access`, async () => {
    const { calls, fetchImpl } = fixture(json({ errors: [{ code: 'MANIFEST_UNKNOWN' }] }, 404), json({ error: privateBody }, status))
    await rejectsRedacted(fetchImpl)
    assert.equal(calls.length, 1)
  })
}

for (const [index, body] of [null, {}, { token: '' }, { token: 12 }, { token: `${token}\r\n${privateBody}` }, { token: '=' }, { token: 'x'.repeat(8193) }].entries()) {
  test(`invalid token evidence case ${index + 1} fails closed`, async () => {
    const { calls, fetchImpl } = fixture(json({ errors: [{ code: 'MANIFEST_UNKNOWN' }] }, 404), json(body))
    await rejectsRedacted(fetchImpl)
    assert.equal(calls.length, 1)
  })
}

for (const [index, evidence] of [
  null, {}, { errors: [] }, { code: 'MANIFEST_UNKNOWN' },
  { errors: [{ code: 'NAME_UNKNOWN' }] }, { errors: [{ code: 'DENIED', message: privateBody }] },
  { errors: [{ code: 'MANIFEST_UNKNOWN' }, { code: 'DENIED' }] }, { errors: [null] },
].entries()) {
  test(`ambiguous/non-manifest 404 case ${index + 1} fails closed`, async () => {
    await rejectsRedacted(fixture(json(evidence, 404)).fetchImpl)
  })
}

test('HTML/malformed/non-JSON 404 responses cannot become missing evidence', async () => {
  for (const response of [
    new Response(privateBody, { status: 404, headers: { 'Content-Type': 'application/json' } }),
    new Response('<html>not found</html>', { status: 404, headers: { 'Content-Type': 'text/html' } }),
    new Response('{"errors":[{"code":"MANIFEST_UNKNOWN"}]}', { status: 404, headers: { 'Content-Type': 'text/plain' } }),
  ]) await rejectsRedacted(fixture(response).fetchImpl)
})

test('malformed token JSON is redacted and prevents manifest access', async () => {
  const { calls, fetchImpl } = fixture(json({}, 404), new Response(privateBody, { headers: { 'Content-Type': 'application/json' } }))
  await rejectsRedacted(fetchImpl)
  assert.equal(calls.length, 1)
})

test('token and manifest redirects are never followed or interpreted as absence', async () => {
  for (const redirectStage of [1, 2]) {
    let calls = 0
    const fetchImpl = async (_url, options) => {
      calls++
      assert.equal(options.redirect, 'error')
      if (calls === redirectStage) throw new Error(`${token}: ${privateBody}`)
      return json({ token })
    }
    await rejectsRedacted(fetchImpl)
    assert.equal(calls, redirectStage)
  }
  await rejectsRedacted(fixture(new Response(null, { status: 302 })).fetchImpl)
  const redirected = json({ errors: [{ code: 'MANIFEST_UNKNOWN' }] }, 404)
  Object.defineProperty(redirected, 'redirected', { value: true })
  await rejectsRedacted(fixture(redirected).fetchImpl)
})

test('invalid repository/tag coordinates are rejected before any network request', async () => {
  for (const [candidateRepository, candidateTag] of [
    ['KazuhaHub/passwall-sub-panel', tag], ['kazuhahub/foreign-package', tag],
    ['kazuhahub/passwall-sub-panel/extra', tag], ['kazuhahub@evil.test/passwall-sub-panel', tag],
    ['../passwall-sub-panel', tag], ['owner_name/passwall-sub-panel', tag], ['a'.repeat(40) + '/passwall-sub-panel', tag],
    [repository, 'latest'], [repository, 'beta'], [repository, ''], [repository, 'v1?token=secret'],
    [repository, 'v1/another'], [repository, 'v1%2Fanother'], [repository, 'v1\nsecret'], [repository, 'v' + '1'.repeat(128)],
  ]) {
    let calls = 0
    await assert.rejects(verifyImageTagMissing(candidateRepository, candidateTag, async () => { calls++; return json({}) }))
    assert.equal(calls, 0)
  }
})
