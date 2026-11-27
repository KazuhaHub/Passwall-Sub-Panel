import { useEffect, useRef, useState } from 'react'
import { IconCheck, IconCopy } from '@/lib/icons'

import { useSpacedT } from '@/i18n/spacing'
import { cn } from '@/lib/utils'
import { copyToClipboard } from '@/utils/clipboard'
import { IconButton } from './IconButton'

const CONFIRM_MS = 1500

// Click to copy, with the confirmation in the control itself (plan 3.2): the icon
// turns into a check and the tooltip says "已复制" for a moment, and a visually
// hidden status line tells a screen reader. A failed copy toasts (the shared util
// does that), since nothing in place would say so.
function useCopy(value: string) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  async function copy() {
    const ok = await copyToClipboard(value, { silent: true })
    if (!ok) return
    setCopied(true)
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setCopied(false), CONFIRM_MS)
  }
  return { copied, copy }
}

export function CopyButton({ value, name, size = 'icon-sm', side, className }: {
  value: string
  /** What is being copied ("订阅链接"); makes the button's name specific. */
  name?: string
  size?: 'icon' | 'icon-sm' | 'icon-xs'
  /** Where the tooltip opens; inside a form field pass 'top' so it does not cover the help text below. */
  side?: 'top' | 'right' | 'bottom' | 'left'
  className?: string
}) {
  const { t } = useSpacedT()
  const { copied, copy } = useCopy(value)
  const label = name ? t('ui.feedback.copy_named', { name }) : t('ui.feedback.copy')
  return (
    <>
      <IconButton
        side={side}
        label={copied ? t('ui.feedback.copied') : label}
        tooltip={copied ? t('ui.feedback.copied') : label}
        size={size}
        className={className}
        onClick={() => { void copy() }}
      >
        {copied ? <IconCheck className="text-success" /> : <IconCopy />}
      </IconButton>
      <span role="status" className="sr-only">{copied ? t('ui.feedback.copied') : ''}</span>
    </>
  )
}

/**
 * A value that copies when you click it: an ID, a link, an address. `value` is what
 * lands on the clipboard; `display` is what is shown (a shortened or masked form).
 * The whole text is the button, so the target is large, and it truncates rather than
 * wrapping so a long link does not break a table row.
 */
export function CopyText({ value, display, name, mono = true, className }: {
  value: string
  display?: string
  name?: string
  mono?: boolean
  className?: string
}) {
  const { t } = useSpacedT()
  const { copied, copy } = useCopy(value)
  const label = name ? t('ui.feedback.copy_named', { name }) : t('ui.feedback.copy')
  return (
    <>
      <button
        type="button"
        title={copied ? t('ui.feedback.copied') : label}
        aria-label={`${label}: ${display ?? value}`}
        onClick={() => { void copy() }}
        className={cn(
          'group/copy inline-flex max-w-full min-w-0 items-center gap-1.5 rounded-sm text-left outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50',
          mono && 'font-mono text-[0.8125rem]', className,
        )}
      >
        <span className="truncate">{display ?? value}</span>
        {copied
          ? <IconCheck aria-hidden className="size-3.5 shrink-0 text-success" />
          : <IconCopy aria-hidden className="size-3.5 shrink-0 opacity-0 transition-opacity group-hover/copy:opacity-60 group-focus-visible/copy:opacity-60 [@media(hover:none)]:opacity-50" />}
      </button>
      <span role="status" className="sr-only">{copied ? t('ui.feedback.copied') : ''}</span>
    </>
  )
}
