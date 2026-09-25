import { Dialog, useTheme, type DialogProps } from '@mui/material'

type Props = Omit<DialogProps, 'slotProps'> & {
  paperWidth: number
  paperHeight?: number | string
  viewportWidth?: string
}

export default function RuleEditorDialog({ paperWidth, paperHeight, viewportWidth = '96vw', children, ...props }: Props) {
  const md = useTheme().palette.md

  return (
    <Dialog
      maxWidth={false}
      {...props}
      slotProps={{
        paper: {
          sx: {
            borderRadius: 3,
            bgcolor: md.surfaceContainerHigh,
            width: paperWidth,
            height: paperHeight,
            maxWidth: viewportWidth,
            maxHeight: 'calc(100vh - 64px)',
          },
        },
      }}
    >
      {children}
    </Dialog>
  )
}
