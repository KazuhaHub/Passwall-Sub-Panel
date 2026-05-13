<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Delete, Lock, Unlock } from '@element-plus/icons-vue'
import {
  createInbound,
  deleteNode,
  generateRealityKeypair,
  importNode,
  listNodes,
  listUnmanagedInbounds,
  setNodeEnabled,
  updateNodeMetadata,
} from '@/api/nodes'
import { listServers, type Server } from '@/api/servers'
import type { Node, UnmanagedInbound } from '@/api/types'

const tab = ref<'managed' | 'unmanaged'>('managed')
const managed = ref<Node[]>([])
const unmanaged = ref<UnmanagedInbound[]>([])
const servers = ref<Server[]>([])
const loading = ref(false)
const enabledBusy = ref<Record<number, boolean>>({})
const selectedManaged = ref<Node[]>([])
const batchBusy = ref<'enable' | 'disable' | 'delete' | ''>('')
const selectedManagedCount = computed(() => selectedManaged.value.length)

async function loadServers() {
  try {
    servers.value = await listServers()
  } catch {
    servers.value = []
  }
}

const importDialog = ref(false)
const importBusy = ref(false)
const importForm = reactive({
  panel_id: 0,
  panel_name: '',
  inbound_id: 0,
  display_name: '',
  server_address: '',
  region: '',
  tags_text: '',
  sort_order: 100,
})

const editDialog = ref(false)
const editBusy = ref(false)
const editing = ref<Node | null>(null)
const editForm = reactive({
  display_name: '',
  server_address: '',
  region: '',
  tags_text: '',
  sort_order: 0,
})

const createDialog = ref(false)
const createBusy = ref(false)
const createForm = reactive({
  panel_id: 0,
  display_name: '',
  server_address: '',
  region: '',
  tags_text: '',
  sort_order: 100,
  port: 443,
  reality_dest: 'yahoo.com:443',
  reality_server_name: 'yahoo.com',
  private_key: '',
  public_key: '',
  short_id: '',
})

function openCreateCheckServers() {
  if (servers.value.length === 0) {
    ElMessage.warning('请先到「服务器」页面添加 3X-UI 服务器')
    return
  }
  openCreate()
}

function openCreate() {
  createForm.panel_id = servers.value[0]?.id ?? 0
  createForm.display_name = ''
  createForm.server_address = ''
  createForm.region = ''
  createForm.tags_text = 'reality'
  createForm.sort_order = 100
  createForm.port = 443
  createForm.reality_dest = 'yahoo.com:443'
  createForm.reality_server_name = 'yahoo.com'
  createForm.private_key = ''
  createForm.public_key = ''
  createForm.short_id = ''
  createDialog.value = true
}

async function genKeys() {
  const kp = await generateRealityKeypair()
  createForm.private_key = kp.private_key
  createForm.public_key = kp.public_key
  createForm.short_id = kp.short_id
  ElMessage.success('Reality 密钥已生成')
}

async function submitCreate() {
  if (!createForm.display_name || !createForm.server_address || !createForm.region) {
    ElMessage.warning('显示名 / 服务器地址 / region 必填')
    return
  }
  if (!createForm.private_key || !createForm.public_key) {
    ElMessage.warning('请先点"生成 Reality 密钥"')
    return
  }
  createBusy.value = true
  try {
    const settings = JSON.stringify({
      clients: [],
      decryption: 'none',
      fallbacks: [],
    })
    const streamSettings = JSON.stringify({
      network: 'tcp',
      security: 'reality',
      externalProxy: [],
      realitySettings: {
        show: false,
        xver: 0,
        dest: createForm.reality_dest,
        serverNames: [createForm.reality_server_name],
        privateKey: createForm.private_key,
        minClient: '',
        maxClient: '',
        maxTimediff: 0,
        shortIds: [createForm.short_id],
        settings: {
          publicKey: createForm.public_key,
          fingerprint: 'chrome',
          serverName: '',
          spiderX: '/',
        },
      },
    })
    const sniffing = JSON.stringify({
      enabled: true,
      destOverride: ['http', 'tls', 'quic'],
      metadataOnly: false,
      routeOnly: false,
    })

    const res = await createInbound({
      panel_id: createForm.panel_id,
      display_name: createForm.display_name,
      server_address: createForm.server_address,
      region: createForm.region,
      tags: createForm.tags_text
        ? createForm.tags_text.split(',').map((t) => t.trim()).filter(Boolean)
        : [],
      sort_order: createForm.sort_order,
      inbound: {
        remark: createForm.display_name,
        enable: true,
        listen: '',
        port: createForm.port,
        protocol: 'vless',
        settings,
        stream_settings: streamSettings,
        sniffing,
        allocate: '',
      },
    })
    ElMessage.success('queued' in res ? '已加入同步任务' : '已创建')
    createDialog.value = false
    tab.value = 'managed'
    await load()
  } finally {
    createBusy.value = false
  }
}

async function load() {
  loading.value = true
  try {
    if (tab.value === 'managed') {
      managed.value = await listNodes()
      selectedManaged.value = []
    } else {
      const res = await listUnmanagedInbounds()
      unmanaged.value = res.items
    }
  } finally {
    loading.value = false
  }
}

function handleManagedSelectionChange(rows: Node[]) {
  selectedManaged.value = rows
}

function startImport(row: UnmanagedInbound) {
  importForm.panel_id = row.PanelID
  importForm.panel_name = row.PanelName
  importForm.inbound_id = row.InboundID
  importForm.display_name = row.Remark || `${row.Protocol}:${row.Port}`
  importForm.server_address = ''
  importForm.region = ''
  importForm.tags_text = ''
  importForm.sort_order = 100
  importDialog.value = true
}

async function submitImport() {
  if (!importForm.server_address || !importForm.region) {
    ElMessage.warning('请填写服务器地址和 region')
    return
  }
  importBusy.value = true
  try {
    await importNode({
      panel_id: importForm.panel_id,
      inbound_id: importForm.inbound_id,
      display_name: importForm.display_name,
      server_address: importForm.server_address,
      region: importForm.region,
      tags: importForm.tags_text
        ? importForm.tags_text.split(',').map((t) => t.trim()).filter(Boolean)
        : [],
      sort_order: importForm.sort_order,
    })
    ElMessage.success('已纳管')
    importDialog.value = false
    tab.value = 'managed'
    await load()
  } finally {
    importBusy.value = false
  }
}

function openEdit(row: Node) {
  editing.value = row
  editForm.display_name = row.display_name
  editForm.server_address = row.server_address
  editForm.region = row.region
  editForm.tags_text = (row.tags ?? []).join(', ')
  editForm.sort_order = row.sort_order
  editDialog.value = true
}

async function submitEdit() {
  if (!editing.value) return
  if (!editForm.display_name || !editForm.region) {
    ElMessage.warning('显示名和 region 必填')
    return
  }
  editBusy.value = true
  try {
    await updateNodeMetadata(editing.value.id, {
      display_name: editForm.display_name,
      server_address: editForm.server_address,
      region: editForm.region,
      tags: editForm.tags_text
        ? editForm.tags_text.split(',').map((t) => t.trim()).filter(Boolean)
        : [],
      sort_order: editForm.sort_order,
    })
    ElMessage.success('已保存')
    editDialog.value = false
    await load()
  } finally {
    editBusy.value = false
  }
}

async function confirmDelete(row: Node) {
  await ElMessageBox.confirm(
    `确定删除节点 ${row.display_name}？会先清除该 inbound 内所有本系统纳管 client，再删除 inbound 本身（要求 inbound 内只剩纳管 client）。`,
    '确认删除',
    { type: 'warning' },
  )
  await deleteNode(row.id)
  ElMessage.success('已删除')
  await load()
}

async function batchSetManagedEnabled(enabled: boolean) {
  if (selectedManaged.value.length === 0) return
  const rows = selectedManaged.value.slice()
  batchBusy.value = enabled ? 'enable' : 'disable'
  try {
    const results = await Promise.allSettled(rows.map((row) => setNodeEnabled(row.id, enabled)))
    const failed = results.filter((result) => result.status === 'rejected').length
    if (failed > 0) {
      ElMessage.warning(`已${enabled ? '启用' : '禁用'} ${rows.length - failed} 个节点，失败 ${failed} 个`)
    } else {
      ElMessage.success(`已${enabled ? '启用' : '禁用'} ${rows.length} 个节点`)
    }
    await load()
  } finally {
    batchBusy.value = ''
  }
}

async function batchDeleteManaged() {
  if (selectedManaged.value.length === 0) return
  const rows = selectedManaged.value.slice()
  const names = rows.slice(0, 5).map((row) => row.display_name).join('、')
  const suffix = rows.length > 5 ? ` 等 ${rows.length} 个节点` : ''
  try {
    await ElMessageBox.confirm(
      `确定删除 ${names}${suffix}？会先清除这些 inbound 内所有本系统纳管 client，再删除 inbound 本身。`,
      '批量删除节点',
      { type: 'warning' },
    )
  } catch {
    return
  }
  batchBusy.value = 'delete'
  try {
    const results = await Promise.allSettled(rows.map((row) => deleteNode(row.id)))
    const deletedRows = rows.filter((_, index) => results[index].status === 'fulfilled')
    const failed = rows.length - deletedRows.length
    managed.value = managed.value.filter((node) => !deletedRows.some((row) => row.id === node.id))
    selectedManaged.value = []
    if (failed > 0) {
      ElMessage.warning(`已删除 ${deletedRows.length} 个节点，失败 ${failed} 个`)
    } else {
      ElMessage.success(`已删除 ${deletedRows.length} 个节点`)
    }
  } finally {
    batchBusy.value = ''
  }
}

async function changeEnabled(row: Node, enabled: boolean) {
  const previous = row.enabled
  row.enabled = enabled
  enabledBusy.value = { ...enabledBusy.value, [row.id]: true }
  try {
    await setNodeEnabled(row.id, enabled)
    if (editing.value?.id === row.id) {
      editing.value.enabled = enabled
    }
    await load()
  } catch (e: any) {
    row.enabled = previous
    if (editing.value?.id === row.id) {
      editing.value.enabled = previous
    }
    ElMessage.error(e?.response?.data?.error ?? e?.message ?? '切换失败')
  } finally {
    enabledBusy.value = { ...enabledBusy.value, [row.id]: false }
  }
}

function onRowEnabledChange(row: Node, value: boolean | string | number) {
  void changeEnabled(row, Boolean(value))
}

function onEditingEnabledChange(value: boolean | string | number) {
  if (!editing.value) return
  void changeEnabled(editing.value, Boolean(value))
}

onMounted(async () => {
  await loadServers()
  await load()
})
</script>

<template>
  <div class="psp-page">
    <div class="psp-page-header">
      <div class="psp-page-title">节点管理</div>
      <el-button type="primary" @click="openCreateCheckServers">新增 inbound (VLESS+Reality)</el-button>
    </div>

    <el-tabs v-model="tab" @tab-change="load">
      <el-tab-pane label="纳管中" name="managed">
        <div v-if="selectedManagedCount > 0" class="psp-toolbar">
          <span class="selection-count">已选 {{ selectedManagedCount }}</span>
          <el-button
            :icon="Unlock"
            :loading="batchBusy === 'enable'"
            :disabled="batchBusy !== ''"
            @click="batchSetManagedEnabled(true)"
          >
            批量启用
          </el-button>
          <el-button
            :icon="Lock"
            :loading="batchBusy === 'disable'"
            :disabled="batchBusy !== ''"
            @click="batchSetManagedEnabled(false)"
          >
            批量禁用
          </el-button>
          <el-button
            type="danger"
            :icon="Delete"
            :loading="batchBusy === 'delete'"
            :disabled="batchBusy !== ''"
            @click="batchDeleteManaged"
          >
            批量删除
          </el-button>
        </div>
        <el-table v-loading="loading" :data="managed" stripe @selection-change="handleManagedSelectionChange">
          <el-table-column type="selection" width="48" />
          <el-table-column prop="display_name" label="显示名" min-width="180" />
          <el-table-column prop="server_address" label="服务器" min-width="200" />
          <el-table-column prop="region" label="Region" width="80" />
          <el-table-column label="Tags" min-width="180">
            <template #default="{ row }">
              <el-tag v-for="t in row.tags" :key="t" size="small" style="margin-right: 4px">
                {{ t }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="服务器 / inbound" min-width="160">
            <template #default="{ row }">
              {{ row.panel_name }} / {{ row.inbound_id }}
            </template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-switch
                :model-value="row.enabled"
                :loading="enabledBusy[row.id]"
                @change="onRowEnabledChange(row, $event)"
              />
            </template>
          </el-table-column>
          <el-table-column label="操作" width="220">
            <template #default="{ row }">
              <el-button size="small" type="primary" @click="openEdit(row)">编辑</el-button>
              <el-button size="small" type="danger" @click="confirmDelete(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="未纳管" name="unmanaged">
        <el-table v-loading="loading" :data="unmanaged" stripe>
          <el-table-column prop="PanelName" label="服务器" width="120" />
          <el-table-column prop="InboundID" label="inbound" width="100" />
          <el-table-column prop="Protocol" label="协议" width="100" />
          <el-table-column prop="Port" label="端口" width="80" />
          <el-table-column prop="Remark" label="3X-UI 备注" min-width="240" />
          <el-table-column label="client 数" width="100">
            <template #default="{ row }">{{ row.ClientCount }}</template>
          </el-table-column>
          <el-table-column label="操作" width="120">
            <template #default="{ row }">
              <el-button type="primary" size="small" @click="startImport(row)">纳管</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="createDialog" title="新增 inbound (VLESS + Reality)" width="640px" top="6vh">
      <el-form label-width="140px">
        <el-divider content-position="left">服务器与端口</el-divider>
        <el-form-item label="服务器" required>
          <el-select v-model="createForm.panel_id" style="width: 100%" placeholder="选择 3X-UI 服务器">
            <el-option
              v-for="s in servers"
              :key="s.id"
              :label="`${s.name} — ${s.url}`"
              :value="s.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="监听端口" required>
          <el-input-number v-model="createForm.port" :min="1" :max="65535" />
        </el-form-item>

        <el-divider content-position="left">显示与分组</el-divider>
        <el-form-item label="显示名" required>
          <el-input v-model="createForm.display_name" placeholder="🇹🇼 Static (Reality)" />
        </el-form-item>
        <el-form-item label="服务器地址" required>
          <el-input v-model="createForm.server_address" placeholder="hinet.example.com" />
          <div style="color: var(--text-muted); font-size: 12px; margin-top: 4px">
            朋友连接时实际拨号的公网域名 / IP
          </div>
        </el-form-item>
        <el-form-item label="Region" required>
          <el-input v-model="createForm.region" placeholder="TW / US / HK / ..." />
        </el-form-item>
        <el-form-item label="Tags">
          <el-input v-model="createForm.tags_text" placeholder="reality, global (逗号分隔)" />
        </el-form-item>
        <el-form-item label="排序权重">
          <el-input-number v-model="createForm.sort_order" />
        </el-form-item>

        <el-divider content-position="left">Reality 参数</el-divider>
        <el-form-item label="伪装目标 (dest)">
          <el-input v-model="createForm.reality_dest" placeholder="yahoo.com:443" />
        </el-form-item>
        <el-form-item label="伪装域名 (SNI)">
          <el-input v-model="createForm.reality_server_name" placeholder="yahoo.com" />
        </el-form-item>
        <el-form-item label="密钥对">
          <el-button @click="genKeys">生成 Reality 密钥</el-button>
          <div v-if="createForm.private_key" style="margin-top: 8px">
            <div style="font-size: 12px; color: var(--text-muted)">privateKey</div>
            <code style="font-size: 12px; word-break: break-all">{{ createForm.private_key }}</code>
            <div style="font-size: 12px; color: var(--text-muted); margin-top: 4px">publicKey</div>
            <code style="font-size: 12px; word-break: break-all">{{ createForm.public_key }}</code>
            <div style="font-size: 12px; color: var(--text-muted); margin-top: 4px">shortId</div>
            <code style="font-size: 12px">{{ createForm.short_id }}</code>
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDialog = false">取消</el-button>
        <el-button type="primary" :loading="createBusy" @click="submitCreate">创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="editDialog" title="编辑节点" width="540px">
      <div v-if="editing">
        <el-descriptions :column="1" border size="small" style="margin-bottom: 16px">
          <el-descriptions-item label="服务器 / inbound">
            {{ editing.panel_name }} / {{ editing.inbound_id }}
          </el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-switch
              :model-value="editing.enabled"
              :loading="enabledBusy[editing.id]"
              @change="onEditingEnabledChange"
            />
          </el-descriptions-item>
        </el-descriptions>

        <el-form label-width="100px">
          <el-form-item label="显示名" required>
            <el-input v-model="editForm.display_name" />
          </el-form-item>
          <el-form-item label="服务器地址">
            <el-input v-model="editForm.server_address" placeholder="hinet.kazuha.org" />
            <div style="color: var(--text-muted); font-size: 12px; margin-top: 4px">
              朋友连接时使用的公网域名/IP；改这里只影响订阅渲染，不动 3X-UI 端配置
            </div>
          </el-form-item>
          <el-form-item label="Region" required>
            <el-input v-model="editForm.region" placeholder="TW / US / HK / ..." />
          </el-form-item>
          <el-form-item label="Tags">
            <el-input v-model="editForm.tags_text" placeholder="reality, global (逗号分隔)" />
          </el-form-item>
          <el-form-item label="排序权重">
            <el-input-number v-model="editForm.sort_order" />
            <span style="margin-left: 8px; color: var(--text-muted); font-size: 12px">
              越小越靠前；分组级 layout 可覆盖此值
            </span>
          </el-form-item>
        </el-form>
      </div>
      <template #footer>
        <el-button @click="editDialog = false">取消</el-button>
        <el-button type="primary" :loading="editBusy" @click="submitEdit">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="importDialog" title="纳管现有 inbound" width="500px">
      <el-form label-width="120px" :model="importForm">
        <el-form-item label="服务器">
          <el-input :model-value="importForm.panel_name" disabled />
        </el-form-item>
        <el-form-item label="inbound ID">
          <el-input :model-value="importForm.inbound_id" disabled />
        </el-form-item>
        <el-form-item label="显示名" required>
          <el-input v-model="importForm.display_name" />
        </el-form-item>
        <el-form-item label="服务器地址" required>
          <el-input v-model="importForm.server_address" placeholder="hinet.kazuha.org" />
          <div style="color: var(--text-muted); font-size: 12px; margin-top: 4px">
            朋友连接时使用的公网域名/IP
          </div>
        </el-form-item>
        <el-form-item label="Region" required>
          <el-input v-model="importForm.region" placeholder="TW / US / HK / ..." />
        </el-form-item>
        <el-form-item label="Tags">
          <el-input v-model="importForm.tags_text" placeholder="reality, global (逗号分隔)" />
        </el-form-item>
        <el-form-item label="排序权重">
          <el-input-number v-model="importForm.sort_order" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="importDialog = false">取消</el-button>
        <el-button type="primary" :loading="importBusy" @click="submitImport">纳管</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.selection-count {
  color: var(--text-muted);
  white-space: nowrap;
}
</style>
