import { useTranslation } from 'react-i18next'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { discardSettingsCopy } from './confirmCopy'
// Mount only while an action owns its confirmation/write. Editors mount their
// own guard; keeping inactive blockers out of the router avoids competing guards.
export default function PendingActionGuard() {
  const { t } = useTranslation('admin')
  useLeaveGuard(false, discardSettingsCopy(t), undefined, true)
  return null
}
