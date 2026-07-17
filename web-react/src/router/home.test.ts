import { describe, expect, it } from 'vitest'

import { homeForRole, isAdminOnlyPath, isAdminPath } from './home'

describe('route access helpers', () => {
  it('lands staff on the dashboard and users on their profile', () => {
    expect(homeForRole('admin')).toBe('/admin/dashboard')
    expect(homeForRole('operator')).toBe('/admin/dashboard')
    expect(homeForRole('user')).toBe('/user/me')
    expect(homeForRole('')).toBe('/user/me')
  })

  it('matches only the admin path namespace', () => {
    expect(isAdminPath('/admin')).toBe(true)
    expect(isAdminPath('/admin/users')).toBe(true)
    expect(isAdminPath('/administrator')).toBe(false)
    expect(isAdminPath('/user/me')).toBe(false)
  })

  it('keeps settings and server subroutes admin-only', () => {
    expect(isAdminOnlyPath('/admin/settings')).toBe(true)
    expect(isAdminOnlyPath('/admin/settings/security')).toBe(true)
    expect(isAdminOnlyPath('/admin/servers/1')).toBe(true)
    expect(isAdminOnlyPath('/admin/users')).toBe(false)
    expect(isAdminOnlyPath('/admin/settings-extra')).toBe(false)
  })
})
