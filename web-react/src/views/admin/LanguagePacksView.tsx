import { useEffect, useMemo, useRef, useState } from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import { IconDelete, IconDownload } from '@/lib/icons'
import { deleteLocale, type LocaleMeta } from '@/api/locales'
import { getVersion } from '@/api/version'
import { AsyncButton } from '@/components/common/AsyncButton'
import { IconButton } from '@/components/common/IconButton'
import { MoreMenu } from '@/components/common/MoreMenu'
import { PageHeader } from '@/components/common/PageHeader'
import { StatusBadge } from '@/components/common/StatusBadge'
import { DataTable } from '@/components/data/DataTable'
import { ErrorState } from '@/components/feedback/ErrorState'
import { pushSnack } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { confirm } from '@/components/ConfirmHost'
import { isBuiltinLanguage } from '@/i18n'
import { useLocales } from '@/query/locales'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { downloadPack, exportBaseTemplate } from './languagePacks/packFiles'
import { UploadDialog } from './languagePacks/UploadDialog'
import { useAdminT } from './useAdminT'

// The languages compiled into the bundle, listed beside the uploaded ones so "which languages does this
// panel have, and which can I change" is one table. zh-TW is generated from zh-CN at build time.
const BUILTIN_CODES = ['zh-CN', 'zh-TW', 'en-US']

interface Row { code: string; name: string; author: string; baseVersion: string; builtin: boolean; meta?: LocaleMeta }

// Admin surface for runtime-uploaded UI language packs. A pack is a single JSON file (see api/locales.ts
// LocalePack); uploading it makes the language selectable after a page reload. Writes are admin-only (gated
// on config.write here + adminGroup server-side); the public read endpoints serve the pack to every client,
// including the pre-auth login screen.
export default function LanguagePacksView() {
  const t = useAdminT()
  const canConfig = useCan('config.write')
  const scope = useQueryScope()
  const query = useLocales(scope)
  const [uploading, setUploading] = useState(false)
  // The row whose delete is in flight: it is dimmed and its menu item is off, so a second click cannot
  // re-open the confirmation mid-request.
  const [deleting, setDeleting] = useState<string | null>(null)
  // Current panel version, for the base_version staleness note. Advisory only.
  const [currentVersion, setCurrentVersion] = useState('')
  useEffect(() => { void getVersion().then(v => setCurrentVersion(v.version)).catch(() => { /* advisory */ }) }, [])

  const rows = useMemo<Row[]>(() => [
    ...BUILTIN_CODES.map(code => ({ code, name: t(`language:${code}`), author: '', baseVersion: '', builtin: true })),
    ...(query.data ?? []).filter(m => !isBuiltinLanguage(m.code)).map(m => ({
      code: m.code, name: m.name, author: m.author ?? '', baseVersion: m.base_version ?? '', builtin: false, meta: m,
    })),
  ], [query.data, t])

  async function remove(m: LocaleMeta) {
    const ok = await confirm({
      title: t('admin:languagePacks.confirm.delete_title', { name: m.name }),
      message: t('admin:languagePacks.confirm.delete_message', { code: m.code, name: m.name }),
      confirmText: t('common:actions.delete'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok) return
    setDeleting(m.code)
    try {
      await deleteLocale(m.code)
      pushSnack(t('admin:languagePacks.toast.deleted'), 'success')
      await query.refetch()
    } catch { /* the request layer already surfaced it */ } finally { setDeleting(null) }
  }

  const removeRef = useRef(remove)
  removeRef.current = remove
  const stale = (r: Row) => !!r.baseVersion && !!currentVersion && r.baseVersion !== currentVersion

  const columns = useMemo<ColumnDef<Row>[]>(() => [
    {
      id: 'name', header: t('admin:languagePacks.table.name'), accessorFn: r => r.name,
      meta: { label: t('admin:languagePacks.table.name'), hideable: false },
      cell: c => <span className="font-medium">{c.row.original.name}</span>,
    },
    { id: 'code', header: t('admin:languagePacks.table.code'), accessorFn: r => r.code, meta: { label: t('admin:languagePacks.table.code'), className: 'font-mono text-[13px]' } },
    {
      id: 'source', header: t('admin:languagePacks.table.source'), accessorFn: r => (r.builtin ? 0 : 1), enableSorting: false, meta: { label: t('admin:languagePacks.table.source') },
      cell: c => c.row.original.builtin
        ? <span title={t('admin:languagePacks.builtin_reason')}>{t('admin:languagePacks.source.builtin')}</span>
        : <span>{t('admin:languagePacks.source.uploaded')}</span>,
    },
    {
      id: 'author', header: t('admin:languagePacks.table.author'), accessorFn: r => r.author, meta: { label: t('admin:languagePacks.table.author'), className: 'text-muted-foreground' },
      cell: c => <span className="block max-w-48 truncate" title={c.row.original.author}>{c.row.original.author || '—'}</span>,
    },
    {
      id: 'base_version', header: t('admin:languagePacks.table.base_version'), accessorFn: r => r.baseVersion, meta: { label: t('admin:languagePacks.table.base_version') },
      cell: c => (
        <span className="inline-flex flex-wrap items-center gap-2">
          <span className="text-muted-foreground">{c.row.original.baseVersion || '—'}</span>
          {stale(c.row.original) && (
            <span title={t('admin:languagePacks.stale_warning', { base: c.row.original.baseVersion, current: currentVersion })}>
              <StatusBadge tone="warning" label={t('admin:languagePacks.stale')} />
            </span>
          )}
        </span>
      ),
    },
    {
      id: 'actions', header: '', enableSorting: false, meta: { hideable: false, align: 'right', label: t('admin:languagePacks.table.actions') },
      cell: c => <RowActions row={c.row.original} t={t} canConfig={canConfig} busy={deleting === c.row.original.code} onDelete={m => void removeRef.current(m)} />,
    },
  // eslint-disable-next-line react-hooks/exhaustive-deps
  ], [t, currentVersion, canConfig, deleting])

  return (
    <div className="p-4 sm:p-6">
      <PageHeader
        title={t('admin:languagePacks.title')}
        description={t('admin:languagePacks.subtitle')}
        secondary={canConfig && <AsyncButton variant="outline" icon={<IconDownload />} onClick={exportBaseTemplate}>{t('admin:languagePacks.export_base')}</AsyncButton>}
        primary={canConfig && <Button onClick={() => setUploading(true)}>{t('admin:languagePacks.upload')}</Button>}
      />
      {/* A failed read is NOT "no packs uploaded". */}
      {query.isError ? (
        <ErrorState title={t('admin:languagePacks.load_failed')} reason={query.error} retry={() => query.refetch()} />
      ) : (
        <DataTable<Row>
          tableId="admin.languagePacks"
          data={rows} columns={columns} getRowId={r => r.code} pagination={false} columnMenu={false}
          loading={query.isPending} maxHeight={null}
          rowOptions={r => (deleting === r.code ? { state: 'dimmed' } : undefined)}
          renderCard={r => <PackCard row={r} t={t} stale={stale(r)} actions={<RowActions row={r} t={t} canConfig={canConfig} busy={deleting === r.code} onDelete={m => void removeRef.current(m)} />} />}
        />
      )}
      {uploading && <UploadDialog onClose={() => setUploading(false)} onUploaded={() => query.refetch()} />}
    </div>
  )
}

function PackCard({ row, t, stale, actions }: { row: Row; t: ReturnType<typeof useAdminT>; stale: boolean; actions: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-3 border-b px-3 py-3">
      <div className="min-w-0">
        <div className="truncate font-medium">{row.name} <span className="font-mono text-[13px] font-normal text-muted-foreground">{row.code}</span></div>
        <div className="text-xs text-muted-foreground">
          {row.builtin ? t('admin:languagePacks.source.builtin') : t('admin:languagePacks.source.uploaded')}
          {row.author && ` · ${row.author}`}
          {row.baseVersion && ` · ${row.baseVersion}`}
        </div>
        {stale && <StatusBadge className="mt-1" tone="warning" label={t('admin:languagePacks.stale')} />}
      </div>
      <div className="shrink-0">{actions}</div>
    </div>
  )
}

function RowActions({ row, t, canConfig, busy, onDelete }: { row: Row; t: ReturnType<typeof useAdminT>; canConfig: boolean; busy: boolean; onDelete: (m: LocaleMeta) => void }) {
  if (row.builtin) {
    // Disabled WITH its reason (aria-disabled keeps it focusable, so the tooltip is reachable): hiding the
    // button would leave "can I remove English?" unanswered.
    return (
      <IconButton label={t('common:actions.delete')} tooltip={t('admin:languagePacks.builtin_reason')} aria-disabled>
        <IconDelete />
      </IconButton>
    )
  }
  const meta = row.meta!
  return (
    <div className="flex items-center justify-end gap-1">
      <IconButton label={t('admin:languagePacks.download', { name: meta.name })} onClick={() => { void downloadPack(meta).catch(() => pushSnack(t('admin:languagePacks.toast.download_failed'), 'error')) }}>
        <IconDownload />
      </IconButton>
      {canConfig && (
        <MoreMenu items={[{ id: 'delete', label: t('common:actions.delete'), icon: IconDelete, destructive: true, disabled: busy, onSelect: () => onDelete(meta) }]} />
      )}
    </div>
  )
}
