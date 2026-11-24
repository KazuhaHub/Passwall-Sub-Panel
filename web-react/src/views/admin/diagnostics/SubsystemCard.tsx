import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { HelpTip } from '@/components/common/HelpTip'
import { Button, buttonVariants } from '@/components/ui/button'
import { IconChevronDown, IconCopy } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { copyToClipboard } from '@/utils/clipboard'
import type { CardSummary, RawFamily } from '@/utils/diagnostics'
import { CARD_LINK, LINK_TARGET } from '@/utils/diagnosticsCatalog'
import { Disclosure } from '@/components/common/Disclosure'
import RawMetricTable from './RawMetricTable'
import { CardStateBadge } from './StateBadge'
import type { DiagFormat } from './useDiagFormat'

/** The series a card's families hold in this reading, as the API sent them. */
export function cardSeries(families: RawFamily[]) {
  const series = families.flatMap(f => f.series)
  return {
    counters: series.flatMap(s => s.counter ? [s.counter] : []),
    gauges: series.flatMap(s => s.gauge ? [s.gauge] : []),
    histograms: series.flatMap(s => s.histogram ? [s.histogram] : []),
  }
}

/** Whether a subsystem is something to look at before anything else. */
export const needsAttention = (card: CardSummary) => card.state === 'failing' || card.state === 'attention'

/**
 * One subsystem: a line with its name, state and one plain sentence, which is
 * all a healthy one costs; open, the figures, the page to go to and its own
 * slice of the raw data. Subsystems that need attention start open, the rest
 * folded; what someone opens stays open across the page's refreshes because
 * the open state lives in the page. The id is the anchor a finding's "show
 * data" scrolls to.
 */
export default function SubsystemCard({ card, sentence, extra, children, families, windowMs, fmt, open, onToggle, rawOpen, onToggleRaw }: {
  card: CardSummary
  sentence: string
  /** Lines under the sentence that hold whatever the body shows (notices). */
  extra?: ReactNode
  /** The figures; left out by the caller whenever they must be withheld. */
  children?: ReactNode
  /** This card's families in the current reading (rawFamilies). */
  families: RawFamily[]
  windowMs: number
  fmt: DiagFormat
  open: boolean
  onToggle: () => void
  rawOpen: boolean
  onToggleRaw: () => void
}) {
  const { t } = fmt
  const link = CARD_LINK[card.id]
  const target = link ? LINK_TARGET[link] : undefined
  const seriesCount = families.reduce((n, f) => n + f.series.length, 0)
  const copyGroup = () => void copyToClipboard(JSON.stringify({ card: card.id, ...cardSeries(families) }, null, 2))
  const title = t(`admin:diagnostics.cards.${card.id}.title`)
  const loud = needsAttention(card)

  return (
    // No scroll margin of its own: the stylesheet's html scroll-padding-top already clears the
    // shell's sticky top bar, and the two would add.
    <section id={`diag-card-${card.id}`} data-state={card.state}
      className={cn('min-w-0 rounded-xl bg-card ring-1', loud ? 'ring-foreground/25' : 'ring-foreground/10')}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2.5">
        <h3 className="m-0 text-base font-semibold">
          <button type="button" aria-expanded={open} onClick={onToggle}
            className="inline-flex items-center gap-1.5 rounded-md text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50 pointer-coarse:min-h-[2rem]">
            <IconChevronDown aria-hidden className={cn('size-4 shrink-0 text-muted-foreground transition-transform', !open && '-rotate-90')} />
            {title}
          </button>
        </h3>
        <CardStateBadge state={card.state} />
        <HelpTip text={t(`admin:diagnostics.cards.${card.id}.purpose`)} label={t('admin:diagnostics.help_label_card', { title })} />
        <p className="min-w-0 flex-1 basis-72 text-sm text-muted-foreground">{sentence}</p>
      </div>
      {open && (
        <div className="px-4 pb-3">
          {card.notices && <p className="text-xs text-muted-foreground">{t('admin:diagnostics.cards.common.notices')}</p>}
          {extra}
          {children}
          {target && (
            <div className="mt-3">
              {/* A real link (role link), drawn like a button: it navigates. */}
              <Link to={target.path} className={buttonVariants({ variant: 'outline', size: 'sm' })}>
                {t('admin:diagnostics.links.open', { page: t(target.nav) })}
              </Link>
            </div>
          )}
          <Disclosure open={rawOpen} onToggle={onToggleRaw} className="mt-2 border-t pt-2" buttonClassName="py-1 text-sm"
            summary={t('admin:diagnostics.cards.common.details', { count: fmt.count(seriesCount) })}>
            <div className="mt-2 min-w-0">
              <div className="flex justify-end">
                <Button variant="outline" size="sm" onClick={copyGroup}>
                  <IconCopy />{t('admin:diagnostics.cards.common.copy_group')}
                </Button>
              </div>
              <RawMetricTable families={families} fmt={fmt} windowMs={windowMs} />
            </div>
          </Disclosure>
        </div>
      )}
    </section>
  )
}
