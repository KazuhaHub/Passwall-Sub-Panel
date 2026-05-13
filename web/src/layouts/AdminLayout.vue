<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useSiteStore } from '@/stores/site'
import { useTheme } from '@/composables/useTheme'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const site = useSiteStore()
const { isDark, toggleTheme } = useTheme()

const isMobileMenuOpen = ref(false)

site.load()

interface NavItem {
  path: string
  label: string
  icon: string
}

const nav: NavItem[] = [
  { path: '/admin/dashboard', label: '总览', icon: 'DataLine' },
  { path: '/admin/users', label: '用户管理', icon: 'User' },
  { path: '/admin/servers', label: '服务器', icon: 'Cpu' },
  { path: '/admin/nodes', label: '节点管理', icon: 'Connection' },
  { path: '/admin/groups', label: '分组', icon: 'Files' },
  { path: '/admin/rules', label: '规则库', icon: 'List' },
  { path: '/admin/templates', label: '配置方案', icon: 'Document' },
  { path: '/admin/traffic', label: '流量统计', icon: 'TrendCharts' },
  { path: '/admin/sync-tasks', label: '同步任务', icon: 'Refresh' },
  { path: '/admin/audit', label: '审计日志', icon: 'Clock' },
  { path: '/admin/settings', label: '系统设置', icon: 'Setting' },
]

const currentRouteName = computed(() => {
  const current = nav.find(n => n.path === route.path)
  return current ? current.label : route.name
})

function logout() {
  auth.logout()
  router.push('/login')
}

function handleUserCommand(cmd: string) {
  if (cmd === 'logout') {
    logout()
  }
}
</script>

<template>
  <div class="layout-wrapper">
    <!-- Mobile Sidebar Overlay -->
    <div 
      v-if="isMobileMenuOpen" 
      class="sidebar-overlay" 
      @click="isMobileMenuOpen = false"
    ></div>

    <!-- Sidebar -->
    <aside class="sidebar" :class="{ 'is-open': isMobileMenuOpen }">
      <div class="brand">
        <img class="brand-logo" :src="site.logoDark" alt="Logo" />
        <span class="brand-title">{{ site.appTitle }}</span>
      </div>
      <div class="nav-menu">
        <router-link
          v-for="n in nav"
          :key="n.path"
          :to="n.path"
          class="nav-item"
          active-class="active"
          @click="isMobileMenuOpen = false"
        >
          <el-icon class="nav-icon"><component :is="n.icon" /></el-icon>
          {{ n.label }}
        </router-link>
      </div>
    </aside>

    <!-- Main Content Wrapper -->
    <div class="main-wrapper">
      <!-- Header -->
      <header class="header">
        <div class="breadcrumbs">
          <el-button class="mobile-menu-btn" text circle @click="isMobileMenuOpen = true">
            <el-icon><Expand /></el-icon>
          </el-button>
          <span class="desktop-only">{{ site.siteTitle }}</span>
          <span class="separator desktop-only">/</span>
          <span class="current">{{ currentRouteName }}</span>
        </div>
        
        <div class="header-actions">
          <el-button text circle @click="toggleTheme" style="font-size: 18px; color: var(--text-muted); margin-left: 8px;">
            <el-icon><component :is="isDark ? 'Moon' : 'Sunny'" /></el-icon>
          </el-button>
          
          <el-dropdown trigger="click" @command="handleUserCommand">
            <div class="user-profile">
              <div class="avatar">{{ auth.label.charAt(0).toUpperCase() }}</div>
              <div class="user-info">
                <div class="user-name">{{ auth.label }}</div>
                <div class="user-role">{{ auth.role === 'admin' ? '管理员' : '用户' }}</div>
              </div>
            </div>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="logout">退出登录</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </header>

      <!-- Page Content -->
      <main class="content">
        <router-view v-slot="{ Component }">
          <transition name="fade" mode="out-in">
            <component :is="Component" />
          </transition>
        </router-view>
      </main>
    </div>
  </div>
</template>

<style scoped>
.layout-wrapper {
  display: flex;
  height: 100vh;
  width: 100vw;
  background-color: var(--main-bg);
  color: var(--text-main);
  overflow: hidden;
  position: relative;
}

/* Sidebar Overlay */
.sidebar-overlay {
  display: none;
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.4);
  z-index: 90;
  backdrop-filter: blur(2px);
}

/* Sidebar */
.sidebar {
  width: 260px;
  background-color: var(--sidebar-bg);
  display: flex;
  flex-direction: column;
  transition: var(--transition);
  box-shadow: 4px 0 24px rgba(0, 0, 0, 0.05);
  z-index: 10;
  flex-shrink: 0;
}

.brand {
  height: 80px;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 0 24px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.05);
}

.brand-logo {
  height: 36px;
  object-fit: contain;
}

.brand-title {
  color: #fff;
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 0.5px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.nav-menu {
  padding: 24px 16px;
  flex: 1;
  overflow-y: auto;
}

.nav-item {
  display: flex;
  align-items: center;
  padding: 12px 16px;
  color: var(--sidebar-text);
  text-decoration: none;
  border-radius: 12px;
  margin-bottom: 8px;
  font-weight: 500;
  transition: var(--transition);
  cursor: pointer;
}

.nav-item:hover {
  color: var(--sidebar-text-active);
  background-color: rgba(255, 255, 255, 0.05);
  transform: translateX(4px);
}

.nav-item.active {
  color: var(--sidebar-text-active);
  background: var(--sidebar-active-bg);
  box-shadow: 0 4px 12px rgba(99, 102, 241, 0.3);
}

.nav-icon {
  font-size: 18px;
  margin-right: 12px;
  opacity: 0.8;
  transition: var(--transition);
}

.nav-item.active .nav-icon {
  opacity: 1;
}

/* Main Content Wrapper */
.main-wrapper {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-width: 0;
}

/* Header */
.header {
  height: 72px;
  background: var(--header-bg);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  border-bottom: 1px solid var(--header-border);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 32px;
  z-index: 5;
  flex-shrink: 0;
}

.breadcrumbs {
  display: flex;
  align-items: center;
  color: var(--text-muted);
  font-size: 14px;
}

.breadcrumbs .separator {
  margin: 0 8px;
}

.breadcrumbs .current {
  color: var(--text-main);
  font-weight: 600;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 24px;
}

.user-profile {
  display: flex;
  align-items: center;
  gap: 12px;
  cursor: pointer;
  padding: 4px 12px 4px 4px;
  border-radius: 24px;
  transition: var(--transition);
  outline: none;
}

.user-profile:hover {
  background: var(--card-bg);
}

.avatar {
  width: 36px;
  height: 36px;
  border-radius: 50%;
  background: var(--sidebar-active-bg);
  color: white;
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 600;
  box-shadow: 0 2px 8px rgba(99, 102, 241, 0.2);
}

.user-info {
  display: flex;
  flex-direction: column;
}

.user-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-main);
}

.user-role {
  font-size: 11px;
  color: var(--text-muted);
}

/* Page Content */
.content {
  flex: 1;
  padding: 24px;
  overflow-y: auto;
  overflow-x: hidden;
  position: relative;
  min-width: 0;
}

.mobile-menu-btn {
  display: none;
  font-size: 20px;
  color: var(--text-main);
  margin-right: 12px;
}

/* Responsive Styles */
@media (max-width: 768px) {
  .sidebar {
    position: fixed;
    top: 0;
    bottom: 0;
    left: 0;
    transform: translateX(-100%);
    z-index: 100;
  }
  
  .sidebar.is-open {
    transform: translateX(0);
  }
  
  .sidebar-overlay {
    display: block;
  }
  
  .mobile-menu-btn {
    display: inline-flex;
  }
  
  .desktop-only {
    display: none !important;
  }
  
  .header {
    padding: 0 16px;
  }
  
  .user-info {
    display: none;
  }
  
  .user-profile {
    padding: 4px;
  }
  
  .content {
    padding: 16px;
  }
}
</style>
