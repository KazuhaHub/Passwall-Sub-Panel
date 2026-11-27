import type { TFunction } from 'i18next'
import { rotateNativeCredential, type NativeServerProvisioning, type Server } from '@/api/servers'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/feedback/toasts'

/**
 * Rotating a Passwall Node's credential: the confirmation (Cancel focused, it invalidates the old credential), the
 * request, and the result in a toast. Returns the new provisioning, or null when the administrator declined or the
 * request failed (its message is already shown). Shared by the list's More menu and the installation page, which both
 * continue with the new credential.
 */
export async function rotateWithConfirm(t: TFunction, s: Server): Promise<NativeServerProvisioning | null> {
  const accepted = await confirm({
    title: t('admin:servers.native.rotate_title'),
    message: t('admin:servers.native.rotate_warning', { name: s.name }),
    destructive: true,
    confirmText: t('admin:servers.native.rotate_confirm'),
    initialFocus: 'cancel',
  })
  if (!accepted) return null
  try {
    const provisioned = await rotateNativeCredential(s.id)
    pushSnack(t('admin:servers.native.rotated'), 'success')
    return provisioned
  } catch (err) {
    pushSnack((err as { response?: { data?: { error?: string } }; message?: string }).response?.data?.error
      ?? (err as { message?: string }).message ?? 'unknown', 'error')
    return null
  }
}
