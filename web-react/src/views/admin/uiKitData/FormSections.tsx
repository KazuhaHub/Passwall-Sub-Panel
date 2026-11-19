import { useState } from 'react'
import { LayoutListIcon, Rows3Icon } from 'lucide-react'
import { Checkbox } from '@/components/ui/checkbox'
import { Combobox, ComboboxContent, ComboboxEmpty, ComboboxInput, ComboboxItem, ComboboxList } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Field } from '@/components/form/Field'
import { FormSection } from '@/components/form/FormSection'
import { InheritanceField, SourceTrail } from '@/components/form/InheritanceField'
import { NumberField } from '@/components/form/NumberField'
import { OptionCards } from '@/components/form/OptionCards'
import { SaveBar } from '@/components/form/SaveBar'
import { SecretField } from '@/components/form/SecretField'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { TagsField } from '@/components/form/TagsField'
import { useFieldErrors } from '@/components/form/useFieldErrors'
import { PathDiagram } from '@/components/diagram/PathDiagram'
import { useUnsavedChanges } from '@/hooks/useUnsavedChanges'
import { Section } from './TableSections'

interface Draft {
  name: string
  host: string
  port: string
  region: string
  note: string
  token: string
  tags: string[]
  enabled: boolean
  hidden: boolean
  mtu: number | null
  trafficGb: number | null
  timeout: number | null
}

const INITIAL: Draft = {
  name: 'Irvine Home', host: 'irvine.example.com', port: '443', region: 'US-Irvine', note: '', token: '', tags: ['Premium', 'Streaming'],
  enabled: true, hidden: false, mtu: null, trafficGb: 0, timeout: 30,
}

const REGIONS = ['US-Irvine', 'JP-Tokyo', 'HK', 'SG', 'TW-Taipei', 'DE-Frankfurt']

export function FormsDemoSection() {
  const [saved, setSaved] = useState(INITIAL)
  const [d, setD] = useState(INITIAL)
  const [pending, setPending] = useState(false)
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setD(prev => ({ ...prev, [k]: v }))
  const dirty = JSON.stringify(d) !== JSON.stringify(saved)

  const fe = useFieldErrors<Draft>({
    name: x => (x.name.trim() ? null : '输入一个名称，例如 Irvine Home'),
    host: x => (/^[\w.-]+$/.test(x.host) ? null : '地址只能包含字母、数字、点和连字符，例如 irvine.example.com'),
    port: x => (/^\d+$/.test(x.port) && +x.port >= 1 && +x.port <= 65535 ? null : '输入 1 到 65535 之间的端口，例如 443'),
  })
  useUnsavedChanges(dirty)

  const save = () => {
    if (!fe.validateAll(d)) return
    setPending(true)
    setTimeout(() => { setSaved(d); setPending(false) }, 700)
  }

  return (
    <Section id="forms" title="表单" note="标签在上，说明在下；失焦才校验，错误替换说明；改动后底部出现保存条。试着清空名称再移开焦点，或把端口改成 99999。">
      <div className="flex max-w-2xl flex-col gap-8 rounded-xl border bg-card p-5">
        <FormSection title="基本信息" description="名称和地址，列表和订阅里都会用到。">
          <Field label="名称" required hint="显示在节点列表和订阅里" error={fe.errors.name}>
            <Input value={d.name} placeholder="Irvine Home" onChange={e => { set('name', e.target.value); fe.change('name', { ...d, name: e.target.value }) }} onBlur={() => fe.blur('name', d)} />
          </Field>
          <div className="grid gap-4 sm:grid-cols-[1fr_8rem]">
            <Field label="地址" required hint="域名或 IP" error={fe.errors.host}>
              <Input value={d.host} placeholder="irvine.example.com" onChange={e => { set('host', e.target.value); fe.change('host', { ...d, host: e.target.value }) }} onBlur={() => fe.blur('host', d)} />
            </Field>
            <Field label="端口" required error={fe.errors.port}>
              <Input inputMode="numeric" value={d.port} placeholder="443" onChange={e => { set('port', e.target.value); fe.change('port', { ...d, port: e.target.value }) }} onBlur={() => fe.blur('port', d)} />
            </Field>
          </div>
          <Field label="区域" hint="用于订阅里的国旗和分组">
            {p => (
              <Combobox items={REGIONS} value={d.region} onValueChange={v => set('region', (v as string | null) ?? '')}>
                <ComboboxInput id={p.id} aria-describedby={p['aria-describedby']} aria-invalid={p['aria-invalid']} placeholder="选择或搜索区域" showClear />
                <ComboboxContent>
                  <ComboboxEmpty>没有匹配的区域</ComboboxEmpty>
                  <ComboboxList>{(item: string) => <ComboboxItem key={item} value={item}>{item}</ComboboxItem>}</ComboboxList>
                </ComboboxContent>
              </Combobox>
            )}
          </Field>
          <Field label="标签" hint="回车或逗号添加；点候选标签也可以">
            {p => <TagsField id={p.id} aria-labelledby={p.labelId} aria-describedby={p['aria-describedby']} value={d.tags} onChange={v => set('tags', v)} suggestions={['Premium', 'HK', 'Streaming', 'Gaming', 'Trial']} />}
          </Field>
          <Field label="备注" hint="只有管理员能看到">
            <Textarea rows={2} value={d.note} onChange={e => set('note', e.target.value)} placeholder="例如：2026-11 迁到新机房" />
          </Field>
          <Field label="协议" hint="决定下面出现哪些字段">
            <NativeSelect defaultValue="vless"><option value="vless">VLESS</option><option value="ss">Shadowsocks 2022</option><option value="trojan">Trojan</option></NativeSelect>
          </Field>
          <Field label="启用" orientation="horizontal" hint="关闭后订阅里不再出现此节点">
            <Switch checked={d.enabled} onCheckedChange={v => set('enabled', v)} />
          </Field>
          <Field label="对订阅者隐藏直连线路" orientation="horizontal">
            <Checkbox checked={d.hidden} onCheckedChange={v => set('hidden', v === true)} />
          </Field>
        </FormSection>

        <FormSection title="限额与超时" description="空表示跟随上一层，0 是明确的取值。">
          <Field label="流量额度" hint="0 表示不限量；清空表示跟随分组">
            <NumberField value={d.trafficGb} onChange={v => set('trafficGb', v)} unit="GB" min={0} allowEmpty emptyLabel="跟随分组" zeroLabel="不限量" />
          </Field>
          <Field label="连接超时">
            <NumberField value={d.timeout} onChange={v => set('timeout', v)} unit="秒" min={1} max={600} integer />
          </Field>
        </FormSection>

        <FormSection title="高级选项" advanced sectionKey="ui-kit.forms.advanced" description="一般不用改。展开状态会被记住。">
          <Field label="MTU" hint="留空使用系统默认值">
            <NumberField value={d.mtu} onChange={v => set('mtu', v)} unit="字节" min={576} max={9000} integer allowEmpty emptyLabel="系统默认" />
          </Field>
          <Field label="访问令牌" hint="已保存的令牌不会再显示；留空表示不修改">
            <SecretField value={d.token} onChange={v => set('token', v)} isSet placeholder="粘贴新的令牌" />
          </Field>
          <Field label="订阅链接" hint="只读，点复制">
            <SecretField value="https://psp.example.com/sub/8f3a2c9d41" readOnly />
          </Field>
        </FormSection>

        <SaveBar
          dirty={dirty}
          pending={pending}
          disabled={fe.count > 0}
          message={fe.count > 0 ? `修正 ${fe.count} 处错误后才能保存` : undefined}
          onSave={save}
          onDiscard={() => { setD(saved); fe.reset() }}
        />
      </div>
    </Section>
  )
}

export function ControlsDemoSection() {
  const [view, setView] = useState<'list' | 'path'>('list')
  const [kind, setKind] = useState<'forward' | 'tunnel' | 'cdn' | 'external'>('forward')
  const [user, setUser] = useState<number | undefined>(undefined)
  const [groupSet, setGroupSet] = useState<number | undefined>(500)

  const levels = [
    { id: 'global', label: '全局', value: 100 as number | undefined },
    { id: 'group', label: '分组', value: groupSet },
    { id: 'user', label: '本人', value: user },
  ]
  const show = (v: number) => (v === 0 ? '不限量' : `${v} GB`)

  const solid = { style: 'solid' as const }
  const sample = (k: typeof kind) => {
    const u = { id: 'u', kind: 'user' as const, label: '用户' }
    const l = { id: 'l', kind: 'landing' as const, label: '落地' }
    if (k === 'forward') return { nodes: [u, { id: 'e', kind: 'entry' as const, label: '入口' }, l], edges: [solid, solid] }
    if (k === 'tunnel') return { nodes: [u, { id: 'e', kind: 'entry' as const, label: '入口' }, l], edges: [solid, { style: 'dashed' as const }] }
    if (k === 'cdn') return { nodes: [u, { id: 'c', kind: 'cdn' as const, label: 'CDN' }, l], edges: [solid, { style: 'solid' as const, name: '回源' }] }
    return { nodes: [u, { id: 'x', kind: 'external' as const, label: '中转机' }, l], edges: [solid, { style: 'solid' as const, name: '转发' }] }
  }
  const card = (k: typeof kind, title: string, description: string, extra: { disabled?: boolean; disabledReason?: string } = {}) => {
    const s = sample(k)
    return { value: k, title, description, diagram: <PathDiagram nodes={s.nodes} edges={s.edges} size="sm" />, ...extra }
  }

  return (
    <Section id="controls" title="选项卡片、分段控件、取值来源" note="类型用带小图的卡片选，不用下拉框。「跟随」和 0 是两种不同的东西。">
      <div className="grid gap-6 lg:grid-cols-2">
        <div className="flex flex-col gap-4 rounded-xl border bg-card p-5">
          <Field label="线路类型">
            {p => (
              <OptionCards
                aria-labelledby={p.labelId}
                value={kind}
                onChange={setKind}
                columns={2}
                options={[
                  card('forward', '端口转发', '入口原样转发，加密由落地负责，开销最低'),
                  card('tunnel', '加密隧道', '入口自己终结连接，再走加密隧道到落地'),
                  card('cdn', 'CDN', '由 CDN 回源，PSP 看不到中间这一跳', { disabled: true, disabledReason: '此节点没有可用的加速域名' }),
                  card('external', '外部入口', '转发由你在面板外配置，这里只登记地址'),
                ]}
              />
            )}
          </Field>
          <Field label="视图">
            {p => (
              <SegmentedControl
                aria-labelledby={p.labelId}
                value={view}
                onChange={setView}
                options={[
                  { value: 'list', label: '按线路', icon: <Rows3Icon /> },
                  { value: 'path', label: '按落地节点', icon: <LayoutListIcon /> },
                ]}
              />
            )}
          </Field>
        </div>
        <div className="flex flex-col gap-5 rounded-xl border bg-card p-5">
          <InheritanceField
            label="流量额度"
            levels={levels}
            editLevel="user"
            onChange={setUser}
            renderValue={show}
            defaultValue={100}
            hint="选择「在此设置」后可以填 0，表示不限量"
            renderEditor={({ id, value, onChange }) => <NumberField id={id} value={value} onChange={v => onChange(v ?? 0)} unit="GB" min={0} integer zeroLabel="不限量" />}
          />
          <div className="flex flex-col gap-2">
            <span className="text-sm font-medium">分组也可以单独设置（只读演示）</span>
            <SegmentedControl
              aria-label="分组额度"
              size="sm"
              value={groupSet === undefined ? 'follow' : 'set'}
              onChange={v => setGroupSet(v === 'follow' ? undefined : 500)}
              options={[{ value: 'follow', label: '分组跟随全局' }, { value: 'set', label: '分组设为 500 GB' }]}
            />
            <SourceTrail levels={levels} renderValue={show} />
          </div>
        </div>
      </div>
    </Section>
  )
}
