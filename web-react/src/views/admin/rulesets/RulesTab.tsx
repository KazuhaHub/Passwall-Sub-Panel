import { lazy, Suspense, useCallback, useDeferredValue, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'

import type { ProxyGroupIssue } from '@/api/rules'
import { confirm } from '@/components/ConfirmHost'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import type { ReorderCall } from '@/components/data/dataTableTypes'
import { pushToast } from '@/components/feedback/toasts'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { useIsMobile } from '@/hooks/use-mobile'
import { IconAdd, IconCopy, IconDelete, IconReset, IconSearch, IconSpinner } from '@/lib/icons'
import { copyToClipboard } from '@/utils/clipboard'
import { blockAt, type Block } from '@/utils/ruleset/blocks'
import type { PasteEntry } from '@/utils/ruleset/detect'
import {
  deleteBlock, deleteLines, editRule, insertRules, mergeBlocks, mergePreview, moveBlock, moveLinesToBlock, moveLinesToNewBlock, newBlock, replaceRange,
  retargetLines, setNote,
} from '@/utils/ruleset/ops'
import { BlockDetail } from './BlockDetail'
import { BlockDirectory } from './BlockDirectory'
import { MoveToDialog, type MoveChoice } from './MoveToDialog'
import { PasteDialog, type PasteWhere } from './PasteDialog'
import { RuleEditDialog } from './RuleEditDialog'
import { targetChoices } from './targets'
import { TextLinesDialog } from './TextLinesDialog'
import { useRuleDoc } from './useRuleDoc'

const CodeEditor = lazy(() => import('@/components/CodeEditor'))

const VIEW_KEY = 'psp_ruleset_view'
type View = 'blocks' | 'text'
const IMPORT_LIMIT = 1 << 20

function storedView(): View {
  try { return localStorage.getItem(VIEW_KEY) === 'text' ? 'text' : 'blocks' } catch { return 'blocks' }
}

type Dialog =
  | { kind: 'edit'; line: number }
  | { kind: 'add' }
  | { kind: 'paste'; where: PasteWhere; text?: string }
  | { kind: 'move_block'; block: number }
  | { kind: 'move_rows'; block: number; lines: number[] }
  | { kind: 'text'; start: number; end: number }

const range = (from: number, to: number) => Array.from({ length: to - from }, (_, i) => from + i)

/**
 * The Rules tab (design 3.2): the groups of rules in match order on the left, the open group's rules in the middle, and
 * the same text as one document when that is easier. Both views edit the draft `content`; switching loses nothing, because
 * an edit rewrites only the lines it names (see utils/ruleset/ops).
 *
 * The right-hand column of the design (the routing tester) is not built yet: the grid has room for it and the middle
 * column takes the width meanwhile.
 */
export function RulesTab({ content, setContent, readOnly, order, rematchNames, serverIssues }: {
  content: string
  setContent: (next: string) => void
  readOnly: boolean
  /** The proxy-group order the rule set stores, which the target picker follows. */
  order: readonly string[]
  rematchNames: readonly string[]
  /** What the server said about the main rules the last time a save was checked. */
  serverIssues: readonly ProxyGroupIssue[]
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
  const [dialog, setDialog] = useState<Dialog | null>(null)
  const [announce, setAnnounce] = useState('')
  const search = useRef<HTMLInputElement>(null)
  const fileInput = useRef<HTMLInputElement>(null)
  const importInto = useRef<number>(0)

  const choices = useMemo(() => targetChoices(blocks, order, rematchNames), [blocks, order, rematchNames])
  const current = blocks[Math.min(selected, blocks.length - 1)] as Block | undefined
  const currentIndex = current?.index ?? 0

  const setView = (next: View) => {
    if (next === view) return
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
  const select = (index: number) => { setSelected(index); setOpened(true) }

  const reorder = ({ movedId, beforeId }: ReorderCall) => {
    const from = Number(movedId)
    const before = beforeId === null ? blocks.length : Number(beforeId)
    if (!apply((d, b) => moveBlock(d, b, from, before))) return
    const to = before > from ? before - 1 : before
    setSelected(to)
    setAnnounce(t('admin:rules.move.announce', { from: from + 1, to: to + 1 }))
  }

  const addGroup = (before: number) => setDialog({ kind: 'paste', where: { kind: 'new', before } })

  const copyBlock = (b: Block, plain: boolean) => {
    const lines = doc.lines.slice(b.ruleStart, b.end).map(l => l.raw.replace(/\r$/, ''))
    void copyToClipboard((plain ? lines.map(l => l.replace(/^\s*-\s+/, '').split(',').slice(1, 2).join('')) : lines.map(l => l.trimStart())).join('\n') + '\n')
  }

  const removeBlock = async (b: Block) => {
    const ok = await confirm({
      title: t('admin:rules.blocks.delete'),
      message: t('admin:rules.blocks.delete_confirm', { target: b.kind === 'text' ? t('admin:rules.blocks.text_block', { count: b.count }) : b.target, count: b.count }),
      confirmText: t('admin:rules.blocks.delete'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (ok) apply((d, bl) => deleteBlock(d, bl[b.index]))
  }

  const mergeNext = async (b: Block) => {
    const preview = mergePreview(doc, blocks, b.index)
    if (!preview.ok) return
    const ok = preview.lines.length === 0 || await confirm({
      title: t('admin:rules.blocks.merge'),
      message: `${t('admin:rules.blocks.merge_confirm')}\n${preview.lines.join('\n')}`,
      confirmText: t('admin:rules.blocks.merge'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (ok) apply((d, bl) => mergeBlocks(d, bl, b.index))
  }

  const menuOf = useCallback((b: Block): MoreMenuItem[] => {
    const items: MoreMenuItem[] = [
      { id: 'move', label: t('admin:rules.move.title'), onSelect: () => setDialog({ kind: 'move_block', block: b.index }) },
      { id: 'above', label: t('admin:rules.blocks.add_above'), icon: IconAdd, onSelect: () => addGroup(b.index) },
      { id: 'below', label: t('admin:rules.blocks.add_below'), icon: IconAdd, onSelect: () => addGroup(b.index + 1) },
    ]
    if (mergePreview(doc, blocks, b.index).ok) items.push({ id: 'merge', label: t('admin:rules.blocks.merge'), icon: IconReset, onSelect: () => void mergeNext(b) })
    if (b.kind === 'rules') {
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
    if (el.closest('input, textarea, select, [contenteditable="true"], .cm-editor')) return
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'z' && !readOnly && view === 'blocks') {
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
    switch (dialog.kind) {
      case 'edit': {
        const line = doc.lines[dialog.line]
        const rule = line?.rule
        if (!rule) return null
        if (!rule.structured) {
          return <TextLinesDialog title={t('admin:rules.matchers.edit_lines_as_text')} initial={line.raw.replace(/\r$/, '')} onClose={close}
            onSubmit={text => { apply(d => replaceRange(d, dialog.line, dialog.line + 1, text)); close() }} />
        }
        return <RuleEditDialog mode="edit" groupName={rule.target} initial={{ type: rule.type, value: rule.value, options: rule.options }} onClose={close}
          onSubmit={r => { apply(d => editRule(d, dialog.line, { type: r.type, value: r.value, options: r.options })); close() }} />
      }
      case 'add': {
        const b = current
        if (!b) return null
        return <RuleEditDialog mode="add" groupName={b.target} initial={{ type: 'DOMAIN-SUFFIX', value: '', options: [] }} onClose={close}
          onSubmit={r => { apply((d, bl) => insertRules(d, bl[b.index].end, [r], b.target)); close() }} />
      }
      case 'paste': {
        const where = dialog.where
        return <PasteDialog where={where} doc={doc} blocks={blocks} choices={choices} initialText={dialog.text} onClose={close}
          onSubmit={(entries: PasteEntry[], target) => {
            const rules = entries.map(e => ({ type: e.type, value: e.value, options: e.options }))
            if (where.kind === 'into') apply((d, bl) => insertRules(d, bl[where.block].end, rules, bl[where.block].target))
            else {
              apply((d, bl) => newBlock(d, bl, where.before, target, rules))
              setSelected(where.before)
              setOpened(true)
            }
            setAnnounce(t('admin:rules.paste.added', { count: rules.length.toLocaleString() }))
            pushToast({ message: t('admin:rules.paste.added', { count: rules.length.toLocaleString() }), severity: 'success' })
            close()
          }} />
      }
      case 'move_block':
        return <MoveToDialog mode="block" blocks={blocks} from={dialog.block} choices={choices} onClose={close}
          onSubmit={choice => {
            const from = dialog.block
            const before = choice.kind === 'top' ? 0 : choice.kind === 'after' ? choice.block + 1 : from
            if (apply((d, bl) => moveBlock(d, bl, from, before))) {
              const to = before > from ? before - 1 : before
              setSelected(to)
              setAnnounce(t('admin:rules.move.announce', { from: from + 1, to: to + 1 }))
            }
            close()
          }} />
      case 'move_rows':
        return <MoveToDialog mode="rows" blocks={blocks} from={dialog.block} choices={choices} count={dialog.lines.length} onClose={close}
          onSubmit={(choice: MoveChoice) => {
            const { lines, block } = dialog
            if (choice.kind === 'into') { apply((d, bl) => moveLinesToBlock(d, bl, lines, choice.block)); setSelected(choice.block - (lines.length === blocks[block].count && choice.block > block ? 1 : 0)) }
            else if (choice.kind === 'new') { apply((d, bl) => moveLinesToNewBlock(d, bl, lines, choice.target, choice.before)); setSelected(choice.before) }
            close()
          }} />
      case 'text': {
        const text = doc.lines.slice(dialog.start, dialog.end).map(l => l.raw.replace(/\r$/, '')).join('\n')
        return <TextLinesDialog title={t('admin:rules.matchers.edit_lines_as_text')} initial={text} onClose={close}
          onSubmit={next => { apply(d => replaceRange(d, dialog.start, dialog.end, next)); close() }} />
      }
    }
  }

  const stats = t('admin:rules.stats', { rules: blocks.reduce((n, b) => n + (b.kind === 'rules' ? b.count : 0), 0).toLocaleString(), blocks: blocks.length.toLocaleString() })
  const resultsText = !q ? '' : hitTotal === 0 ? t('admin:rules.search.none', { query }) : t('admin:rules.search.results', { count: hitTotal, blocks: hits?.size ?? 0 })
  const showDirectory = !phone || !opened
  const showDetail = !phone || opened

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

      <div className="flex flex-wrap items-center gap-2" role="toolbar" aria-label={t('admin:rules.toolbar')}>
        <div className="relative min-w-0 flex-1 basis-64 sm:max-w-md">
          <IconSearch aria-hidden className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input ref={search} value={query} className="pl-8" placeholder={t('admin:rules.search.placeholder')} aria-label={t('admin:rules.search.placeholder')}
            onChange={e => setQuery(e.target.value)} onKeyDown={onSearchKey} data-rules-search />
        </div>
        <SegmentedControl<View> aria-label={t('admin:rules.view.label')} value={view} onChange={setView}
          options={[{ value: 'blocks', label: t('admin:rules.view.blocks') }, { value: 'text', label: t('admin:rules.view.text') }]} />
        {view === 'blocks' && targets.length > 1 && (
          <div className="w-48">
            <NativeSelect aria-label={t('admin:rules.search.by_target')} value={targetFilter} onChange={e => setTargetFilter(e.target.value)}>
              <option value="">{t('admin:rules.search.by_target')}</option>
              {targets.map(x => <option key={x} value={x}>{x}</option>)}
            </NativeSelect>
          </div>
        )}
        {!readOnly && view === 'blocks' && (
          <span className="flex items-center gap-1">
            <Button variant="outline" size="sm" disabled={!history.canUndo} onClick={history.undo}>{t('admin:rules.undo')}</Button>
            <Button variant="outline" size="sm" disabled={!history.canRedo} onClick={history.redo}>{t('admin:rules.redo')}</Button>
          </span>
        )}
        <span className="text-xs text-muted-foreground tabular-nums">{stats}</span>
      </div>
      <p role="status" aria-live="polite" className="min-h-4 text-xs text-muted-foreground">{resultsText || announce}</p>

      {view === 'text' ? (
        <div className="flex flex-col gap-1.5">
          {phone && <p className="text-xs text-muted-foreground">{t('admin:rules.text.mobile_hint')}</p>}
          <div className="overflow-hidden rounded-lg border">
            <Suspense fallback={<div className="grid h-96 place-items-center"><IconSpinner aria-hidden className="animate-spin text-muted-foreground" /></div>}>
              <CodeEditor value={content} onChange={setContent} readOnly={readOnly} ariaLabel={t('admin:rules.field.content')} />
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
              {current ? (
                <BlockDetail
                  key={`${current.index}:${current.target}:${current.kind}`}
                  doc={doc} block={current} choices={choices} readOnly={readOnly} query={q} phone={phone} onBack={() => setOpened(false)}
                  onEditRow={line => setDialog({ kind: 'edit', line })}
                  onAddRow={() => setDialog({ kind: 'add' })}
                  onPaste={() => setDialog({ kind: 'paste', where: { kind: 'into', block: current.index } })}
                  onImport={() => { importInto.current = current.index; fileInput.current?.click() }}
                  onDeleteRows={lines => { apply(d => deleteLines(d, lines)); setAnnounce(t('admin:rules.matchers.deleted', { count: lines.length })) }}
                  onMoveRows={lines => setDialog({ kind: 'move_rows', block: current.index, lines })}
                  onRetarget={target => apply(d => retargetLines(d, range(current.ruleStart, current.end), target))}
                  onSetNote={text => apply((d, bl) => setNote(d, bl[current.index], text))}
                  onEditText={(start, end) => setDialog({ kind: 'text', start, end })}
                />
              ) : blocks.length > 0 ? <p className="text-sm text-muted-foreground">{t('admin:rules.blocks.select_hint')}</p> : null}
            </div>
          )}
        </div>
      )}
      <input ref={fileInput} type="file" accept=".txt,.list,.yaml,.yml,.conf,text/plain" hidden aria-hidden tabIndex={-1}
        onChange={e => { const f = e.target.files?.[0]; e.target.value = ''; if (f) void readFile(f) }} />
      {renderDialog()}
    </div>
  )
}
