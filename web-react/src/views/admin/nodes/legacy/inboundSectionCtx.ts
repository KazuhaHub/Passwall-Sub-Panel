import type { Dispatch, ReactNode, SetStateAction } from 'react'
import type { Theme } from '@mui/material'
import type { TFunction } from 'i18next'
import type { PanelType } from '@/api/servers'
import type { InboundFormState } from '../inbound/inboundModel'

/** What the legacy inbound form's sections read from InboundFormFields: its state, helpers and labels. */
export interface SectionCtx {
  form: InboundFormState
  update: <K extends keyof InboundFormState>(key: K, value: InboundFormState[K]) => void
  setForm: Dispatch<SetStateAction<InboundFormState>>
  advanced?: boolean
  t: TFunction
  md: Theme['palette']['md']
  sectionTitle: (text: string) => ReactNode
  fieldLabel: (text: string) => ReactNode
  switchControl: (label: string, checked: boolean, onChange: (c: boolean) => void, dangerous?: boolean) => ReactNode
  tlsCertFields: () => ReactNode
  protocolLabel: string
  effectivePanelType: PanelType
  realityFingerprintLocked: boolean
  canScanReality: boolean
  setRealityScannerOpen: (open: boolean) => void
  onGenKeys: () => void | Promise<void>
  onRandomRealitySite?: () => void
  onGenSSPassword: () => void
  genKeysBusy: boolean
  fetchingPanelCert: boolean
  fetchFromPanel: () => void | Promise<void>
  protocolReadonly?: boolean
  panelType?: PanelType
}
