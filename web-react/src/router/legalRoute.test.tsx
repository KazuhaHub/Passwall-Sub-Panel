// @vitest-environment jsdom
import { isValidElement } from 'react'
import { matchRoutes } from 'react-router'
import { afterAll, expect, it } from 'vitest'
import { router } from './index'
import RequireAuth from './RequireAuth'

afterAll(() => router.dispose())

it('mounts both legal documents outside the actual authentication branches', () => {
  for (const path of ['/legal/terms', '/legal/privacy']) {
    const matches = matchRoutes(router.routes, path)
    expect(matches?.at(-1)?.params.kind).toBe(path.split('/').at(-1))
    expect(matches?.some(({ route }) => isValidElement(route.element) && route.element.type === RequireAuth)).toBe(false)
  }
  // Guard against a fixture that accidentally omits all authentication.
  const userMatches = matchRoutes(router.routes, '/user/me')
  expect(userMatches?.some(({ route }) => isValidElement(route.element) && route.element.type === RequireAuth)).toBe(true)
})
