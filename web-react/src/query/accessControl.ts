import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getAccessControlSettings, putAccessControlSettings } from '@/api/accessControl'
import { accessControlKeys, settingsKeys } from './keys'
import type { QueryScope } from './session'

export function useAccessControlSettings(scope: QueryScope, enabled: boolean) {
  return useQuery({
    queryKey: accessControlKeys.settings(scope),
    queryFn: ({ signal }) => getAccessControlSettings({ signal, silent: true }),
    enabled, staleTime: 0, refetchOnWindowFocus: false,
  })
}

export function useSaveAccessControlSettings(scope: QueryScope) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: putAccessControlSettings,
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: accessControlKeys.settings(scope) })
      void client.invalidateQueries({ queryKey: accessControlKeys.status(scope) })
      void client.invalidateQueries({ queryKey: settingsKeys.ui(scope) })
    },
  })
}
