import { Box, Button as MuiButton, Chip, IconButton, MenuItem, Select, Table as MuiTable, TableBody as MuiTableBody,
  TableCell as MuiTableCell, TableHead as MuiTableHead, TableRow as MuiTableRow, TextField, Typography } from '@mui/material'
import SaveIcon from '@mui/icons-material/Save'
import DeleteIcon from '@mui/icons-material/Delete'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar'
import LegacyBoundary from '@/components/LegacyBoundary'
import '@/styles/ui-dev.css'

// Dev-only proof of the legacy boundary (mounted at /__ui-kit/mixed, DEV builds
// only). The same MUI sample is rendered outside any new wrapper and inside
// SidebarProvider > SidebarInset > LegacyBoundary, and a few new components are
// rendered directly in the shell and re-entering inside the legacy content.
// scripts/check-coexistence.mjs loads this page and asserts the pairs match:
//   #mix-outside   vs #mix-inside      (old content must not change in a new shell)
//   #mix-new-shell vs #mix-new-reenter (new components must not change in old content)
// Hard-coded English: nothing here ships.

const PIXEL = 'data:image/gif;base64,R0lGODlhAQABAAAAACwAAAAAAQABAAACAUwAOw=='

// MUI components plus raw elements with nothing on them: the raw ones are where a
// leaking reset shows first (MUI classes only win where MUI styles a property).
function MuiSample() {
  return (
    <Box>
      <Typography variant="h2">Heading</Typography>
      <Typography>Body text</Typography>
      <TextField label="Field" size="small" defaultValue="value" />
      <MuiButton variant="contained" startIcon={<SaveIcon />}>Save</MuiButton>
      <IconButton aria-label="delete"><DeleteIcon /></IconButton>
      <Chip label="Chip" />
      <MuiTable size="small">
        <MuiTableHead><MuiTableRow><MuiTableCell>Head</MuiTableCell></MuiTableRow></MuiTableHead>
        <MuiTableBody><MuiTableRow><MuiTableCell>Cell</MuiTableCell></MuiTableRow></MuiTableBody>
      </MuiTable>
      <Select value="a" size="small"><MenuItem value="a">Option A</MenuItem></Select>
      <a href="#raw">raw a</a>
      <ul><li>raw li</li></ul>
      <p>raw p</p>
      <img alt="" src={PIXEL} />
      <canvas width={20} height={10} />
      <table><tbody><tr><td>raw td</td></tr></tbody></table>
      <b>raw b</b>
      <span>raw span</span>
    </Box>
  )
}

function NewComponents() {
  return (
    <div>
      <Button>New button</Button>
      <Input defaultValue="new input" />
      <Badge>New badge</Badge>
    </div>
  )
}

export default function UiMixedTree() {
  return (
    <div>
      <div id="mix-outside"><MuiSample /></div>
      <SidebarProvider>
        <SidebarInset>
          <div id="mix-new-shell"><NewComponents /></div>
          <LegacyBoundary>
            <div id="mix-inside"><MuiSample /></div>
            <div id="mix-new-reenter"><NewComponents /></div>
          </LegacyBoundary>
        </SidebarInset>
      </SidebarProvider>
      <div id="mix-ready" />
    </div>
  )
}
