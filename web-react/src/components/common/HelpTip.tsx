import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { IconHelp, IconInfo } from '@/lib/icons'

import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Button } from '@/components/ui/button'
import { IconButton } from './IconButton'
import { TOUCH_TARGET } from './touchTarget'
import { cn } from '@/lib/utils'

// The explanation patterns, on popover/tooltip. They take translated TEXT, not
// i18n keys as components/HelpTip.tsx did: that component took keys so the risk
// center's key-coverage test could see every help text a page names, a constraint
// that belongs to that page, not to the pattern.

/**
 * A page's or a tab's explanation behind a "?" at its top right. The text is one
 * click away, and the list stays what the admin sees first. `label` names the
 * button; a page with several of them passes each its own so a screen reader does
 * not hear the same name on every one.
 */
export function HelpTip({ text, label, className }: { text: ReactNode; label?: string; className?: string }) {
  const { t } = useTranslation('common')
  const name = label ?? t('ui.feedback.help_label')
  return (
    <Popover>
      <PopoverTrigger render={<IconButton label={name} size="icon-sm" aria-haspopup="dialog" className={className} />}>
        <IconHelp />
      </PopoverTrigger>
      <PopoverContent align="end" className="w-auto max-w-sm text-[13px] leading-relaxed whitespace-pre-line">
        {text}
      </PopoverContent>
    </Popover>
  )
}

/**
 * An (i) that explains a concept next to the field it belongs to. The text is the
 * popover and the button's accessible name, so it reads the same with a pointer, a
 * keyboard or a screen reader. It opens on hover and focus and also on click or tap,
 * which is the part a plain tooltip lacks on a phone.
 */
export function InfoTip({ text, className }: { text: string; className?: string }) {
  return (
    <Popover>
      <PopoverTrigger
        openOnHover
        delay={150}
        aria-label={text}
        render={<Button variant="ghost" size="icon-xs" className={cn(TOUCH_TARGET, 'text-muted-foreground', className)} />}
      >
        <IconInfo />
      </PopoverTrigger>
      <PopoverContent className="w-auto max-w-xs px-3 py-2 text-xs leading-relaxed whitespace-pre-line">{text}</PopoverContent>
    </Popover>
  )
}

/**
 * A one-glance verdict under a form field, with the reasoning one click away.
 *
 * The reasoning has to be there (an operator who reads "void fleet-wide" and
 * cannot find out WHICH server is affected either trusts the form blindly or
 * ignores the warning), but a full sentence inline reflows the field and pushes the
 * form around as the user types. So the summary is always visible and the detail is
 * on demand. Click rather than hover: hover is unreachable on touch.
 */
export function FieldHint({ summary, detail, tone = 'warning' }: {
  summary: string
  detail: string
  /** 'warning' for a broken or misleading value, 'muted' for the merely informational. */
  tone?: 'warning' | 'muted'
}) {
  return (
    <Popover>
      <PopoverTrigger
        // type=button: this lives inside forms, and a button with no type submits
        // them. A hint that saves the dialog when clicked would be a far worse bug
        // than the layout problem it was added to fix.
        type="button"
        className={cn(
          'inline-flex items-center gap-1 rounded-sm text-left align-baseline text-[length:inherit] underline-offset-2 outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring/50',
          tone === 'warning' ? 'text-warning-ink' : 'text-muted-foreground',
        )}
      >
        {summary}
        <IconInfo aria-hidden className="size-3.5 shrink-0" />
      </PopoverTrigger>
      <PopoverContent align="start" className="w-auto max-w-sm text-[13px] leading-relaxed">{detail}</PopoverContent>
    </Popover>
  )
}
