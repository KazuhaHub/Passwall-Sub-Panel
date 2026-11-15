import { Box, Chip, Drawer, IconButton, Skeleton, Typography, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import { useTranslation } from 'react-i18next'

import type { LegalKind } from '@/api/legal'
import { useAdminLegal } from '@/query/legal'
import { useQueryScope } from '@/query/useQueryScope'

/** Every version of one document, newest first. Text is not shown: a version is
 *  immutable and the list is for telling which were material changes. */
export default function LegalHistorySheet({ kind, onClose }: { kind: LegalKind; onClose: () => void }) {
  const md = useTheme().palette.md
  const { t, i18n } = useTranslation('admin')
  const scope = useQueryScope()
  const q = useAdminLegal(scope, kind)
  const doc = t(`auth:legal.kind.${kind}`)
  const versions = [...(q.data?.versions ?? [])].sort((a, b) =>
    b.published_at.localeCompare(a.published_at) || b.id - a.id)

  return (
    <Drawer anchor="right" open onClose={onClose}
      slotProps={{ paper: { role: 'dialog', 'aria-labelledby': 'legal-history-title', sx: { width: { xs: '100%', sm: 400 }, p: 2 } } as object }}>
      <Box sx={{ display: 'flex', alignItems: 'center', mb: 2 }}>
        <Typography id="legal-history-title" component="h2" sx={{ flex: 1, fontSize: 18, fontWeight: 600 }}>
          {t('settings.legal.history_sheet.title', { doc })}
        </Typography>
        <IconButton aria-label={t('settings.legal.editor.close')} onClick={onClose}><CloseIcon /></IconButton>
      </Box>
      {q.isLoading && [0, 1, 2].map(i => <Skeleton key={i} variant="rounded" height={48} sx={{ mb: 1 }} />)}
      {!q.isLoading && versions.length === 0 && (
        <Typography sx={{ color: md.onSurfaceVariant }}>{t('settings.legal.history_sheet.empty')}</Typography>
      )}
      <Box component="ul" sx={{ listStyle: 'none', m: 0, p: 0 }}>
        {versions.map(v => (
          <Box component="li" key={v.id} sx={{ py: 1, borderTop: `1px solid ${md.outlineVariant}`, display: 'flex', alignItems: 'center', gap: 1 }}>
            <Box sx={{ flex: 1 }}>
              <Typography sx={{ fontSize: 14, fontWeight: 500 }}>{t('settings.legal.history_sheet.row', { locale: v.locale, version: v.version })}</Typography>
              <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
                {new Date(v.published_at).toLocaleString(i18n.language)}
              </Typography>
            </Box>
            {v.consent_bump && <Chip size="small" label={t('settings.legal.history_sheet.major')} />}
          </Box>
        ))}
      </Box>
    </Drawer>
  )
}
