import type { ConfirmOpts } from '@/components/ConfirmHost'

// THE CONFIRMATION CATALOGUE (plan §7.4 S20). Which actions ask, and what the
// asking says, in one place as pure functions of `t` and the numbers, so a
// test asserts every row. Anything not here does NOT ask: renaming, adding an
// exemption, toggling an existing policy, editing a list, reordering and a
// node's inbound rule are explained by the line beside the save button or by
// the page verdict ("saved, goes out in N seconds"), not by a second click.
// A title names its object as a question; the button is a verb, never "OK".

type T = (key: string, opts?: Record<string, unknown>) => string

const P = 'admin:access_control.confirm.'

/** `minutes` is `applyMinutes(status)`: null when the server did not say. */
function minutesWords(t: T, minutes: number | null): string {
  return minutes === null ? t(`${P}minutes_unknown`) : t(`${P}minutes`, { count: minutes })
}

function nodesWords(t: T, nodes: number | null): string {
  return nodes === null ? t(`${P}nodes_unknown`) : t(`${P}nodes`, { count: nodes })
}

export interface ConfirmArgs {
  t: T
  minutes: number | null
}

export function firstPublishConfirm({ t, minutes, nodes, seconds }: ConfirmArgs & {
  /** The nodes a change reaches; null when the status could not be read ("each node", never 0). */
  nodes: number | null
  /** The debounce window, from the settings; null when unknown. */
  seconds: number | null
}): ConfirmOpts {
  return {
    title: t(`${P}first_publish.title`),
    message: t(seconds === null ? `${P}first_publish.message_no_window` : `${P}first_publish.message`, {
      nodes: nodesWords(t, nodes), minutes: minutesWords(t, minutes), seconds,
    }),
    confirmText: t(`${P}first_publish.action`),
  }
}

export function deletePolicyConfirm({ t, minutes }: ConfirmArgs, name: string): ConfirmOpts {
  return {
    title: t(`${P}delete_policy.title`, { name }),
    message: t(`${P}delete_policy.message`, { minutes: minutesWords(t, minutes) }),
    confirmText: t(`${P}delete_policy.action`),
    destructive: true,
  }
}

/** An unused list: nothing to fear. A used one is not a confirmation but an explanation (the lists tab's in-use notice). */
export function deleteListConfirm(t: T, name: string): ConfirmOpts {
  return {
    title: t(`${P}delete_list.title`, { name }),
    message: t(`${P}delete_list.message`),
    confirmText: t(`${P}delete_list.action`),
    destructive: true,
  }
}

export function switchListTypeConfirm(t: T): ConfirmOpts {
  return {
    title: t(`${P}switch_list_type.title`),
    message: t(`${P}switch_list_type.message`),
    confirmText: t(`${P}switch_list_type.action`),
  }
}

export function removeExemptionConfirm({ t, minutes }: ConfirmArgs, upn: string): ConfirmOpts {
  return {
    title: t(`${P}remove_exemption.title`, { upn }),
    message: t(`${P}remove_exemption.message`, { minutes: minutesWords(t, minutes) }),
    confirmText: t(`${P}remove_exemption.action`),
  }
}

export function pauseConfirm({ t, minutes }: ConfirmArgs): ConfirmOpts {
  return {
    title: t(`${P}pause.title`),
    message: t(`${P}pause.message`, { minutes: minutesWords(t, minutes) }),
    confirmText: t(`${P}pause.action`),
    destructive: true,
  }
}

export function resumeConfirm({ t, minutes }: ConfirmArgs): ConfirmOpts {
  return {
    title: t(`${P}resume.title`),
    message: t(`${P}resume.message`, { minutes: minutesWords(t, minutes) }),
    confirmText: t(`${P}resume.action`),
  }
}

/** Saving a report-only policy as On: the Convert dialog's question, for the editor's own path to the same change. */
export function convertToBlockConfirm({ t, minutes }: ConfirmArgs, name: string): ConfirmOpts {
  return {
    title: t(`${P}convert_policy.title`, { name }),
    message: t(`${P}convert_policy.message`, { minutes: minutesWords(t, minutes) }),
    confirmText: t(`${P}convert_policy.action`),
  }
}

export function discardChangesConfirm(t: T): ConfirmOpts {
  return {
    title: t(`${P}discard.title`),
    message: t(`${P}discard.message`),
    confirmText: t(`${P}discard.action`),
    cancelText: t(`${P}discard.keep`),
  }
}

/** Shortening the hit retention deletes the older records within the hour, for good: the one retention change that asks. */
export function shortenRetentionConfirm(t: T, days: number): ConfirmOpts {
  return {
    title: t(`${P}shorten_retention.title`),
    message: t(`${P}shorten_retention.message`, { days }),
    confirmText: t(`${P}shorten_retention.action`),
    destructive: true,
  }
}
