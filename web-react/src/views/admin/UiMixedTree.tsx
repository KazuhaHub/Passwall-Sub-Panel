import { Box, Button as MuiButton, Chip, IconButton, MenuItem, Select, Table as MuiTable, TableBody as MuiTableBody,
  TableCell as MuiTableCell, TableHead as MuiTableHead, TableRow as MuiTableRow, TextField, Typography } from '@mui/material'
import type { ReactNode } from 'react'
import SaveIcon from '@mui/icons-material/Save'
import DeleteIcon from '@mui/icons-material/Delete'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Card, CardAction, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import { NativeSelect } from '@/components/ui/native-select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar'
import LegacyBoundary from '@/components/LegacyBoundary'
import '@/styles/ui-dev.css'

// Dev-only proof of the legacy boundary (mounted at /__ui-kit/mixed, DEV builds
// only), in the REAL nesting shape:
//   shell (.psp-ui) > LegacyBoundary (page) > new PageHeaderLike
//     > LegacyBoundary (its actions slot) > MUI content + new components
//       > another PageHeaderLike > LegacyBoundary > MUI content
// scripts/check-coexistence.mjs loads this page and asserts the pairs match:
//   #mix-outside vs #mix-inside / #mix-deep / #mix-deeper   (old content must not
//       change at any depth inside the shell)
//   #mix-new-shell vs #mix-new-reenter / #mix-new-reenter2  (new components must
//       not change inside old content, at any depth)
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

// Several single- and multi-element new components: a card (header, content),
// a table, a dialog trigger, a native select, plus button / input / badge.
function NewComponents() {
  return (
    <>
      <Button>New button</Button>
      <Input defaultValue="new input" placeholder="placeholder" />
      <Badge>New badge</Badge>
      <NativeSelect defaultValue="a"><option value="a">Option A</option><option value="b">Option B</option></NativeSelect>
      <Card>
        <CardHeader><CardTitle>Card title</CardTitle></CardHeader>
        <CardContent><p>Card content with <b>bold</b>, <a href="#x">a link</a> and <code>code</code>.</p></CardContent>
      </Card>
      <Table>
        <TableHeader><TableRow><TableHead>Head</TableHead></TableRow></TableHeader>
        <TableBody><TableRow><TableCell>Cell</TableCell></TableRow></TableBody>
      </Table>
      <Dialog>
        <DialogTrigger render={<Button variant="outline" />}>Open dialog</DialogTrigger>
        <DialogContent>
          <DialogHeader><DialogTitle>Dialog</DialogTitle><DialogDescription>Closed in this fixture.</DialogDescription></DialogHeader>
        </DialogContent>
      </Dialog>
    </>
  )
}

// A new component with a slot that an un-migrated caller fills with MUI content,
// like a PageHeader's `actions`. Per the contract, the NEW component wraps the
// slot in LegacyBoundary.
function PageHeaderLike({ actions }: { actions: ReactNode }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Page header</CardTitle>
        <CardAction><LegacyBoundary>{actions}</LegacyBoundary></CardAction>
      </CardHeader>
    </Card>
  )
}

export default function UiMixedTree() {
  return (
    <div>
      <div id="mix-outside"><MuiSample /></div>
      <SidebarProvider className="psp-ui">
        <SidebarInset>
          <div id="mix-new-shell"><NewComponents /></div>
          <LegacyBoundary>
            <div id="mix-inside"><MuiSample /></div>
            <div id="mix-new-reenter"><NewComponents /></div>
            <PageHeaderLike
              actions={
                <>
                  <div id="mix-deep"><MuiSample /></div>
                  <div id="mix-new-reenter2"><NewComponents /></div>
                  <PageHeaderLike actions={<div id="mix-deeper"><MuiSample /></div>} />
                </>
              }
            />
          </LegacyBoundary>
        </SidebarInset>
      </SidebarProvider>
      {/* Makes ui-dev.css generate `font-mono`, which check-coexistence injects. */}
      <div hidden className="font-mono" />
      <div id="mix-ready" />
    </div>
  )
}
