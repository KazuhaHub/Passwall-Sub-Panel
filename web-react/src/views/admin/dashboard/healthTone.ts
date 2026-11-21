import type { Tone } from '@/components/common/tones'

/**
 * How a node's health state reads on the dashboard. The same split as the nodes list
 * (nodes/shared/statusOf): a node that cannot be reached, or whose inbound is gone, is a
 * failure; one the probe could not decide about, or whose inbound is switched off, is a
 * warning. `inconclusive` is never success: a fleet whose probe has gone blind must not
 * look healthy. The server only lists nodes that are not healthy, so `ok` is here for
 * completeness and an unknown or never-probed state is quiet.
 */
export function healthTone(state: string): Tone {
  switch (state) {
    case 'ok': return 'success'
    case 'unreachable':
    case 'panel_unreachable':
    case 'inbound_missing': return 'danger'
    case 'inconclusive':
    case 'inbound_disabled': return 'warning'
    default: return 'neutral'
  }
}
