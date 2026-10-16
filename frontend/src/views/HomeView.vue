<script setup lang="ts">
import { computed } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import NavigationCard from '../components/NavigationCard.vue'
import embyIcon from '../assets/emby.svg'
import telegramIcon from '../assets/telegram/telegram.svg'
import { managementCategories } from '../utils/managementFeatures'

const props = defineProps<{ username: string; category: keyof typeof managementCategories }>()
const emit = defineEmits<{ openDashboard: []; category: [value: 'p115' | 'telegram']; openP115: []; openShare: []; openTelegram: []; openLogs: []; logout: [] }>()
// 分类页只展示功能入口，不加载业务配置或启动状态轮询。
const current = computed(() => managementCategories[props.category])
</script>

<template>
  <ManagementLayout :title="current.title" :description="current.description" @logout="emit('logout')">
    <div class="grid items-start gap-3 sm:gap-4 md:grid-cols-2">
      <NavigationCard v-for="feature in current.features" :key="feature.to" :to="feature.to" :title="feature.title" :description="feature.description">
        <template #icon>
          <img v-if="feature.icon === 'emby'" :src="embyIcon" alt="" aria-hidden="true" class="h-7 w-7" draggable="false" />
          <img v-else-if="feature.icon === 'telegram'" :src="telegramIcon" alt="" aria-hidden="true" class="h-6 w-6 object-contain" draggable="false" />
          <svg v-else class="h-6 w-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" aria-hidden="true">
            <path v-if="feature.icon === 'cloud'" d="M6 19a5 5 0 0 1-1-9.9A7 7 0 0 1 18.7 8a5.5 5.5 0 0 1-.2 11H6Z" />
            <template v-else-if="feature.icon === 'play'"><rect x="3" y="4" width="18" height="16" rx="3"/><path d="m10 8 6 4-6 4V8Z"/></template>
            <template v-else-if="feature.icon === 'download'"><path d="M12 3v11m0 0 4-4m-4 4-4-4"/><path d="M5 17v2a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-2"/></template>
            <template v-else-if="feature.icon === 'monitor'"><path d="M4 5h6l2 2h8v12H4z"/><path d="M8 12h8m-4-4v8"/></template>
            <template v-else-if="feature.icon === 'users'"><circle cx="9" cy="7" r="4"/><path d="M3 21v-2a6 6 0 0 1 12 0v2M17 4a4 4 0 0 1 0 8m4 9v-2a6 6 0 0 0-4-5.7"/></template>
            <template v-else-if="feature.icon === 'invite'"><circle cx="9" cy="7" r="4"/><path d="M3 21v-2a6 6 0 0 1 12 0v2M19 8v6m-3-3h6"/></template>
          </svg>
        </template>
      </NavigationCard>
    </div>
  </ManagementLayout>
</template>
