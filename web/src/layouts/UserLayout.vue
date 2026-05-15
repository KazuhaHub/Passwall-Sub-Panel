<script setup lang="ts">
import { useRouter } from 'vue-router'
import LineIcon from '@/components/LineIcon.vue'
import { useAuthStore } from '@/stores/auth'
import { useSiteStore } from '@/stores/site'
import { useTheme } from '@/composables/useTheme'

const auth = useAuthStore()
const site = useSiteStore()
const router = useRouter()
const { isDark, toggleTheme } = useTheme()

site.load()

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
  <div class="user-layout">
    <!-- Header -->
    <header class="header">
      <div class="brand">
        <img class="logo" :src="isDark ? site.logoDark : site.logoLight" alt="Logo" />
        <span class="title">{{ site.appTitle }}</span>
        <span class="badge">个人中心</span>
      </div>
      
      <div style="display: flex; align-items: center; gap: 16px;">
        <el-button text circle @click="toggleTheme" style="font-size: 18px; color: var(--text-muted);">
          <LineIcon :name="isDark ? 'moon' : 'sun'" :size="19" />
        </el-button>

        <el-dropdown trigger="click" @command="handleUserCommand">
        <div class="user-profile">
          <div class="avatar">{{ auth.label.charAt(0).toUpperCase() }}</div>
          <div class="user-info">
            <div class="user-name">{{ auth.label }}</div>
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

    <!-- Main Content -->
    <main class="content">
      <div class="content-container">
        <router-view v-slot="{ Component }">
          <transition name="fade" mode="out-in">
            <component :is="Component" />
          </transition>
        </router-view>
      </div>
    </main>
  </div>
</template>

<style scoped>
.user-layout {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background-color: var(--main-bg);
  color: var(--text-main);
}

/* Header (Glassmorphism) */
.header {
  height: 72px;
  background: var(--header-bg);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  border-bottom: 1px solid var(--header-border);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 40px;
  position: sticky;
  top: 0;
  z-index: 50;
}

.brand {
  display: flex;
  align-items: center;
  gap: 12px;
}

.logo {
  height: 36px;
  object-fit: contain;
}

.title {
  font-weight: 700;
  font-size: 18px;
  letter-spacing: 0.5px;
}

.badge {
  background: rgba(99, 102, 241, 0.1);
  color: #6366f1;
  padding: 4px 8px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 600;
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

.user-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-main);
}

/* Content Area */
.content {
  flex: 1;
  overflow-y: auto;
  padding: 40px 20px;
}

.content-container {
  max-width: 1200px;
  margin: 0 auto;
  width: 100%;
}

/* Responsive Styles */
@media (max-width: 768px) {
  .header {
    padding: 0 16px;
  }
  
  .title {
    display: none;
  }
  
  .badge {
    display: none;
  }
  
  .user-name {
    display: none;
  }
  
  .user-profile {
    padding: 4px;
  }
  
  .content {
    padding: 24px 20px;
  }
}
</style>
