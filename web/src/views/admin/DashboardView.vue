<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { listUsers } from '@/api/users'
import { listNodes } from '@/api/nodes'
import { listGroups } from '@/api/groups'

const userCount = ref(0)
const nodeCount = ref(0)
const groupCount = ref(0)
const loading = ref(true)

onMounted(async () => {
  try {
    const [u, n, g] = await Promise.all([
      listUsers({ page: 1, page_size: 1 }),
      listNodes(),
      listGroups(),
    ])
    userCount.value = u.total
    nodeCount.value = n.length
    groupCount.value = g.items.length
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="psp-page">
    <div class="psp-page-header">
      <div class="psp-page-title">总览</div>
    </div>
    <el-row :gutter="20" v-loading="loading">
      <el-col :span="6">
        <el-card>
          <div style="font-size: 13px; color: #909399">用户总数</div>
          <div style="font-size: 32px; font-weight: 600">{{ userCount }}</div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card>
          <div style="font-size: 13px; color: #909399">节点总数</div>
          <div style="font-size: 32px; font-weight: 600">{{ nodeCount }}</div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card>
          <div style="font-size: 13px; color: #909399">分组总数</div>
          <div style="font-size: 32px; font-weight: 600">{{ groupCount }}</div>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>
