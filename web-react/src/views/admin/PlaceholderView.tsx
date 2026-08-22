import { Box, Card, CardContent, Typography } from '@mui/material'
import ConstructionIcon from '@mui/icons-material/Construction'
import { useLocation } from 'react-router'
import { useTranslation } from 'react-i18next'
import { useTheme } from '@mui/material/styles'

// Temporary stub for any admin page that hasn't been migrated yet.
// Used while we move pages over from the old Vue app one at a time.
export default function PlaceholderView() {
  const location = useLocation()
  const { t } = useTranslation('common')
  const theme = useTheme()
  const md = theme.palette.md

  return (
    <Box sx={{ p: 3 }}>
      <Card sx={{ border: `1px solid ${md.outlineVariant}`, bgcolor: md.surface }}>
        <CardContent sx={{ display: 'flex', gap: 2, alignItems: 'flex-start', p: 3 }}>
          <Box sx={{
            width: 48, height: 48, borderRadius: '50%',
            display: 'grid', placeItems: 'center',
            bgcolor: md.primaryContainer, color: md.onPrimaryContainer,
          }}>
            <ConstructionIcon />
          </Box>
          <Box>
            <Typography variant="h6">{t('placeholder.title', { defaultValue: '页面待迁移' })}</Typography>
            <Typography variant="body2" sx={{ mt: 0.5 }}>
              {t('placeholder.body', { defaultValue: '此页面尚未从 Vue 版本迁移到 React + MUI。当前路径：' })}
              <code style={{ marginLeft: 6 }}>{location.pathname}</code>
            </Typography>
          </Box>
        </CardContent>
      </Card>
    </Box>
  )
}
