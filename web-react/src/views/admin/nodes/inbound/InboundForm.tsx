import { type Dispatch, type SetStateAction, useCallback, useEffect, useState } from 'react'
import { type Cert, listCerts } from '@/api/certs'
import type { Server } from '@/api/servers'
import { xrayRequiresMLKEMFirst } from '@/utils/xrayReality'
import { AdvancedSection } from './AdvancedSection'
import { ProtocolSection } from './ProtocolSection'
import { ProtocolSpecific } from './ProtocolSpecific'
import { RawJsonEditor } from './RawJsonEditor'
import { SecuritySection } from './SecuritySection'
import { TransportSection } from './TransportSection'
import type { SectionProps } from './controls'
import { canUseJson, panelTypeOf, snapshotRaw, xrayVersionOf } from './formRules'
import { type InboundFormState, usesVlessStream } from './inboundModel'

export { canUseJson, snapshotRaw }

export interface InboundFormProps {
  form: InboundFormState
  setForm: Dispatch<SetStateAction<InboundFormState>>
  /** The server the inbound lives on / is created on: capabilities (reality.scan), xray/core version, panel type. */
  server?: Server
  /** Create only: the writable servers. When given, the server picker shows; picking re-derives name/region/address/protocol/transport defaults. */
  servers?: Server[]
  protocolReadonly?: boolean
  /** JSON mode: the per-protocol sections are replaced by the three raw editors. The mode switch itself lives in the tab, not here. */
  json: boolean
  /** Called when picking a non-3X-UI server must leave JSON mode. */
  onLeaveJson?: () => void
  onGenKeys: () => void | Promise<void>
  onRandomRealitySite?: () => void
  onGenSSPassword: () => void
  genKeysBusy: boolean
}

/**
 * The inbound's own settings: where it listens, its transport and security, what only one protocol has,
 * and the advanced options. The name, address, region and tags are the General tab's, and the JSON switch
 * and the source-country rules are the tab's; this is controlled by the caller, which owns the state.
 */
export function InboundForm({ form, setForm, server, servers, protocolReadonly, json, onLeaveJson, onGenKeys, onRandomRealitySite, onGenSSPassword, genKeysBusy }: InboundFormProps) {
  // The server being created on wins over the one the caller passed, which is where an existing inbound lives.
  const selected = servers?.find(s => s.id === form.panel_id) ?? server
  const panelType = panelTypeOf(selected)
  const fingerprintLocked = xrayRequiresMLKEMFirst(xrayVersionOf(selected))
  const canScan = selected?.capabilities?.includes('reality.scan') ?? false

  const update = useCallback(<K extends keyof InboundFormState>(key: K, value: InboundFormState[K]) => {
    setForm(prev => ({ ...prev, [key]: value }))
  }, [setForm])

  // The managed certificates a "PSP-managed" source can bind. Loaded once; a failure leaves the list empty (the request layer says why).
  const [certs, setCerts] = useState<Cert[]>([])
  useEffect(() => { listCerts().then(setCerts).catch(() => {}) }, [])

  // The form forces the fingerprint itself, not only the select: the saved value may be anything, and a
  // locked select showing 'chrome' over a state of 'firefox' would save what it does not show.
  useEffect(() => {
    if (!fingerprintLocked) return
    setForm(prev => prev.reality_fingerprint === 'chrome' ? prev : { ...prev, reality_fingerprint: 'chrome' })
  }, [fingerprintLocked, setForm])

  const section: SectionProps = { form, setForm, update, panelType }
  return (
    <div className="flex flex-col gap-6">
      <ProtocolSection {...section} servers={servers} protocolReadonly={protocolReadonly} json={json} onLeaveJson={onLeaveJson} />
      {json ? <RawJsonEditor form={form} update={update} /> : (
        <>
          {usesVlessStream(form.protocol) && (
            <>
              <TransportSection {...section} />
              <SecuritySection {...section} certs={certs} fingerprintLocked={fingerprintLocked} canScan={canScan} scanSourceName={selected?.name}
                onGenKeys={onGenKeys} onRandomRealitySite={onRandomRealitySite} genKeysBusy={genKeysBusy} />
            </>
          )}
          <ProtocolSpecific {...section} certs={certs} onGenSSPassword={onGenSSPassword} />
          <AdvancedSection {...section} />
        </>
      )}
    </div>
  )
}
