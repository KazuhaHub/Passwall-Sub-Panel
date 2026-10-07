import { isAxiosError } from 'axios'
import type { DestinationCategoriesView } from '@/api/accessControl'

// The latest read wins over cached metadata: a failed refresh must stop polling
// even when React Query retains the old catalog for category selection.
export function categoryRefreshState(data?: DestinationCategoriesView, error?: unknown): { refreshing: boolean; failed: boolean } {
  if (error) {
    if (isAxiosError(error) && error.response?.status === 503) {
      const state = error.response.data
      return { refreshing: state?.refreshing === true, failed: !!state?.last_error }
    }
    return { refreshing: false, failed: true }
  }
  return { refreshing: data?.refreshing === true, failed: !!data?.last_error }
}
