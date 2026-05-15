<script setup lang="ts">
import { computed, type Component } from 'vue'
import {
  ChartNoAxesCombined,
  CircleUser,
  ClipboardList,
  Clock,
  FileText,
  FolderKanban,
  Home,
  KeyRound,
  LayoutDashboard,
  Link2,
  ListChecks,
  Lock,
  LogIn,
  Mail,
  Menu,
  Moon,
  Network,
  Palette,
  Pencil,
  Plus,
  RefreshCw,
  Server,
  Settings,
  Sun,
  Trash2,
  TriangleAlert,
  Unlock,
  User,
  Users,
  X,
} from 'lucide-vue-next'

const props = defineProps<{
  name: string
  size?: number
}>()

const icons: Record<string, Component> = {
  dashboard: LayoutDashboard,
  users: Users,
  user: User,
  profile: CircleUser,
  home: Home,
  servers: Server,
  nodes: Network,
  groups: FolderKanban,
  rules: ListChecks,
  templates: FileText,
  link: Link2,
  mail: Mail,
  brand: Palette,
  sso: KeyRound,
  traffic: ChartNoAxesCombined,
  logs: ClipboardList,
  sync: RefreshCw,
  settings: Settings,
  menu: Menu,
  sun: Sun,
  moon: Moon,
  lock: Lock,
  unlock: Unlock,
  trash: Trash2,
  refresh: RefreshCw,
  close: X,
  plus: Plus,
  edit: Pencil,
  clock: Clock,
  warning: TriangleAlert,
  login: LogIn,
}

const icon = computed(() => icons[props.name] || CircleUser)
</script>

<template>
  <component
    :is="icon"
    class="line-icon"
    :size="size || 22"
    :stroke-width="1.8"
    aria-hidden="true"
  />
</template>

<style scoped>
.line-icon {
  display: inline-block;
  flex-shrink: 0;
}
</style>
