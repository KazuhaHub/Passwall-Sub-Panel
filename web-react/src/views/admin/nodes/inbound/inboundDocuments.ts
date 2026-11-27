import type { PanelType } from '@/api/servers'
import type { InboundFormState } from './inboundModel'
import { buildSniffing, buildStreamSettings, settingsBuilderFor } from './inboundSettings'

/** The three JSON documents of an inbound: the raw editors in JSON mode, else what the structured form builds. */
export function inboundDocuments(f: InboundFormState, panelType: PanelType, json: boolean) {
  return {
    settings: json ? (f.raw_settings || '{}') : JSON.stringify(settingsBuilderFor(f.protocol)(f)),
    stream_settings: json ? (f.raw_stream_settings || '{}') : JSON.stringify(buildStreamSettings(f)),
    // S-UI has no sniffing section; its inbound carries an empty document.
    sniffing: json ? (f.raw_sniffing || '{}') : panelType === '3xui' ? JSON.stringify(buildSniffing(f)) : '{}',
  }
}
