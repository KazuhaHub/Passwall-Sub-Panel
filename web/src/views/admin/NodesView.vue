<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  deleteNode,
  importNode,
  listNodes,
  listUnmanagedInbounds,
  setNodeEnabled,
} from '@/api/nodes'
import type { Node, UnmanagedInbound } from '@/api/types'

const tab = ref<'managed' | 'unmanaged'>('managed')
const managed = ref<Node[]>([])
const unmanaged = ref<UnmanagedInbound[]>([])
const loading = ref(false)

const importDialog = ref(false)
const importBusy = ref(false)
const importForm = reactive({
  panel_name: '',
  inbound_id: 0,
  display_name: '',
  server_address: '',
  region: '',
  tags_text: '',
  sort_order: 100,
})

async function load() {
  loading.value = true
  try {
    if (tab.value === 'managed') {
      managed.value = await listNodes()
    } else {
      const res = await listUnmanagedInbounds()
      unmanaged.value = res.items
    }
  } finally {
    loading.value = false
  }
}

function startImport(row: UnmanagedInbound) {
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
      panel_name: importForm.panel_name,
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

async function confirmDelete(row: Node) {
  await ElMessageBox.confirm(
    `确定删除节点 ${row.display_name}？会先清除该 inbound 内所有面板纳管 client，再删除 inbound 本身（要求 inbound 内只剩纳管 client）。`,
    '确认删除',
    { type: 'warning' },
  )
  await deleteNode(row.id)
  ElMessage.success('已删除')
  await load()
}

async function toggleEnabled(row: Node) {
  await setNodeEnabled(row.id, !row.enabled)
  await load()
}

onMounted(load)
</script>

<template>
  <div class="psp-page">
    <div class="psp-page-header">
      <div class="psp-page-title">节点管理</div>
    </div>

    <el-tabs v-model="tab" @tab-change="load">
      <el-tab-pane label="纳管中" name="managed">
        <el-table v-loading="loading" :data="managed" stripe>
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
          <el-table-column label="面板/inbound" min-width="160">
            <template #default="{ row }">
              {{ row.panel_name }} / {{ row.inbound_id }}
            </template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag v-if="row.enabled" type="success" size="small">启用</el-tag>
              <el-tag v-else type="info" size="small">禁用</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="200" fixed="right">
            <template #default="{ row }">
              <el-button size="small" @click="toggleEnabled(row)">
                {{ row.enabled ? '禁用' : '启用' }}
              </el-button>
              <el-button size="small" type="danger" @click="confirmDelete(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="未纳管" name="unmanaged">
        <el-table v-loading="loading" :data="unmanaged" stripe>
          <el-table-column prop="PanelName" label="面板" width="120" />
          <el-table-column prop="InboundID" label="inbound" width="100" />
          <el-table-column prop="Protocol" label="协议" width="100" />
          <el-table-column prop="Port" label="端口" width="80" />
          <el-table-column prop="Remark" label="3X-UI 备注" min-width="240" />
          <el-table-column label="client 数" width="100">
            <template #default="{ row }">{{ row.ClientCount }}</template>
          </el-table-column>
          <el-table-column label="操作" width="120" fixed="right">
            <template #default="{ row }">
              <el-button type="primary" size="small" @click="startImport(row)">纳管</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="importDialog" title="纳管现有 inbound" width="500px">
      <el-form label-width="120px" :model="importForm">
        <el-form-item label="面板">
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
          <div style="color: #909399; font-size: 12px; margin-top: 4px">
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
