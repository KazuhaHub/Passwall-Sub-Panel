import { client } from './client'

export interface ReconcileIssue {
  panel_name?: string
  client_email?: string
  code?: string
  detail?: string
  // fixed=true means reconcile already healed this issue on this run; the
  // entry is retained for traceability. UI should only label as
  // "unfixed" / "未修复" when fixed is false (or missing).
  fixed?: boolean
}

export interface ReconcileReport {
  scanned: number
  fixed: number
  issues: ReconcileIssue[]
}

export async function runReconcile(): Promise<ReconcileReport> {
  const { data } = await client.post<ReconcileReport>('/admin/reconcile/run')
  return data
}
