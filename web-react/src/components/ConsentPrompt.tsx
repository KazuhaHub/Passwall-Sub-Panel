import { useMyProfile } from '@/query/me'
import type { QueryScope } from '@/query/session'
import ConsentDialog from './ConsentDialog'

export default function ConsentPrompt({ scope }: { scope: QueryScope }) {
  const profile = useMyProfile(scope)
  return <ConsentDialog pending={!!profile.data?.legal_pending} version={profile.data?.legal_consent_version ?? 0}
    sessionKey={`psp-legal-later-${scope.userId}-${scope.authEpoch}`} onRefresh={async () => { await profile.refetch({ throwOnError: true }) }} />
}
