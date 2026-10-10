import { useEffect, useState } from 'react'
import { Accordion, AccordionDetails, AccordionSummary, Alert, Box, Button, Drawer, IconButton, Skeleton, Stack, Typography } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import { useTranslation } from 'react-i18next'
import { getLegalHistory, type DataCollection, type LegalAdminDocument, type LegalKind } from '@/api/legal'
import LegalDocument from '@/components/LegalDocument'

export default function LegalHistorySheet({ kind, collection, onClose }: { kind: LegalKind; collection: DataCollection | null; onClose: () => void }) {
  const { t } = useTranslation('admin')
  const [items, setItems] = useState<LegalAdminDocument[]>([])
  const [next, setNext] = useState(0)
  const [loading, setLoading] = useState(true)
  const [failed, setFailed] = useState(false)
  const [request, setRequest] = useState({ cursor: 0, attempt: 0 })
  useEffect(() => {
    const controller = new AbortController()
    setLoading(true); setFailed(false)
    void getLegalHistory(kind, request.cursor, controller.signal).then(page => {
      if (controller.signal.aborted) return
      setItems(previous => request.cursor === 0 ? page.items : [...previous, ...page.items.filter(item => !previous.some(old => old.id === item.id))])
      setNext(page.next_before_id)
    }).catch(() => { if (!controller.signal.aborted) setFailed(true) }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [kind, request])
  return <Drawer anchor="right" open onClose={onClose} slotProps={{ paper: { sx: { width: { xs: '100%', sm: 600 }, p: { xs: 2, sm: 3 } } } }}>
    <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between' }}>
      <Typography component="h2" variant="h6">{t('legal.history_title', { name: t(`legal.${kind}`) })}</Typography>
      <IconButton aria-label={t('legal.close')} onClick={onClose} sx={{ minWidth: 44, minHeight: 44 }}><CloseIcon /></IconButton>
    </Stack>
    <Box sx={{ mt: 2 }}>
      {items.map(item => <Accordion key={item.id} disableGutters>
        <AccordionSummary expandIcon={<ExpandMoreIcon />} sx={{ minHeight: 56 }}><Stack><Typography>{item.locale} · v{item.version}</Typography><Typography variant="caption">{new Date(item.published_at).toLocaleString()}</Typography></Stack></AccordionSummary>
        <AccordionDetails>
          {item.consent_bump && <Typography variant="caption">{t('legal.major')}</Typography>}
          {collection ? <LegalDocument document={{ ...item, consent_version: 0, data_collection: collection }} /> : <Alert severity="warning">{t('legal.collection_failed')}</Alert>}
        </AccordionDetails>
      </Accordion>)}
      {loading && <Box role="status" aria-label={t('legal.loading')}><Skeleton height={64} /><Skeleton height={64} /><Skeleton height={64} /></Box>}
      {!loading && failed && <Alert severity="error" action={<Button onClick={() => setRequest(value => ({ ...value, attempt: value.attempt + 1 }))}>{t('legal.retry')}</Button>}>{t('legal.load_failed')}</Alert>}
      {!loading && !failed && items.length === 0 && <Typography sx={{ py: 4 }}>{t('legal.history_empty')}</Typography>}
      {!loading && !failed && next > 0 && <Button onClick={() => setRequest(value => ({ cursor: next, attempt: value.attempt + 1 }))} sx={{ minHeight: 44, mt: 2 }}>{t('legal.load_more')}</Button>}
    </Box>
  </Drawer>
}
