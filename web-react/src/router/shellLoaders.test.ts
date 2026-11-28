import { beforeEach, describe, expect, it, vi } from 'vitest'

const i18n = vi.hoisted(() => ({ ensureNamespaces: vi.fn(async (_ns: readonly string[]) => {}) }))
vi.mock('@/i18n', () => i18n)
vi.mock('./AdminShellRoute', () => ({ default: 'admin-shell' }))
vi.mock('./UserShellRoute', () => ({ default: 'user-shell' }))
vi.mock('@/views/Enroll2FAView', () => ({ default: 'enroll' }))

import { loadAdminShell, loadEnroll2FA, loadUserShell } from './shellLoaders'

beforeEach(() => i18n.ensureNamespaces.mockClear())

describe('shell route loaders', () => {
  it('fetches admin with the admin shell, and only admin', async () => {
    expect((await loadAdminShell()).default).toBe('admin-shell')
    expect(i18n.ensureNamespaces.mock.calls).toEqual([[['admin']]])
  })

  it('fetches user with the user portal, and only user', async () => {
    expect((await loadUserShell()).default).toBe('user-shell')
    expect(i18n.ensureNamespaces.mock.calls).toEqual([[['user']]])
  })

  it('fetches user for the 2FA enrollment page, which sits outside both shells', async () => {
    expect((await loadEnroll2FA()).default).toBe('enroll')
    expect(i18n.ensureNamespaces.mock.calls).toEqual([[['user']]])
  })

  it('does not render the shell when its wording failed to load', async () => {
    i18n.ensureNamespaces.mockRejectedValueOnce(new Error('chunk 404'))
    await expect(loadAdminShell()).rejects.toThrow('chunk 404')
  })
})
