import {
  IconBlock, IconCheck, IconCircle, IconError, IconExperiment, IconHelp, IconIdle, IconLock, IconPaused, IconPending, IconShield, IconShow,
  IconSuccess, IconUnlimited, IconVerified, IconWarning, type IconType,
} from '@/lib/icons'
import type { Tone } from '@/components/common/tones'
import type { AccessToneKind } from '@/utils/accessControl'

// THE ACCESS-CONTROL PAGES' CLOSED STATUS TABLE, in the new components' vocabulary: one row per
// MEANING (a synonym shares its row), each a tone from common/tones.ts plus its own icon, so no two
// rows are told apart by colour alone and none shares a (tone, icon) pair (accessStatus.test.ts holds
// that). utils/accessControl.ts keeps the old MUI table for the risk pages, which still read it.
//
// "Block" is a word, not an alarm: its tone is neutral and only the icon is drawn in the danger ink.

export interface AccessStatus {
  tone: Tone
  icon: IconType
  /** Icon colour override, for the rows whose icon carries more than the tone's own ink. */
  iconClass?: string
  /**
   * Shown as a small dot with plain words instead of a tinted badge: the state needs no attention
   * (ok, nothing recorded, paused). The meaning rows (block, allow, ...) keep their icon.
   */
  quiet: boolean
}

export const ACCESS_STATUS: Record<AccessToneKind, AccessStatus> = {
  block: { tone: 'neutral', icon: IconBlock, iconClass: 'text-destructive', quiet: false },
  allowlist_deny: { tone: 'neutral', icon: IconLock, iconClass: 'text-destructive', quiet: false },
  observe: { tone: 'info', icon: IconShow, quiet: false },
  would_deny: { tone: 'info', icon: IconExperiment, quiet: false },
  allow: { tone: 'neutral', icon: IconCheck, quiet: false },
  exempt: { tone: 'neutral', icon: IconVerified, quiet: false },
  allowlist_enforced: { tone: 'neutral', icon: IconShield, quiet: false },
  ok: { tone: 'success', icon: IconSuccess, quiet: true },
  measuring: { tone: 'info', icon: IconPending, quiet: false },
  attention: { tone: 'warning', icon: IconWarning, quiet: false },
  failing: { tone: 'danger', icon: IconError, quiet: false },
  paused: { tone: 'neutral', icon: IconPaused, quiet: true },
  not_applicable: { tone: 'neutral', icon: IconIdle, quiet: true },
  none: { tone: 'neutral', icon: IconCircle, quiet: true },
  unknown: { tone: 'neutral', icon: IconHelp, quiet: true },
  permanent: { tone: 'neutral', icon: IconUnlimited, quiet: false },
}

export function accessStatus(kind: AccessToneKind): AccessStatus {
  return ACCESS_STATUS[kind]
}
