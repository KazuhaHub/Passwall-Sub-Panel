import { client } from './client'

// EmailLog is one row from mail_sent joined with the recipient user.
// Surfaced under Logs → Email so the admin can verify that a renewal /
// expiry / traffic-low notification actually went out. Shape mirrors
// SubLog for consistency across the three log tabs.
export interface EmailLog {
  id: number
  user_id: number
  user_upn?: string
  user_display?: string
  to_email: string
  kind: string
  window_key: string
  sent_at: string
}

export interface EmailLogListResponse {
  items: EmailLog[]
  total: number
}

export interface EmailLogFilter {
  page?: number
  page_size?: number
  user_id?: number
  /** Case-insensitive substring across to_email / kind / upn / display. */
  search?: string
  since?: string
  until?: string
}

export async function getEmailLogs(filter: EmailLogFilter = {}) {
  const { data } = await client.get<EmailLogListResponse>('/admin/email-logs', { params: filter })
  return data
}

export async function clearEmailLogs() {
  await client.delete('/admin/email-logs')
}

export async function purgeEmailLogs() {
  const { data } = await client.post<{ deleted: number }>('/admin/email-logs/purge')
  return data
}
