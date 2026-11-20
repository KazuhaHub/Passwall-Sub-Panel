import { OptionCards, type OptionCardOption } from '@/components/form/OptionCards'
import { useNodesT } from '../shared/useNodesT'
import { CardDiagram } from './LineDiagram'
import type { LineKind } from './lineModel'

const KINDS: LineKind[] = ['forward', 'tunnel', 'cdn', 'external']

/**
 * The four kinds of line for a NEW line (design 4.4.1), each with its picture and why it is off when it is.
 * `entryServers` is how many Passwall Node servers other than the landing's own exist: forward and tunnel both
 * enter through one. A tunnel line also needs the landing itself on a Passwall Node server.
 */
export function TypeCards({ value, onChange, entryServers, landingIsNative, labelledBy }: {
  value: LineKind | null
  onChange: (kind: LineKind) => void
  entryServers: number
  landingIsNative: boolean
  labelledBy: string
}) {
  const t = useNodesT()
  const options: OptionCardOption<LineKind>[] = KINDS.map(kind => {
    const off = kind === 'forward' ? entryServers === 0 : kind === 'tunnel' ? !landingIsNative || entryServers === 0 : false
    const reason = kind === 'tunnel' && !landingIsNative ? t('nodes.line.card.not_native_landing') : t('nodes.line.card.no_server')
    return { value: kind, title: t(`nodes.type.${kind}`), description: t(`nodes.line.card.${kind}`), diagram: <CardDiagram kind={kind} />, disabled: off, disabledReason: off ? reason : undefined }
  })
  return <OptionCards value={value} onChange={onChange} options={options} columns={2} aria-labelledby={labelledBy} />
}
