import type { ReactNode } from 'react'
import { Link } from 'react-router'

import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { IconArrowLeft } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { MoreMenu, type MoreMenuItem } from './MoreMenu'
import { TOUCH_TARGET } from './touchTarget'

/**
 * THE OBJECT PAGE (owner decision, 2026-11): a thing with tabs or more than a screen of content (a user, later a
 * group, a template, a server) is a page with its own address, not a panel sliding in over its list. This is the
 * frame every such page shares, so they look and behave alike:
 *
 *   back link to the list · title with its badges · subtitle · status lines · actions (at most one primary, secondary
 *   buttons, the rest in a More menu) · the body (usually `ObjectTabs`)
 *
 * The page pads itself (`p-4 sm:p-6`; the shell gives its content none). Everything the load can end in is drawn in
 * the same frame, with the back link still there: pass `state` instead of the header while the object is not ready.
 * Short tasks on the object (a confirmation, a one-time secret) stay dialogs over the page; they are not tabs.
 */
export interface ObjectBack {
  /** The list's address, with its page, search, order and filters when the person came from it (see `useBackTarget`). */
  to: string
  /** "Users": the thing the link leads back to. The visible text is "‹ Users". */
  label: string
}

export type ObjectPageState =
  | { kind: 'loading' }
  | { kind: 'not_found'; title: string; description?: string }
  | { kind: 'forbidden'; title: string; description?: string }
  | { kind: 'error'; reason: unknown; retry: () => unknown; title?: string }

/** What a failed read means for the page: a 404 is "no such object", a 403 "not yours to open", anything else a failure to retry. */
export function objectErrorKind(error: unknown): 'not_found' | 'forbidden' | 'error' {
  const status = (error as { response?: { status?: number } } | null | undefined)?.response?.status
  return status === 404 ? 'not_found' : status === 403 ? 'forbidden' : 'error'
}

export function BackLink({ back }: { back: ObjectBack }) {
  // A plain link (an address that can be opened, copied, middle-clicked), not a button made to look like one.
  return (
    <Link
      to={back.to}
      data-slot="object-back"
      className={cn(TOUCH_TARGET, 'inline-flex w-fit items-center gap-1 rounded-md py-1 pr-2 text-sm text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50')}
    >
      <IconArrowLeft aria-hidden className="size-4" />
      {back.label}
    </Link>
  )
}

/** The skeleton of a loading object page: the shape of what is coming (header, tab bar, one block), so nothing jumps. */
function ObjectSkeleton() {
  return (
    <div role="status" aria-busy="true" data-slot="object-loading" className="space-y-4">
      <div aria-hidden className="space-y-4">
        <Skeleton className="h-7 w-56" />
        <Skeleton className="h-4 w-80 max-w-full" />
        <Skeleton className="h-(--control-h) w-full max-w-md" />
        <Skeleton className="h-48 w-full max-w-3xl rounded-xl" />
      </div>
    </div>
  )
}

function StateBody({ state, back }: { state: ObjectPageState; back: ObjectBack }) {
  const action = <Button variant="outline" render={<Link to={back.to} />}>{back.label}</Button>
  switch (state.kind) {
    case 'loading': return <ObjectSkeleton />
    case 'not_found':
    case 'forbidden': return <EmptyState title={state.title} description={state.description} action={action} />
    case 'error': return <ErrorState title={state.title} reason={state.reason} retry={state.retry} />
  }
}

export function ObjectPage({ back, title, badges, subtitle, status, primary, secondary, more, state, children, className }: {
  back: ObjectBack
  /** The object's name. Rendered as the page's one h1. */
  title?: ReactNode
  /** Beside the title: a role, a kind. Short. */
  badges?: ReactNode
  /** One line under the title (who it is: display name, address). */
  subtitle?: ReactNode
  /** Under the subtitle: the object's states, each already said in words. */
  status?: ReactNode
  /** The one solid button, contextual and never destructive. Often none. */
  primary?: ReactNode
  /** Outline buttons for the few state changes worth a click from here. A disabled one says why. */
  secondary?: ReactNode
  /** Everything else; destructive items last (MoreMenu puts them below a divider). */
  more?: MoreMenuItem[]
  /** The object is not ready (loading, missing, not allowed, failed): the frame shows that instead of the header and body. */
  state?: ObjectPageState
  children?: ReactNode
  className?: string
}) {
  const hasActions = !!(primary || secondary || (more && more.length > 0))
  return (
    <div data-slot="object-page" className={cn('p-4 sm:p-6', className)}>
      <div className="mb-3"><BackLink back={back} /></div>
      {state ? <StateBody state={state} back={back} /> : (
        <>
          <header data-slot="object-page-header" className="mb-4 flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
            <div className="min-w-0 flex-1 basis-60">
              <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                <h1 className="min-w-0 text-xl font-semibold tracking-tight break-words">{title}</h1>
                {badges}
              </div>
              {subtitle ? <p className="mt-1 max-w-3xl text-sm break-words text-muted-foreground">{subtitle}</p> : null}
              {status ? <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1.5">{status}</div> : null}
            </div>
            {hasActions && (
              <div data-slot="object-page-actions" className="flex max-w-full shrink-0 flex-wrap items-center gap-2">
                {secondary}
                {primary}
                {more && more.length > 0 && <MoreMenu items={more} />}
              </div>
            )}
          </header>
          {children}
        </>
      )}
    </div>
  )
}
