import { lazy, Suspense, useCallback, useDeferredValue, useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'

import type { ProxyGroupIssue } from '@/api/rules'
import { confirm } from '@/components/ConfirmHost'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import type { ReorderCall } from '@/components/data/dataTableTypes'
import { pushToast } from '@/components/feedback/toasts'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { DetailDialog } from '@/components/data/DetailDialog'
import { IconButton } from '@/components/common/IconButton'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { useIsMobile } from '@/hooks/use-mobile'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { IconAdd, IconArrowLeft, IconClose, IconCopy, IconDelete, IconReset, IconSearch, IconSpinner, IconWarning } from '@/lib/icons'
import { copyToClipboard } from '@/utils/clipboard'
import { blockAt, type Block } from '@/utils/ruleset/blocks'
import type { Doc } from '@/utils/ruleset/parse'
import type { PasteEntry } from '@/utils/ruleset/detect'
import type { ProbeInput } from '@/utils/ruleset/match'
import {
  deleteBlock, deleteLines, editRule, insertRules, mergeBlocks, mergePreview, moveBlock, moveLinesToBlock, moveLinesToNewBlock, newBlock, replaceRange,
  retargetLines, setNote,
} from '@/utils/ruleset/ops'
import { IssuesPanel } from './IssuesPanel'
import { BlockDetail } from './BlockDetail'
import { BlockDirectory } from './BlockDirectory'
import { MoveToDialog, moveBlockOptions, type MoveChoice } from './MoveToDialog'
import { PasteDialog, type PasteWhere } from './PasteDialog'
import { RuleEditDialog } from './RuleEditDialog'
import { EMPTY_PROBE, RouteTester } from './RouteTester'
import { isBuiltinTarget, targetChoices } from './targets'
import { TargetView } from './TargetView'
import { TextLinesDialog } from './TextLinesDialog'
import { useRuleDoc } from './useRuleDoc'
import { useShadowFindings } from './useShadowFindings'

const CodeEditor = lazy(() => import('@/components/CodeEditor'))

const VIEW_KEY = 'psp_ruleset_view'
type View = 'blocks' | 'text'
const IMPORT_LIMIT = 1 << 20

function storedView(): View {
  try { return localStorage.getItem(VIEW_KEY) === 'text' ? 'text' : 'blocks' } catch { return 'blocks' }
}

// A dialog names what it works on by LINE ID, never by index: indexes move with every edit, ids do not. Groups are named by
// the id of their first line. All of it is turned into indexes only at the moment the edit is applied, against the document
// as it is then, and an id that is no longer there means "nothing is written" (see `run`).
type Dialog =
  | { kind: 'edit'; id: number }
  | { kind: 'add'; block: number }
  | { kind: 'paste'; where: { kind: 'into'; block: number } | { kind: 'new'; before: number | null }; text?: string }
  | { kind: 'move_block'; block: number }
  | { kind: 'move_rows'; block: number; ids: number[] }
  | { kind: 'text'; ids: number[] }

const range = (from: number, to: number) => Array.from({ length: to - from }, (_, i) => from + i)
const idxOf = (d: Doc, id: number) => d.lines.findIndex(l => l.id === id)
/** The indexes of lines named by id, or null if any of them is gone. */
const indexesOf = (d: Doc, ids: readonly number[]): number[] | null => {
  const at = new Map(d.lines.map((l, i) => [l.id, i]))
  const out: number[] = []
  for (const id of ids) { const i = at.get(id); if (i === undefined) return null; out.push(i) }
  return out
}
const blockOfLine = (d: Doc, bl: readonly Block[], id: number): Block | null => { const i = idxOf(d, id); return i < 0 ? null : blockAt(bl, i) }
const beforeIndex = (d: Doc, bl: readonly Block[], before: number | null): number => (before === null ? bl.length : (blockOfLine(d, bl, before)?.index ?? -1))

/**
 * The Rules tab (design 3.2): the groups of rules in match order on the left, the open group's rules in the middle, and
 * the same text as one document when that is easier. Both views edit the draft `content`; switching loses nothing, because
 * an edit rewrites only the lines it names (see utils/ruleset/ops).
 *
 * The routing test (design 3.9) is the third column on a wide screen, a dialog on a narrower one and a screen of its own on a
 * phone; the problems panel (3.13) is a dialog. Both read the same parsed draft, and both can open a line in its group.
 */
export function RulesTab({ content, setContent, readOnly, order, rematchNames, serverIssues, showTarget, subRule, ariaLabel }: {
  content: string
  setContent: (next: string) => void
  readOnly: boolean
  /** The proxy-group order the rule set stores, which the target picker follows. */
  order: readonly string[]
  rematchNames: readonly string[]
  /** What the server said about the main rules the last time a save was checked. */
  serverIssues: readonly ProxyGroupIssue[]
  /** Asks for the rules of one outbound to be shown (the Proxy groups tab's "Show its rules"); `token` makes a repeat of the same request count. */
  showTarget?: { name: string; token: number } | null
  /**
   * Set when this edits a sub-rule instead of the main rules: its targets are the rule set's proxy groups, which the main rules
   * (and the other sub-rules) name, so those are offered as targets and count as taken names; and the routing test, which
   * walks the main rules, is not shown.
   */
  subRule?: { groups: readonly string[] }
  /** The name of the text editor (the Text view) when it is not "Main rules". */
  ariaLabel?: string
}) {
  const { t } = useTranslation(['admin', 'common'])
  const phone = useIsMobile()
  const history = useRuleDoc(content, setContent)
  const { doc, blocks, apply } = history
  const [view, setViewState] = useState<View>(storedView)
  const [selected, setSelected] = useState(0)
  const [opened, setOpened] = useState(false)
  const [query, setQuery] = useState('')
  const [targetFilter, setTargetFilter] = useState('')
  // With a target chosen, the middle column either lists every rule of that target together (to read) or opens one group (to edit).
  const [targetMode, setTargetMode] = useState<'merged' | 'runs'>('merged')
  const [dialog, setDialog] = useState<Dialog | null>(null)
  const [announce, setAnnounce] = useState('')
  // The routing test: on a wide screen a third column, on a narrower one a dialog, on a phone a screen of its own. What was typed
  // in it is kept here so that moving between the three, or closing it, does not lose it.
  const wide = useMediaQuery('(min-width: 1280px)')
  const [testerOpen, setTesterOpen] = useState(() => typeof window !== 'undefined' && !!window.matchMedia?.('(min-width: 1280px)').matches)
  const [probe, setProbe] = useState<ProbeInput>(EMPTY_PROBE)
  const [issuesOpen, setIssuesOpen] = useState(false)
  const [focusRow, setFocusRow] = useState<{ id: number; token: number } | null>(null)
  const search = useRef<HTMLInputElement>(null)
  const fileInput = useRef<HTMLInputElement>(null)
  const importInto = useRef<number>(0)

  // A document with any line outside the dialect the structured view understands (parse.ts foreignOf) is edited as text only:
  // an edit next to such a line could change what the server reads. The view moves to Text by itself, and stays there until
  // the person chooses Groups again (which is possible once no foreign line is left), so fixing the last one while typing does not pull the editor away.
  const foreign = doc.foreign
  const textOnly = foreign.length > 0
  useEffect(() => {
    if (showTarget) { setTargetFilter(showTarget.name); setTargetMode('merged'); setOpened(false) }
  }, [showTarget])
  const findings = useShadowFindings(doc)
  const choices = useMemo(() => {
    const own = targetChoices(blocks, order, rematchNames)
    if (!subRule) return own
    return { ...own, groups: [...own.groups, ...subRule.groups.filter(g => !own.groups.includes(g) && !rematchNames.includes(g) && !isBuiltinTarget(g))] }
  }, [blocks, order, rematchNames, subRule])
  const current = blocks[Math.min(selected, blocks.length - 1)] as Block | undefined
  const currentIndex = current?.index ?? 0

  useEffect(() => {
    if (textOnly && view === 'blocks') { history.beginText(); setViewState('text') }
    // `history.beginText` is stable.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [textOnly, view])
  const shownView: View = textOnly ? 'text' : view
  const setView = (next: View) => {
    if (next === view || (next === 'blocks' && textOnly)) return
    if (next === 'text') history.beginText(); else history.endText()
    setViewState(next)
    try { localStorage.setItem(VIEW_KEY, next) } catch { /* private window: the choice just is not remembered */ }
  }

  // ---- search and filtering ------------------------------------------------------------------------------------
  const q = useDeferredValue(query.trim().toLowerCase())
  const hits = useMemo(() => {
    if (!q) return null
    const m = new Map<number, number>()
    doc.lines.forEach((l, i) => {
      if (!l.raw.toLowerCase().includes(q)) return
      const b = blockAt(blocks, i)
      if (b) m.set(b.index, (m.get(b.index) ?? 0) + 1)
    })
    return m
  }, [q, doc, blocks])
  const hitTotal = hits ? [...hits.values()].reduce((a, b) => a + b, 0) : 0
  const shown = useMemo(() => blocks.filter(b => (!hits || hits.has(b.index)) && (!targetFilter || b.target === targetFilter)), [blocks, hits, targetFilter])
  const filtering = q !== '' || targetFilter !== ''
  const targets = useMemo(() => [...new Set(blocks.filter(b => b.kind === 'rules' && b.target).map(b => b.target))], [blocks])

  // ---- edits -----------------------------------------------------------------------------------------------------
  const select = (index: number) => { setSelected(index); setOpened(true); setTargetMode('runs') }

  /**
   * Applies an edit written against the document as it is AT THIS MOMENT. The edit returns null when something it names
   * (a line id, a group) is gone; then nothing is written and the person is told, so a stale selection can never land
   * on whatever line has since taken its index.
   */
  const run = (edit: (d: Doc, bl: Block[]) => Doc | null): boolean => {
    let missing = false
    const changed = apply((d, bl) => { const r = edit(d, bl); if (r === null) { missing = true; return d } return r })
    if (missing) pushToast({ message: t('admin:rules.stale_line'), severity: 'warning' })
    return changed
  }
  const firstLineId = (b: Block) => doc.lines[b.start].id

  /** Opens a line (from the routing test or the problems panel) in its group, with anything that would hide it cleared. */
  const jumpToLine = (line: number) => {
    const b = blockAt(blocks, line)
    if (!b || textOnly) return
    setQuery(''); setTargetFilter(''); setTargetMode('runs')
    if (shownView === 'text') setView('blocks')
    setSelected(b.index); setOpened(true)
    setFocusRow({ id: doc.lines[line].id, token: Date.now() })
    // The narrower layouts show the test over the page; the page is what the jump is for.
    if (!wide) setTesterOpen(false)
  }

  const reorder = ({ movedId, beforeId }: ReorderCall) => {
    const from = Number(movedId)
    const before = beforeId === null ? blocks.length : Number(beforeId)
    if (!apply((d, b) => moveBlock(d, b, from, before))) return
    const to = before > from ? before - 1 : before
    setSelected(to)
    setAnnounce(t('admin:rules.move.announce', { from: from + 1, to: to + 1 }))
  }

  const addGroup = (before: number) => setDialog({ kind: 'paste', where: { kind: 'new', before: before < blocks.length ? firstLineId(blocks[before]) : null } })

  const copyBlock = (b: Block, plain: boolean) => {
    const lines = doc.lines.slice(b.ruleStart, b.end).map(l => l.raw.replace(/\r$/, ''))
    void copyToClipboard((plain ? lines.map(l => l.replace(/^\s*-\s+/, '').split(',').slice(1, 2).join('')) : lines.map(l => l.trimStart())).join('\n') + '\n')
  }

  const label = (b: Block) => (b.kind === 'text' ? t('admin:rules.blocks.text_block', { count: b.count }) : b.kind === 'notarget' ? t('admin:rules.blocks.no_target') : b.target)

  const removeBlock = async (b: Block) => {
    const id = firstLineId(b)
    const ok = await confirm({
      title: t('admin:rules.blocks.delete'),
      message: t('admin:rules.blocks.delete_confirm', { target: label(b), count: b.count }),
      confirmText: t('admin:rules.blocks.delete'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (ok) run((d, bl) => { const hit = blockOfLine(d, bl, id); return hit ? deleteBlock(d, hit) : null })
  }

  const mergeNext = async (b: Block) => {
    const preview = mergePreview(doc, blocks, b.index)
    if (!preview.ok) return
    const id = firstLineId(b)
    const ok = preview.lines.length === 0 || await confirm({
      title: t('admin:rules.blocks.merge'),
      message: `${t('admin:rules.blocks.merge_confirm')}\n${preview.lines.join('\n')}`,
      confirmText: t('admin:rules.blocks.merge'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (ok) run((d, bl) => { const hit = blockOfLine(d, bl, id); return hit ? mergeBlocks(d, bl, hit.index) : null })
  }

  const menuOf = useCallback((b: Block): MoreMenuItem[] => {
    const items: MoreMenuItem[] = [
      moveBlockOptions(blocks, b.index).length === 0
        ? { id: 'move', label: t('admin:rules.move.title'), disabled: true, disabledReason: t('admin:rules.move.no_destination') }
        : { id: 'move', label: t('admin:rules.move.title'), onSelect: () => setDialog({ kind: 'move_block', block: doc.lines[b.start].id }) },
      { id: 'above', label: t('admin:rules.blocks.add_above'), icon: IconAdd, onSelect: () => addGroup(b.index) },
      { id: 'below', label: t('admin:rules.blocks.add_below'), icon: IconAdd, onSelect: () => addGroup(b.index + 1) },
    ]
    if (mergePreview(doc, blocks, b.index).ok) items.push({ id: 'merge', label: t('admin:rules.blocks.merge'), icon: IconReset, onSelect: () => void mergeNext(b) })
    if (b.kind !== 'text') {
      items.push({ id: 'copy_rules', label: t('admin:rules.matchers.copy_rules'), icon: IconCopy, separatorBefore: true, onSelect: () => copyBlock(b, false) })
      items.push({ id: 'copy_list', label: t('admin:rules.matchers.copy_list'), icon: IconCopy, onSelect: () => copyBlock(b, true) })
    }
    items.push({ id: 'delete', label: t('admin:rules.blocks.delete'), icon: IconDelete, destructive: true, onSelect: () => void removeBlock(b) })
    return items
    // The handlers read the document and groups of the render they were built in.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [t, doc, blocks])

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const el = e.target as HTMLElement
    if (el.closest('input, textarea, select, [contenteditable="true"], .cm-editor, [role="dialog"]')) return
    // With a dialog open, undo would change the document under it.
    if (dialog !== null) return
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'z' && !readOnly && shownView === 'blocks') {
      e.preventDefault()
      if (e.shiftKey) history.redo(); else history.undo()
    }
  }

  const onSearchKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Escape') setQuery('')
    // Enter walks the groups that have hits, Shift+Enter backwards.
    if (e.key === 'Enter' && hits && shown.length > 0) {
      e.preventDefault()
      const at = shown.findIndex(b => b.index === currentIndex)
      const next = shown[(at + (e.shiftKey ? -1 : 1) + shown.length) % shown.length]
      select(next.index)
    }
  }

  const readFile = async (file: File) => {
    if (file.size > IMPORT_LIMIT) { pushToast({ message: t('admin:rules.paste.file_too_large', { size: '1 MB' }), severity: 'warning' }); return }
    const text = await file.text()
    setDialog({ kind: 'paste', where: { kind: 'into', block: importInto.current }, text })
  }

  // ---- dialogs ---------------------------------------------------------------------------------------------------
  const renderDialog = () => {
    if (!dialog) return null
    const close = () => setDialog(null)
    // The indexes the dialog's ids mean in the document as drawn now (nothing can change it while the dialog is open).
    const blockNow = (id: number) => blockOfLine(doc, blocks, id)
    switch (dialog.kind) {
      case 'edit': {
        const at = idxOf(doc, dialog.id)
        const line = doc.lines[at]
        const rule = line?.rule
        if (!rule) return null
        const id = dialog.id
        if (!rule.structured) {
          return <TextLinesDialog title={t('admin:rules.matchers.edit_lines_as_text')} initial={line.raw.replace(/\r$/, '')} onClose={close}
            onSubmit={text => { run(d => { const i = idxOf(d, id); return i < 0 ? null : replaceRange(d, i, i + 1, text) }); close() }} />
        }
        return <RuleEditDialog mode="edit" groupName={rule.target} initial={{ type: rule.type, value: rule.value, options: rule.options }} onClose={close}
          onSubmit={r => { run(d => { const i = idxOf(d, id); return i < 0 ? null : editRule(d, i, { type: r.type, value: r.value, options: r.options }) }); close() }} />
      }
      case 'add': {
        const b = blockNow(dialog.block)
        if (!b || b.kind !== 'rules') return null
        const id = dialog.block
        return <RuleEditDialog mode="add" groupName={b.target} initial={{ type: 'DOMAIN-SUFFIX', value: '', options: [] }} onClose={close}
          onSubmit={r => { run((d, bl) => { const hit = blockOfLine(d, bl, id); return hit ? insertRules(d, hit.end, [r], hit.target) : null }); close() }} />
      }
      case 'paste': {
        const w = dialog.where
        const where: PasteWhere = w.kind === 'into' ? { kind: 'into', block: blockNow(w.block)?.index ?? 0 } : { kind: 'new', before: beforeIndex(doc, blocks, w.before) }
        return <PasteDialog where={where} doc={doc} blocks={blocks} choices={choices} initialText={dialog.text} onClose={close}
          onSubmit={(entries: PasteEntry[], target) => {
            const rules = entries.map(e => ({ type: e.type, value: e.value, options: e.options }))
            const ok = run((d, bl) => {
              if (w.kind === 'into') { const hit = blockOfLine(d, bl, w.block); return hit && hit.kind === 'rules' ? insertRules(d, hit.end, rules, hit.target) : null }
              const before = beforeIndex(d, bl, w.before)
              return before < 0 ? null : newBlock(d, bl, before, target, rules)
            })
            if (ok && w.kind === 'new') { setSelected(where.kind === 'new' ? where.before : 0); setOpened(true) }
            if (ok) {
              setAnnounce(t('admin:rules.paste.added', { count: rules.length.toLocaleString() }))
              pushToast({ message: t('admin:rules.paste.added', { count: rules.length.toLocaleString() }), severity: 'success' })
            }
            close()
          }} />
      }
      case 'move_block': {
        const from = blockNow(dialog.block)
        if (!from) return null
        const id = dialog.block
        return <MoveToDialog mode="block" blocks={blocks} from={from.index} choices={choices} onClose={close}
          onSubmit={choice => {
            const moved = run((d, bl) => {
              const hit = blockOfLine(d, bl, id)
              if (!hit) return null
              const before = choice.kind === 'top' ? 0 : choice.kind === 'after' ? choice.block + 1 : hit.index
              return moveBlock(d, bl, hit.index, before)
            })
            if (moved) {
              const before = choice.kind === 'top' ? 0 : choice.kind === 'after' ? choice.block + 1 : from.index
              const to = before > from.index ? before - 1 : before
              setSelected(to)
              setAnnounce(t('admin:rules.move.announce', { from: from.index + 1, to: to + 1 }))
            }
            close()
          }} />
      }
      case 'move_rows': {
        const from = blockNow(dialog.block)
        if (!from || from.kind !== 'rules') return null
        const ids = dialog.ids
        const destId = (i: number) => doc.lines[blocks[i].start].id
        return <MoveToDialog mode="rows" blocks={blocks} from={from.index} choices={choices} count={ids.length} onClose={close}
          onSubmit={(choice: MoveChoice) => {
            if (choice.kind === 'into') {
              const dest = destId(choice.block)
              const ok = run((d, bl) => {
                const at = indexesOf(d, ids); const hit = blockOfLine(d, bl, dest)
                return at && hit && hit.kind === 'rules' ? moveLinesToBlock(d, bl, at, hit.index) : null
              })
              if (ok) setSelected(choice.block - (ids.length === from.count && choice.block > from.index ? 1 : 0))
            } else if (choice.kind === 'new') {
              const before = choice.before < blocks.length ? destId(choice.before) : null
              const ok = run((d, bl) => {
                const at = indexesOf(d, ids); const b = beforeIndex(d, bl, before)
                return at && b >= 0 ? moveLinesToNewBlock(d, bl, at, choice.target, b) : null
              })
              if (ok) setSelected(choice.before)
            }
            close()
          }} />
      }
      case 'text': {
        const at = indexesOf(doc, dialog.ids)
        if (!at) return null
        const ids = dialog.ids
        const text = at.map(i => doc.lines[i].raw.replace(/\r$/, '')).join('\n')
        return <TextLinesDialog title={t('admin:rules.matchers.edit_lines_as_text')} initial={text} onClose={close}
          onSubmit={next => {
            run(d => {
              const now = indexesOf(d, ids)
              // The lines must still be there and still be one run.
              if (!now || now.some((v, k) => k > 0 && v !== now[k - 1] + 1)) return null
              return replaceRange(d, now[0], now[now.length - 1] + 1, next)
            })
            close()
          }} />
      }
    }
  }

  const stats = t('admin:rules.stats', { rules: blocks.reduce((n, b) => n + (b.kind === 'rules' ? b.count : 0), 0).toLocaleString(), blocks: blocks.length.toLocaleString() })
  const resultsText = !q ? '' : hitTotal === 0 ? t('admin:rules.search.none', { query }) : t('admin:rules.search.results', { count: hitTotal, blocks: hits?.size ?? 0 })
  // On a phone the directory and the open group are two screens; the merged list of a target is a screen of its own.
  const merged = targetFilter !== '' && targetMode === 'merged'
  const showDirectory = !phone || (!opened && !merged)
  const showDetail = !phone || opened || merged

  // On a phone the test is a screen of its own, in place of the rules.
  if (phone && testerOpen && !subRule) {
    return (
      <div className="flex flex-col gap-3">
        <div className="flex items-center gap-2">
          <IconButton label={t('admin:rules.tester.back')} onClick={() => setTesterOpen(false)}><IconArrowLeft /></IconButton>
          <h2 className="text-base font-semibold">{t('admin:rules.tester.title')}</h2>
        </div>
        <RouteTester doc={doc} blocks={blocks} input={probe} setInput={setProbe} textOnly={textOnly} onJump={jumpToLine} />
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-3" onKeyDown={onKeyDown}>
      {serverIssues.length > 0 && (
        <Alert variant="destructive">
          <AlertDescription>
            <p className="font-medium">{t('admin:rules.issues.main_rules')}</p>
            <ul className="list-disc pl-4">{serverIssues.map((i, n) => <li key={n}>{i.message}</li>)}</ul>
          </AlertDescription>
        </Alert>
      )}

      {textOnly && (
        <Alert>
          <AlertDescription>
            <p className="font-medium">{t('admin:rules.problems.indent')}</p>
            <ul className="list-disc pl-4">
              {foreign.slice(0, 6).map(f => <li key={f.id}>{t('admin:rules.foreign.line', { line: f.line + 1, why: t(`admin:rules.foreign.${f.reason}`) })}</li>)}
              {foreign.length > 6 && <li>{t('admin:rules.paste.more', { count: foreign.length - 6 })}</li>}
            </ul>
          </AlertDescription>
        </Alert>
      )}

      <div className="flex flex-wrap items-center gap-2" role="toolbar" aria-label={t('admin:rules.toolbar')}>
        {shownView === 'blocks' && <div className="relative min-w-0 flex-1 basis-64 sm:max-w-md">
          <IconSearch aria-hidden className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input ref={search} value={query} className="pl-8" placeholder={t('admin:rules.search.placeholder')} aria-label={t('admin:rules.search.placeholder')}
            onChange={e => setQuery(e.target.value)} onKeyDown={onSearchKey} data-rules-search />
        </div>}
        <SegmentedControl<View> aria-label={t('admin:rules.view.label')} value={shownView} onChange={setView}
          options={[{ value: 'blocks', label: t('admin:rules.view.blocks'), disabled: textOnly }, { value: 'text', label: t('admin:rules.view.text') }]} />
        {shownView === 'blocks' && targetFilter !== '' && (
          <SegmentedControl<'merged' | 'runs'> aria-label={t('admin:rules.targetview.label')} value={targetMode} onChange={setTargetMode}
            options={[{ value: 'merged', label: t('admin:rules.targetview.merged') }, { value: 'runs', label: t('admin:rules.targetview.runs') }]} />
        )}
        {shownView === 'blocks' && (targets.length > 1 || targetFilter !== '') && (
          <div className="w-48">
            <NativeSelect aria-label={t('admin:rules.search.by_target')} value={targetFilter} onChange={e => { setTargetFilter(e.target.value); setTargetMode('merged') }}>
              <option value="">{t('admin:rules.search.by_target')}</option>
              {targets.map(x => <option key={x} value={x}>{x}</option>)}
            </NativeSelect>
          </div>
        )}
        {!readOnly && shownView === 'blocks' && (
          <span className="flex items-center gap-1">
            <Button variant="outline" size="sm" disabled={!history.canUndo || dialog !== null} onClick={history.undo}>{t('admin:rules.undo')}</Button>
            <Button variant="outline" size="sm" disabled={!history.canRedo || dialog !== null} onClick={history.redo}>{t('admin:rules.redo')}</Button>
          </span>
        )}
        <span className="text-xs text-muted-foreground tabular-nums">{stats}</span>
        <span className="ml-auto flex flex-wrap items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => setIssuesOpen(true)}>
            <IconWarning />
            {findings !== null && findings.length > 0 ? t('admin:rules.issues.button', { count: findings.length.toLocaleString() }) : t('admin:rules.issues.title')}
          </Button>
          {!subRule && <Button variant={testerOpen ? 'secondary' : 'outline'} size="sm" aria-pressed={testerOpen} onClick={() => setTesterOpen(o => !o)}>{t('admin:rules.tester.title')}</Button>}
        </span>
      </div>
      <p role="status" aria-live="polite" className="min-h-4 text-xs text-muted-foreground">{resultsText || announce}</p>

      <div className={wide && testerOpen && !subRule ? 'grid min-w-0 gap-4 xl:grid-cols-[minmax(0,1fr)_20rem]' : 'min-w-0'}>
      <div className="min-w-0">
      {shownView === 'text' ? (
        <div className="flex flex-col gap-1.5">
          {phone && <p className="text-xs text-muted-foreground">{t('admin:rules.text.mobile_hint')}</p>}
          <div className="overflow-hidden rounded-lg border">
            <Suspense fallback={<div className="grid h-96 place-items-center"><IconSpinner aria-hidden className="animate-spin text-muted-foreground" /></div>}>
              <CodeEditor value={content} onChange={setContent} readOnly={readOnly} ariaLabel={ariaLabel ?? t('admin:rules.field.content')} />
            </Suspense>
          </div>
          <p className="text-xs text-muted-foreground">{t('admin:rules.editor_keyboard_hint')}</p>
        </div>
      ) : (
        <div className="grid min-w-0 gap-4 lg:grid-cols-[minmax(16rem,22rem)_minmax(0,1fr)]">
          {showDirectory && (
            <div className="flex min-w-0 flex-col gap-2">
              <p className="text-xs text-muted-foreground">{t('admin:rules.blocks.order_hint')}</p>
              <BlockDirectory
                doc={doc} blocks={blocks} shown={shown} hits={hits} selected={currentIndex} onSelect={select} readOnly={readOnly}
                onReorder={reorder} reorderBlockedReason={filtering ? t('admin:rules.move.blocked_filter') : undefined} menuOf={menuOf}
                onAdd={() => addGroup(blocks.length)}
              />
              {!readOnly && blocks.length > 0 && (
                <div><Button variant="outline" size="sm" onClick={() => addGroup(blocks.length)}><IconAdd />{t('admin:rules.blocks.add')}</Button></div>
              )}
            </div>
          )}
          {showDetail && (
            <div className="min-w-0">
              {merged ? (
                <TargetView doc={doc} blocks={blocks} target={targetFilter} query={q} onOpenGroup={select} />
              ) : current ? (
                <BlockDetail
                  key={`${current.index}:${current.target}:${current.kind}`}
                  doc={doc} block={current} choices={choices} readOnly={readOnly} query={q} phone={phone} onBack={() => setOpened(false)} focusRow={focusRow}
                  onEditRow={id => setDialog({ kind: 'edit', id })}
                  onAddRow={() => setDialog({ kind: 'add', block: firstLineId(current) })}
                  onPaste={() => setDialog({ kind: 'paste', where: { kind: 'into', block: firstLineId(current) } })}
                  onImport={() => { importInto.current = firstLineId(current); fileInput.current?.click() }}
                  onDeleteRows={ids => { if (run(d => { const at = indexesOf(d, ids); return at ? deleteLines(d, at) : null })) setAnnounce(t('admin:rules.matchers.deleted', { count: ids.length })) }}
                  onMoveRows={ids => setDialog({ kind: 'move_rows', block: firstLineId(current), ids })}
                  onRetarget={target => { const id = firstLineId(current); run((d, bl) => { const hit = blockOfLine(d, bl, id); return hit ? retargetLines(d, range(hit.ruleStart, hit.end), target) : null }) }}
                  onSetNote={text => { const id = firstLineId(current); run((d, bl) => { const hit = blockOfLine(d, bl, id); return hit ? setNote(d, hit, text) : null }) }}
                  onEditText={ids => setDialog({ kind: 'text', ids })}
                />
              ) : blocks.length > 0 ? <p className="text-sm text-muted-foreground">{t('admin:rules.blocks.select_hint')}</p> : null}
            </div>
          )}
        </div>
      )}
      </div>
      {wide && testerOpen && !subRule && (
        <aside aria-label={t('admin:rules.tester.title')} className="flex min-w-0 flex-col gap-2 rounded-lg border p-3">
          <div className="flex items-center justify-between gap-2">
            <h2 className="text-sm font-semibold">{t('admin:rules.tester.title')}</h2>
            <IconButton label={t('admin:rules.tester.hide_tester')} size="icon-xs" onClick={() => setTesterOpen(false)}><IconClose /></IconButton>
          </div>
          <RouteTester doc={doc} blocks={blocks} input={probe} setInput={setProbe} textOnly={textOnly} onJump={jumpToLine} />
        </aside>
      )}
      </div>
      <input ref={fileInput} type="file" accept=".txt,.list,.yaml,.yml,.conf,text/plain" hidden aria-hidden tabIndex={-1}
        onChange={e => { const f = e.target.files?.[0]; e.target.value = ''; if (f) void readFile(f) }} />
      {renderDialog()}
      {!wide && !phone && testerOpen && !subRule && (
        <DetailDialog open onClose={() => setTesterOpen(false)} size="md" title={t('admin:rules.tester.title')}>
          <RouteTester doc={doc} blocks={blocks} input={probe} setInput={setProbe} textOnly={textOnly} onJump={jumpToLine} />
        </DetailDialog>
      )}
      {issuesOpen && <IssuesPanel doc={doc} findings={findings} textOnly={textOnly} onJump={textOnly ? undefined : jumpToLine} onClose={() => setIssuesOpen(false)} />}
    </div>
  )
}
