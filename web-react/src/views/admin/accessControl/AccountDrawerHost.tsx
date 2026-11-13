import RiskUserDrawer from '@/views/admin/risk/drawer/RiskUserDrawer'

/**
 * The risk center's account drawer, opened from the access-control page: the
 * "access" host, landing on the access tab. It stacks above the page's own
 * sheets the way the risk drawer does everywhere (riskDrawerZIndex).
 */
export default function AccountDrawerHost({ userId, onClose }: { userId: number | null; onClose: () => void }) {
  return <RiskUserDrawer userId={userId} onClose={onClose} host="access" initialTab="access" />
}
