import { useState } from 'react'
import { Box, Link, Popover } from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { DestPolicyRef } from '@/api/accessControl'

/** The "N policies" cell: a button that opens the list of who uses the list, each a link to its editor. */
export default function UsedByPopover({ usedBy, onOpenPolicy, onOpenAllowlist }: {
  usedBy: DestPolicyRef[]
  onOpenPolicy: (id: number) => void
  /** An allowlist group that uses the list: its card on the allowlist tab. */
  onOpenAllowlist: (groupId: number) => void
}) {
  const { t } = useTranslation(['admin'])
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const policies = usedBy.filter(u => u.kind === 'policy')
  const groups = usedBy.filter(u => u.kind === 'group')
  if (usedBy.length === 0) return <span>{t('admin:access_control.lists.usage.unused')}</span>
  return (
    <>
      <Link component="button" type="button" underline="hover" onClick={e => setAnchor(e.currentTarget)}
        sx={{ font: 'inherit', color: 'inherit' }}>
        {[
          policies.length > 0 ? t('admin:access_control.lists.usage.policies', { count: policies.length }) : null,
          groups.length > 0 ? t('admin:access_control.lists.usage.groups', { count: groups.length }) : null,
        ].filter(Boolean).join(' · ')}
      </Link>
      <Popover open={anchor !== null} anchorEl={anchor} onClose={() => setAnchor(null)}
        slotProps={{ paper: { sx: { maxWidth: 320, p: 1.5 } } }}>
        <Box component="ul" sx={{ m: 0, p: 0, listStyle: 'none', display: 'grid', gap: 0.5 }}>
          {policies.map(u => (
            <li key={`${u.kind}:${u.id}`}>
              <Link component="button" type="button" underline="hover" sx={{ font: 'inherit', textAlign: 'left' }}
                onClick={() => { setAnchor(null); onOpenPolicy(u.id) }}>
                {u.name || String(u.id)}
              </Link>
            </li>
          ))}
          {groups.map(u => (
            <li key={`${u.kind}:${u.id}`}>
              <Link component="button" type="button" underline="hover" sx={{ font: 'inherit', textAlign: 'left' }}
                onClick={() => { setAnchor(null); onOpenAllowlist(u.id) }}>
                {u.name || String(u.id)}
              </Link>
            </li>
          ))}
        </Box>
      </Popover>
    </>
  )
}
