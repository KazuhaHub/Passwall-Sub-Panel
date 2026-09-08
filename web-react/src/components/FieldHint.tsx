import { useState } from 'react'
import { Link, Popover, Typography } from '@mui/material'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'

/**
 * A one-glance verdict under a form field, with the reasoning one click away.
 *
 * The reasoning genuinely has to be there — an operator who reads "void
 * fleet-wide" and cannot find out WHICH panel is left trusting the form
 * blindly or ignoring it, and both make the warning useless. But a full
 * sentence rendered inline reflows the field it belongs to and pushes the
 * form around as the admin types, which is how a warning stops being read for
 * the opposite reason.
 *
 * So the summary is always visible and the detail is on demand. Click rather
 * than hover: a hover-only detail is unreachable on touch, and this is the
 * half that carries the panel names.
 */
export default function FieldHint({
  summary,
  detail,
  tone = 'warning',
}: {
  summary: string
  detail: string
  /** 'warning' for a broken or misleading cap; 'muted' for merely informational. */
  tone?: 'warning' | 'muted'
}) {
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const color = tone === 'warning' ? 'warning.main' : 'text.secondary'
  return (
    <>
      <Link
        component="button"
        // Explicit: this lives inside a form, and a button with no type
        // submits it. A hint that saves the dialog when clicked would be a
        // far worse bug than the layout problem it was added to fix.
        type="button"
        underline="hover"
        onClick={e => { e.preventDefault(); setAnchor(e.currentTarget) }}
        sx={{
          color,
          // Inherit the helper text's metrics so the line does not change
          // height when a hint appears.
          font: 'inherit',
          display: 'inline-flex',
          alignItems: 'center',
          gap: 0.25,
          verticalAlign: 'baseline',
          textAlign: 'left',
        }}
      >
        {summary}
        <InfoOutlinedIcon sx={{ fontSize: 13 }} />
      </Link>
      <Popover
        open={Boolean(anchor)}
        anchorEl={anchor}
        onClose={() => setAnchor(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'left' }}
        slotProps={{ paper: { sx: { maxWidth: 380, p: 1.5 } } }}
      >
        <Typography variant="body2" sx={{ lineHeight: 1.6 }}>{detail}</Typography>
      </Popover>
    </>
  )
}
