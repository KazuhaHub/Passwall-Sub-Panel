import { client } from './client'
import type { AuthLoginResponse } from './types'

export async function localLogin(username: string, password: string) {
  const { data } = await client.post<AuthLoginResponse>('/auth/local/login', {
    username,
    password,
  })
  return data
}

export function ssoLoginURL(returnTo: string = '/admin'): string {
  return `/api/auth/saml/login?return_to=${encodeURIComponent(returnTo)}`
}
