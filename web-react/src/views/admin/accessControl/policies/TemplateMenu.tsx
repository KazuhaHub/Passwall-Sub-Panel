import { useState } from 'react'
import { Button, Divider, ListItemText, ListSubheader, Menu, MenuItem } from '@mui/material'
import ArrowDropDownIcon from '@mui/icons-material/ArrowDropDown'
import { useTranslation } from 'react-i18next'

import type { DestCatalog, DestTemplate } from '@/api/accessControl'
import { pickLocalized } from './templates'

const T = 'admin:access_control.templates.'

/** "New policy ▾": a blank one, the catalog's rule groups, and why some have none. (MUI has no split button; the lists tab's "New list" is the same Button + Menu.) */
export default function TemplateMenu({ catalog, onBlank, onUse, onShowUnavailable }: {
  catalog: DestCatalog | undefined
  onBlank: () => void
  onUse: (tpl: DestTemplate) => void
  onShowUnavailable: () => void
}) {
  const { t, i18n } = useTranslation(['admin'])
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const close = () => setAnchor(null)
  return (
    <>
      <Button variant="contained" endIcon={<ArrowDropDownIcon />} aria-haspopup="menu" onClick={e => setAnchor(e.currentTarget)}>
        {t('admin:access_control.policies.new')}
      </Button>
      <Menu anchorEl={anchor} open={anchor !== null} onClose={close}>
        <MenuItem onClick={() => { close(); onBlank() }}><ListItemText>{t('admin:access_control.policies.new_blank')}</ListItemText></MenuItem>
        <Divider />
        <ListSubheader>{t(`${T}menu_heading`)}</ListSubheader>
        {(catalog?.templates ?? []).map(tpl => (
          <MenuItem key={tpl.key} onClick={() => { close(); onUse(tpl) }}>
            <ListItemText primary={pickLocalized(tpl.name, i18n.language)} secondary={tpl.added ? t(`${T}added`) : undefined} />
          </MenuItem>
        ))}
        {(catalog?.unavailable.length ?? 0) > 0 && <Divider />}
        {(catalog?.unavailable.length ?? 0) > 0 && (
          <MenuItem onClick={() => { close(); onShowUnavailable() }}><ListItemText>{t(`${T}why_none`)}</ListItemText></MenuItem>
        )}
      </Menu>
    </>
  )
}
