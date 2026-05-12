<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

interface NavItem {
  path: string
  label: string
  icon: string
}

const nav: NavItem[] = [
  { path: '/admin/dashboard', label: '总览', icon: 'DataLine' },
  { path: '/admin/users', label: '用户', icon: 'User' },
  { path: '/admin/nodes', label: '节点', icon: 'Connection' },
  { path: '/admin/groups', label: '分组', icon: 'Files' },
  { path: '/admin/rules', label: '规则集', icon: 'List' },
  { path: '/admin/templates', label: '模板', icon: 'Document' },
  { path: '/admin/traffic', label: '流量', icon: 'TrendCharts' },
  { path: '/admin/audit', label: '审计', icon: 'Clock' },
]

const active = computed(() => route.path)

function logout() {
  auth.logout()
  router.push('/login')
}
</script>

<template>
  <el-container style="height: 100vh">
    <el-aside width="200px" style="background: #001428; color: #fff">
      <div class="brand">PSP</div>
      <el-menu
        :default-active="active"
        background-color="#001428"
        text-color="#cfd3dc"
        active-text-color="#409eff"
        router
      >
        <el-menu-item v-for="n in nav" :key="n.path" :index="n.path">
          <el-icon><component :is="n.icon" /></el-icon>
          <template #title>{{ n.label }}</template>
        </el-menu-item>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header
        style="
          background: #fff;
          display: flex;
          justify-content: space-between;
          align-items: center;
          border-bottom: 1px solid #ebeef5;
        "
      >
        <div></div>
        <div>
          <span style="margin-right: 12px"
            >{{ auth.username }} · {{ auth.source === 'sso' ? 'SSO' : '本地' }}</span
          >
          <el-button text @click="logout">退出</el-button>
        </div>
      </el-header>
      <el-main>
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<style scoped>
.brand {
  font-size: 20px;
  font-weight: 600;
  color: #fff;
  text-align: center;
  padding: 16px 0;
  letter-spacing: 2px;
}
</style>
