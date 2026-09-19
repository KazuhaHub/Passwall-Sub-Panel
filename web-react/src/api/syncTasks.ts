import { client } from './client'
import type { ListResponse, SyncTask, SyncTaskStatus, SyncTaskType } from './types'

export interface SyncTaskListParams {
  page?: number
  page_size?: number
  status?: SyncTaskStatus
  type?: SyncTaskType
}

export async function listSyncTasks(params: SyncTaskListParams = {}, opts: { signal?: AbortSignal } = {}) {
  const { data } = await client.get<ListResponse<SyncTask>>('/admin/sync-tasks', {
    params,
    signal: opts.signal,
  })
  return data
}

export async function retrySyncTask(id: number) {
  await client.post(`/admin/sync-tasks/${id}/retry`)
}

export async function cancelSyncTask(id: number) {
  await client.post(`/admin/sync-tasks/${id}/cancel`)
}

export async function purgeFinishedSyncTasks() {
  const { data } = await client.post<{ deleted: number }>('/admin/sync-tasks/purge')
  return data
}
