import { useState } from 'react'
import { Box, Link, Popover } from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { DestPolicyRef } from '@/api/accessControl'

/** The "N policies" cell: a button that opens the list of who uses the list, each a link to its editor. */
export default function UsedByPopover({ usedBy, onOpenPolicy }: { usedBy: DestPolicyRef[]; onOpenPolicy: (id: number) => void }) {
  const { t } = useTranslation(['admin'])
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  if (usedBy.length === 0) return <span>{t('admin:access_control.lists.usage.unused')}</span>
  return (
    <>
      <Link component="button" type="button" underline="hover" onClick={e => setAnchor(e.currentTarget)}
        sx={{ font: 'inherit', color: 'inherit' }}>
        {t('admin:access_control.lists.usage.policies', { count: usedBy.length })}
      </Link>
      <Popover open={anchor !== null} anchorEl={anchor} onClose={() => setAnchor(null)}
        slotProps={{ paper: { sx: { maxWidth: 320, p: 1.5 } } }}>
        <Box component="ul" sx={{ m: 0, p: 0, listStyle: 'none', display: 'grid', gap: 0.5 }}>
          {usedBy.filter(u => u.kind === 'policy').map(u => (
            <li key={`${u.kind}:${u.id}`}>
              <Link component="button" type="button" underline="hover" sx={{ font: 'inherit', textAlign: 'left' }}
                onClick={() => { setAnchor(null); onOpenPolicy(u.id) }}>
                {u.name || String(u.id)}
              </Link>
            </li>
          ))}
        </Box>
      </Popover>
    </>
  )
}
