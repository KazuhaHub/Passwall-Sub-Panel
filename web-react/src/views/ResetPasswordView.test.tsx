// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import * as authApi from '@/api/auth'
import { renderAccess } from '@/test/accessControlHarness'
import ResetPasswordView from './ResetPasswordView'

vi.mock('@/api/auth', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/auth')>()
  return { ...real, getAuthMethods: vi.fn().mockResolvedValue({}), resetPassword: vi.fn() }
})

describe('the reset-password form', () => {
  it('submitting with the confirmation empty says so under it, focuses it and sends nothing', async () => {
    renderAccess(<ResetPasswordView />, { route: '/reset-password?token=t1', path: '/reset-password', role: 'user' })
    const pw = await screen.findByLabelText('新密码')
    expect(screen.queryByText('请再次输入密码')).toBeNull()
    fireEvent.change(pw, { target: { value: 'abcdefg1' } })
    fireEvent.click(screen.getByRole('button', { name: '重置密码' }))
    expect(await screen.findByText('请再次输入密码')).toBeTruthy()
    await waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText('确认密码')))
    expect(authApi.resetPassword).not.toHaveBeenCalled()
  })
})
