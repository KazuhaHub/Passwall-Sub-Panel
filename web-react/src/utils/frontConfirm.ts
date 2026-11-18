import { frontErrorOf, frontErrorWording } from '@/api/fronts'
import { confirm } from '@/components/ConfirmHost'
import i18n from '@/i18n'

/** What a save sends once the admin has agreed to what a shared port does. */
export interface FrontConfirmation {
  /** Joining a shared port: the loopback port the 409 offered (0 otherwise). */
  loopback_port?: number
  /** Always true: the admin agreed to what the 409 said. */
  confirm_front?: boolean
}

/**
 * Runs a node save and, when the server answers 409 `confirm_required` because
 * the change touches a shared port (a listener joining or leaving it, or a
 * fronted listener being disabled), asks the admin in the server's own words and
 * sends the same save again with the confirmation. Declining throws the
 * original refusal, so nothing is saved and nothing is changed silently.
 */
export async function withFrontConfirm<T>(run: (confirmation?: FrontConfirmation) => Promise<T>, held?: { confirmation?: FrontConfirmation }): Promise<T> {
  try {
    // `held` carries an agreement across calls: a caller that retries this whole
    // function (a tunnel confirmation around it) does not ask about the same shared
    // port twice. A stale one is refused again, and the admin is asked again.
    return await run(held?.confirmation)
  } catch (err) {
    const e = frontErrorOf(err)
    if (!e || e.status !== 409 || e.code !== 'confirm_required') throw err
    const action = String(e.params.action ?? '')
    if (!['join', 'disable', 'leave', 'move', 'enable'].includes(action)) throw err
    const ok = await confirm({
      title: i18n.t(`admin:servers.fronts.confirm.${action}_title`) as string,
      message: i18n.t(frontErrorWording(e).key, frontErrorWording(e).values) as string,
      destructive: true,
    })
    if (!ok) throw err
    const confirmation = { loopback_port: Number(e.params.loopback_port) || 0, confirm_front: true }
    if (held) held.confirmation = confirmation
    return run(confirmation)
  }
}
