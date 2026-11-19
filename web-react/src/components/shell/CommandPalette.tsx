import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import type { Role } from '@/api/types'
import { eventOrigin, isCodeEditorTarget, isTypingTarget } from '@/lib/typing'
import type { CommandContext, CommandEntry, CommandProvider } from './commands'

// Another dialog is on screen if there is a role=dialog or alertdialog that is not
// the palette itself (Base UI and MUI dialogs both set the role), or an MUI modal
// that is mounted and not hidden (a drawer, which has no dialog role of its own).
// Opening ⌘K over one would stack a second modal on a task the user is in the
// middle of, and a confirm that is waiting on an answer must not be buried.
export function anotherDialogIsOpen(): boolean {
  for (const el of document.querySelectorAll('[role="dialog"], [role="alertdialog"]')) {
    if (!el.querySelector('[data-slot="command"]')) return true
  }
  return document.querySelector('.MuiModal-root:not(.MuiModal-hidden)') !== null
}

export function isMacPlatform(): boolean {
  return typeof navigator !== 'undefined' && /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent)
}

/**
 * ⌘K / Ctrl+K toggles the palette.
 *
 * ⌘K (meta) works everywhere except in the code editor, which owns its own keys.
 * Ctrl+K is the shortcut only off macOS (on a Mac it is "delete to end of line" in a text
 * field) and then not while typing in a field either. The palette's own input is always
 * exempt, so the shortcut that opened it closes it. It does not open while another dialog
 * is open (see above), and the key is only swallowed when the palette actually opens or
 * closes: a Ctrl+K that does nothing must reach whatever else wanted it.
 */
export function useCommandPaletteHotkey(open: boolean, setOpen: (open: boolean) => void) {
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      // Not during IME composition: the key belongs to the candidate window.
      if (e.isComposing || e.keyCode === 229) return
      if (e.key.toLowerCase() !== 'k' || e.altKey || e.shiftKey) return
      const origin = eventOrigin(e)
      const viaMeta = e.metaKey
      const viaCtrl = e.ctrlKey && !e.metaKey && !isMacPlatform()
      if (!viaMeta && !viaCtrl) return
      const inPalette = origin instanceof Element && origin.closest('[data-slot="command"]') !== null
      if (!inPalette) {
        if (isCodeEditorTarget(origin)) return
        if (viaCtrl && isTypingTarget(origin)) return
      }
      if (open) {
        e.preventDefault()
        setOpen(false)
      } else if (!anotherDialogIsOpen()) {
        e.preventDefault()
        setOpen(true)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, setOpen])
}

interface Group {
  provider: CommandProvider
  entries: CommandEntry[]
  loading: boolean
}

// Runs every provider for the current query. A provider that is local answers in the
// same tick; one with a debounce waits for the user to stop typing. A run is
// abandoned (its signal aborted, its answer ignored) as soon as the query changes or
// the palette closes, so a slow search can never overwrite a newer one.
function useProviderResults(providers: readonly CommandProvider[], query: string, open: boolean, ctx: CommandContext): Group[] {
  const [results, setResults] = useState<Record<string, { entries: CommandEntry[]; loading: boolean }>>({})
  // The context object changes identity each render; the search itself only needs
  // its latest value, not to restart on it.
  const ctxRef = useRef(ctx)
  useEffect(() => { ctxRef.current = ctx })

  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    const timers: ReturnType<typeof setTimeout>[] = []

    for (const p of providers) {
      const set = (entries: CommandEntry[], loading: boolean) => {
        if (controller.signal.aborted) return
        setResults(prev => ({ ...prev, [p.id]: { entries, loading } }))
      }
      if (query.trim().length < (p.minQueryLength ?? 0)) {
        set([], false)
        continue
      }
      const run = () => {
        try {
          const out = p.search(query, ctxRef.current, controller.signal)
          if (Array.isArray(out)) { set(out, false); return }
          set([], true)
          out.then(entries => set(entries, false), () => set([], false))
        } catch {
          set([], false)
        }
      }
      if (p.debounceMs) timers.push(setTimeout(run, p.debounceMs))
      else run()
    }
    return () => { controller.abort(); timers.forEach(clearTimeout) }
  }, [providers, query, open])

  return useMemo(() => [...providers]
    .sort((a, b) => (a.order ?? 0) - (b.order ?? 0))
    .map(provider => ({
      provider,
      entries: (results[provider.id]?.entries ?? []).slice(0, provider.limit ?? 8),
      loading: results[provider.id]?.loading ?? false,
    })), [providers, results])
}

/**
 * The palette: a search box over groups of results from the providers it is given.
 * It is controlled (`open` / `onOpenChange`) so the top bar's button and the hotkey
 * share one state. Focus returns to whatever had it before it opened (Base UI's dialog
 * restores it), so ⌘K from a table row drops the user back on that row.
 */
export function CommandPalette({ open, onOpenChange, providers, navigate, role }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  providers: readonly CommandProvider[]
  navigate: (to: string) => void
  role: Role | ''
}) {
  const { t } = useTranslation('common')
  const [query, setQuery] = useState('')
  const close = useCallback(() => onOpenChange(false), [onOpenChange])
  const ctx = useMemo<CommandContext>(() => ({ navigate, close, role }), [navigate, close, role])
  const groups = useProviderResults(providers, query, open, ctx)

  // Start every opening from an empty box.
  useEffect(() => { if (!open) setQuery('') }, [open])

  function choose(entry: CommandEntry) {
    close()
    if (entry.to) navigate(entry.to)
    else entry.run?.(ctx)
  }

  const nothing = groups.every(g => g.entries.length === 0)
  const searching = groups.some(g => g.loading)

  return (
    <CommandDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('ui.shell.palette.title')}
      description={t('ui.shell.palette.description')}
      className="sm:max-w-xl"
    >
      {/* Filtering is the providers' job (a server search has already filtered), so
          cmdk's own is off. */}
      <Command shouldFilter={false}>
        <CommandInput value={query} onValueChange={setQuery} placeholder={t('ui.shell.palette.placeholder')} />
        <CommandList className="max-h-[60vh]">
          {nothing && (
            <CommandEmpty>{searching ? t('ui.shell.palette.searching') : t('ui.shell.palette.empty')}</CommandEmpty>
          )}
          {groups.filter(g => g.entries.length > 0).map(({ provider, entries }) => (
            <CommandGroup key={provider.id} heading={provider.heading}>
              {entries.map(entry => {
                const Icon = entry.icon
                return (
                  <CommandItem
                    key={entry.id}
                    value={`${provider.id}:${entry.id}`}
                    onSelect={() => choose(entry)}
                  >
                    {Icon && <Icon />}
                    <span className="flex min-w-0 flex-col">
                      <span className="truncate">{entry.label}</span>
                      {entry.description && <span className="truncate text-xs text-muted-foreground">{entry.description}</span>}
                    </span>
                  </CommandItem>
                )
              })}
            </CommandGroup>
          ))}
        </CommandList>
      </Command>
    </CommandDialog>
  )
}
