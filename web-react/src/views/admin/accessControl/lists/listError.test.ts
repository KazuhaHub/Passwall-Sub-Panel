import { describe, expect, it } from 'vitest'

import { listErrorKey } from './listError'

// The server stores a refresh failure as free text; the page maps the known
// shapes to words and shows nothing raw for the rest.
describe('listErrorKey', () => {
  it.each([
    ['fetch: HTTP 404', 'http', { code: '404' }],
    ['dest_list_too_large', 'too_large', {}],
    ['broad_entry: *.com', 'broad', { entry: '*.com' }],
    ['fetch: Get "https://x": dial tcp 10.0.0.1: connection refused', 'generic', {}],
    ['something <script> raw', 'generic', {}],
  ])('%s', (raw, key, params) => {
    expect(listErrorKey(raw)).toEqual({ key, params })
  })
})
